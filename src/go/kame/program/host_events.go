package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func (p *Program) pump(wait int) {
	if p.Host == nil {
		return
	}
	p.Host.Pump(wait)
	for {
		next := p.Host.Next()
		if !next.OK {
			break
		}
		event := next.Event
		if entry := p.serviceProbeForRequest(event.ID); entry != nil {
			if event.Kind == host.ProcessTerminal {
				p.completeServiceReadyProbe(entry, event)
			}
			event.Free(p.Alloc)
			continue
		}
		entry := p.instanceForRequest(event.ID)
		if entry == nil && (event.Kind == host.ProcessStarted || event.Kind == host.ProcessTerminal) {
			for i := range p.Pending {
				if p.Pending[i].ID == event.ID {
					entry = p.streamInstance(p.Pending[i].NodeID)
					break
				}
			}
		}
		if event.Kind == host.ProcessStderr || event.Kind == host.ProcessStdout {
			for i := range p.Pending {
				if p.Pending[i].ID == event.ID && (p.Pending[i].Capture || p.Pending[i].Stream) {
					if event.Kind == host.ProcessStderr {
						p.captureStderr(p.Pending[i], event.Data)
					} else if p.Pending[i].Stream {
						p.statementStdout(p.Pending[i], event.Data)
					}
					break
				}
			}
		}
		if entry != nil && event.Kind == host.ProcessStdout {
			p.emitNode(entry.Node, entry.Plan.Target, Stdout, diagnostic.Span{}, event.Data)
		} else if entry != nil && event.Kind == host.ProcessStderr {
			p.emitNode(entry.Node, entry.Plan.Target, Stderr, diagnostic.Span{}, event.Data)
		} else if entry != nil && event.Kind == host.ProcessStarted {
			p.emitProcess(entry, ProcessStarted, event.ID)
			p.serviceSpawned(entry)
		} else if event.Kind == host.ProcessTerminal {
			if entry != nil {
				p.emitProcess(entry, ProcessExited, event.ID)
			}
			p.complete(event)
		}
		event.Free(p.Alloc)
	}
	p.drainCancellations()
	p.observeInstances()
}

// drainRequests services evaluator operations before scheduling resumed nodes.
func (p *Program) drainRequests() {
	for {
		next := p.Eval.Requests.Next()
		if !next.OK {
			return
		}
		request := next.Request
		if p.Forwarding {
			if request.Kind == host.RequestProcess {
				display := processDisplayFromPayload(p.Alloc, request.Payload, p.Options.Shell)
				pending := pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Program: display.Program, Argv: display.Argv, DisplayTruncated: display.Truncated}
				p.Pending = slices.Append(p.Alloc, p.Pending, pending)
			}
			// Ownership transfers to the outbound queue; the embedding host
			// frees the request when it completes it.
			p.Outbound = slices.Append(p.Alloc, p.Outbound, request)
			continue
		}
		if request.Kind == host.RequestProcess {
			script := host.PayloadText(request.Payload, host.FieldScript)
			values := host.PayloadArgv(request.Payload)
			var argv []string
			for i := range values {
				argv = slices.Append(p.Alloc, argv, values[i].Text)
			}
			var stages []host.ProcessStage
			graphs := host.PayloadStages(request.Payload)
			setups := host.PayloadSetups(request.Payload)
			for i := range graphs {
				var arguments []string
				for j := range graphs[i].List {
					arguments = slices.Append(p.Alloc, arguments, graphs[i].List[j].Text)
				}
				stage := host.ProcessStage{Argv: arguments}
				if i < len(setups) {
					stage.Directory, stage.TimeoutMS = host.PayloadText(setups[i], host.FieldCwd), host.PayloadInt(setups[i], host.FieldTimeout)
					values := host.PayloadList(setups[i], host.FieldEnvironment)
					for j := range values {
						stage.Environment = slices.Append(p.Alloc, stage.Environment, values[j].Text)
					}
				}
				stages = slices.Append(p.Alloc, stages, stage)
			}
			// The shell operation returns captured output, so it needs a
			// retained-byte budget even when no --log-limit was given.
			retain := p.Options.RetainBytes
			if retain < cacheLogDefault {
				retain = cacheLogDefault
			}
			capture := len(argv) != 0 || len(stages) != 0
			stream := false
			for i := range request.Payload.Record {
				if request.Payload.Record[i].Key == "stream" {
					stream = request.Payload.Record[i].Value.Bool
				}
			}
			capture = capture && !stream
			if capture {
				retain = p.Options.CaptureLimit
			}
			if stream {
				retain = 0
			}
			environment := p.Options.Environment
			var scopedEnvironment []string
			for i := range request.Payload.Record {
				if request.Payload.Record[i].Key == host.FieldEnvironment {
					values := request.Payload.Record[i].Value.List
					for j := range values {
						scopedEnvironment = slices.Append(p.Alloc, scopedEnvironment, values[j].Text)
					}
					environment = scopedEnvironment
				}
			}
			processRequest := host.ProcessRequest{ID: request.ID, Shell: p.Options.Shell, Argv: argv, Stages: stages, Input: host.PayloadText(request.Payload, host.FieldInput), Output: host.PayloadText(request.Payload, host.FieldOutput), Append: host.PayloadAppend(request.Payload), Script: []byte(script), Directory: p.Options.Directory, Environment: environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}
			if (script == "" && !capture && !stream) || p.Host == nil || !p.Host.Start(processRequest) {
				p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Diagnostic: failure(p.Alloc, "HOST_FAIL", "cannot start shell request")})
			} else {
				display := processDisplayFromHost(p.Alloc, processRequest)
				pending := pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Retries: 0, Capture: capture, Stream: stream, Program: display.Program, Argv: display.Argv, DisplayTruncated: display.Truncated}
				p.Pending = slices.Append(p.Alloc, p.Pending, pending)
			}
			slices.Free(p.Alloc, scopedEnvironment)
			slices.Free(p.Alloc, argv)
			for i := range stages {
				slices.Free(p.Alloc, stages[i].Argv)
				slices.Free(p.Alloc, stages[i].Environment)
			}
			slices.Free(p.Alloc, stages)
		} else {
			p.completeRequest(request)
		}
		request.Free(p.Alloc)
	}
}

// ProcessStarted, ProcessStream, and ProcessTerminal service one forwarded
// process request, emitting the same lifecycle events the native host would so
// --json output stays comparable.
func (p *Program) ProcessStarted(request host.Request) {
	if p.serviceProbeForRequest(request.ID) != nil {
		return
	}
	entry := p.instanceForRequest(request.ID)
	if entry == nil {
		entry = p.serviceForProcessRequest(request.ID)
	}
	if entry == nil {
		entry = p.streamInstance(request.NodeID)
	}
	if entry == nil {
		return
	}
	p.emitProcess(entry, ProcessStarted, request.ID)
	p.serviceSpawned(entry)
}

// ProcessExited publishes a forwarded process terminal event without completing
// its request. Expression operations complete separately with their structured
// result, after the host reports this lifecycle event.
func (p *Program) ProcessExited(request host.Request) {
	if p.serviceProbeForRequest(request.ID) != nil { return }
	entry := p.instanceForRequest(request.ID)
	if entry == nil { entry = p.serviceForProcessRequest(request.ID) }
	if entry == nil { entry = p.streamInstance(request.NodeID) }
	if entry != nil { p.emitProcess(entry, ProcessExited, request.ID) }
}

func (p *Program) serviceSpawned(entry *instance) {
	if entry == nil || entry.Rule.Kind != rule.ServiceRule || entry.ServiceReady {
		return
	}
	if len(entry.Service.ReadyArgv) == 0 {
		p.publishServiceReady(entry)
		return
	}
	p.setServiceState(entry, "checking-ready")
	if p.Forwarding {
		p.requestServiceClock(entry)
		return
	}
	if p.Host == nil {
		return
	}
	entry.ServiceReadyDeadline = p.Host.Monotonic() + entry.Service.ReadyTimeout*1000000
	p.startServiceReadyProbe(entry)
}

func (p *Program) serviceProbeForRequest(id int64) *instance {
	for i := range p.Instances {
		if p.Instances[i].ServiceProbeID == id {
			return &p.Instances[i]
		}
	}
	return nil
}

func (p *Program) serviceClockForRequest(id int64) *instance {
	for i := range p.Instances {
		if p.Instances[i].ServiceClockID == id {
			return &p.Instances[i]
		}
	}
	return nil
}

func (p *Program) serviceTimerForRequest(id int64) *instance {
	for i := range p.Instances {
		if p.Instances[i].ServiceTimerID == id {
			return &p.Instances[i]
		}
	}
	return nil
}

func (p *Program) requestServiceClock(entry *instance) {
	if entry == nil || entry.ServiceClockID != 0 {
		return
	}
	p.nextRequest++
	id := p.nextRequest
	entry.ServiceClockID = id
	p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: id, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, Kind: host.RequestMonotonicTime})
}

func (p *Program) requestServiceTimer(entry *instance) {
	if entry == nil || entry.ServiceTimerID != 0 {
		return
	}
	p.nextRequest++
	id := p.nextRequest
	entry.ServiceTimerID = id
	delay := entry.Service.ReadyInterval
	if entry.ServiceReady {
		delay = entry.Service.HealthInterval
	}
	payload := host.ServiceTimerPayload(p.Alloc, delay)
	p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: id, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, Kind: host.RequestTimer, Payload: payload})
}

func (p *Program) completeServiceClock(entry *instance, now int64) {
	entry.ServiceClockID = 0
	if entry.ServiceReady {
		p.startServiceHealthProbeAt(entry, now)
		return
	}
	if entry.ServiceReadyDeadline == 0 {
		entry.ServiceReadyDeadline = now + entry.Service.ReadyTimeout*1000000
	}
	if now >= entry.ServiceReadyDeadline {
		p.failServiceReady(entry)
		return
	}
	p.startServiceReadyProbeAt(entry, now)
}

func (p *Program) publishServiceReady(entry *instance) {
	entry.ServiceReady, entry.ServiceReadyDeadline, entry.ServiceNextProbe = true, 0, 0
	p.setServiceState(entry, "ready")
	p.Engine.Publish(entry.Node, core.Value{Kind: core.Nil})
	if len(entry.Service.HealthArgv) == 0 {
		return
	}
	if p.Forwarding {
		p.requestServiceTimer(entry)
		return
	}
	if p.Host != nil {
		entry.ServiceNextHealth = p.Host.Monotonic() + entry.Service.HealthInterval*1000000
	}
}

func (p *Program) startServiceReadyProbe(entry *instance) {
	if entry.ServiceReady || entry.ServiceProbeID != 0 || entry.ServiceReadyDeadline == 0 {
		return
	}
	if p.Forwarding {
		p.requestServiceClock(entry)
		return
	}
	if p.Host == nil {
		return
	}
	p.startServiceReadyProbeAt(entry, p.Host.Monotonic())
}

func (p *Program) startServiceReadyProbeAt(entry *instance, now int64) {
	p.startServiceProbeAt(entry, now, false)
}

func (p *Program) startServiceHealthProbeAt(entry *instance, now int64) {
	p.startServiceProbeAt(entry, now, true)
}

func (p *Program) startServiceProbeAt(entry *instance, now int64, health bool) {
	if entry.ServiceProbeID != 0 || (health && (!entry.ServiceReady || len(entry.Service.HealthArgv) == 0)) || (!health && (entry.ServiceReady || entry.ServiceReadyDeadline == 0)) {
		return
	}
	remaining := entry.ServiceReadyDeadline - now
	if !health && remaining <= 0 {
		p.failServiceReady(entry)
		return
	}
	if health {
		p.setServiceState(entry, "checking-health")
	} else {
		p.setServiceState(entry, "checking-ready")
	}
	p.nextRequest++
	id := p.nextRequest
	argv := entry.Service.ReadyArgv
	timeout := (remaining + 999999) / 1000000
	if health {
		argv, timeout = entry.Service.HealthArgv, 0
	}
	argv = cloneStrings(p.Alloc, argv)
	if p.Forwarding {
		payload := host.ServiceProbePayload(p.Alloc, argv, p.Options.Directory, timeout, entry.Environment)
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: id, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, Kind: host.RequestProcess, Payload: payload})
		entry.ServiceProbeID, entry.ServiceProbeHealth = id, health
		freeStrings(p.Alloc, argv)
		return
	}
	if p.Host == nil {
		freeStrings(p.Alloc, argv)
		return
	}
	request := host.ProcessRequest{ID: id, Argv: argv, Directory: p.Options.Directory, Environment: entry.Environment, TimeoutMS: timeout}
	started := p.Host.Start(request)
	freeStrings(p.Alloc, argv)
	if !started {
		if health {
			p.serviceHealthFailure(entry, now)
		} else {
			entry.ServiceNextProbe = now + entry.Service.ReadyInterval*1000000
		}
		return
	}
	entry.ServiceProbeID, entry.ServiceProbeHealth = id, health
}

func (p *Program) completeServiceReadyProbe(entry *instance, event host.ProcessEvent) {
	health := entry.ServiceProbeHealth
	entry.ServiceProbeID = 0
	entry.ServiceProbeHealth = false
	if event.Outcome == host.ProcessExited && event.Status == 0 && event.Signal == 0 {
		if health {
			entry.ServiceHealthFailures = 0
			p.setServiceState(entry, "ready")
			p.scheduleServiceHealth(entry)
		} else {
			p.publishServiceReady(entry)
		}
		return
	}
	if health {
		now := int64(0)
		if p.Host != nil {
			now = p.Host.Monotonic()
		}
		p.serviceHealthFailure(entry, now)
		return
	}
	if event.Outcome == host.ProcessTimedOut {
		p.failServiceReady(entry)
		return
	}
	if p.Forwarding {
		p.requestServiceTimer(entry)
		return
	}
	if p.Host == nil {
		return
	}
	now := p.Host.Monotonic()
	if now >= entry.ServiceReadyDeadline {
		p.failServiceReady(entry)
		return
	}
	entry.ServiceNextProbe = now + entry.Service.ReadyInterval*1000000
}

func (p *Program) scheduleServiceHealth(entry *instance) {
	if entry == nil || len(entry.Service.HealthArgv) == 0 {
		return
	}
	if p.Forwarding {
		p.requestServiceTimer(entry)
		return
	}
	if p.Host != nil {
		entry.ServiceNextHealth = p.Host.Monotonic() + entry.Service.HealthInterval*1000000
	}
}

func (p *Program) serviceHealthFailure(entry *instance, now int64) {
	entry.ServiceHealthFailures++
	if entry.ServiceHealthFailures >= entry.Service.HealthFailures {
		if p.prepareServiceRestart(entry) {
			p.Engine.Invalidate(entry.Node)
			p.drainCancellations()
			return
		}
		message, code := "service failed its health checks", "SERVICE_UNHEALTHY"
		if entry.ServiceRestartCount > 0 {
			message, code = "service restart attempts were exhausted", "SERVICE_RESTART_EXHAUSTED"
		}
		p.Engine.Fail(entry.Node, failure(p.Alloc, code, message))
		p.drainCancellations()
		return
	}
	if p.Forwarding {
		p.setServiceState(entry, "ready")
		p.requestServiceTimer(entry)
	} else {
		p.setServiceState(entry, "ready")
		entry.ServiceNextHealth = now + entry.Service.HealthInterval*1000000
	}
}

func (p *Program) tickServiceReadiness() {
	if p.Host == nil {
		return
	}
	now := p.Host.Monotonic()
	for i := range p.Instances {
		entry := &p.Instances[i]
		if entry.Rule.Kind != rule.ServiceRule || entry.ServiceProbeID != 0 {
			continue
		}
		if entry.ServiceReady {
			if len(entry.Service.HealthArgv) != 0 && entry.ServiceNextHealth != 0 && now >= entry.ServiceNextHealth {
				p.startServiceHealthProbeAt(entry, now)
			}
			continue
		}
		if entry.ServiceReadyDeadline == 0 {
			continue
		}
		if now >= entry.ServiceReadyDeadline {
			p.failServiceReady(entry)
		} else if now >= entry.ServiceNextProbe {
			p.startServiceReadyProbe(entry)
		}
	}
}

func (p *Program) failServiceReady(entry *instance) {
	if entry == nil || entry.ServiceReady || entry.Node.State == core.NodeFailed || entry.Node.State == core.NodeCancelled || entry.Node.State == core.NodeComplete {
		return
	}
	if entry.ServiceProbeID != 0 && p.Host != nil {
		p.Host.Cancel(entry.ServiceProbeID)
		entry.ServiceProbeID = 0
	}
	if p.prepareServiceRestart(entry) {
		p.Engine.Invalidate(entry.Node)
		p.drainCancellations()
		return
	}
	entry.ServiceReadyDeadline, entry.ServiceNextProbe, entry.ServiceNextHealth, entry.ServiceTimerID, entry.ServiceClockID = 0, 0, 0, 0, 0
	code, message := "SERVICE_READY_TIMEOUT", "service did not become ready before timeout"
	if entry.ServiceRestartCount > 0 {
		code, message = "SERVICE_RESTART_EXHAUSTED", "service restart attempts were exhausted"
	}
	p.Engine.Fail(entry.Node, failure(p.Alloc, code, message))
	p.drainCancellations()
}

func (p *Program) ProcessStream(request host.Request, stderr bool, data []byte) {
	if p.serviceProbeForRequest(request.ID) != nil {
		return
	}
	for i := range request.Payload.Record {
		if request.Payload.Record[i].Key == "stream" && request.Payload.Record[i].Value.Bool {
			pending := pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Stream: true}
			if stderr {
				p.captureStderr(pending, data)
			} else {
				p.statementStdout(pending, data)
			}
			return
		}
	}
	if stderr && (len(host.PayloadArgv(request.Payload)) != 0 || len(host.PayloadStages(request.Payload)) != 0) {
		p.captureStderr(pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Capture: true}, data)
		return
	}
	entry := p.instanceForRequest(request.ID)
	if entry == nil {
		return
	}
	kind := Stdout
	if stderr {
		kind = Stderr
	}
	p.emitNode(entry.Node, entry.Plan.Target, kind, diagnostic.Span{}, data)
}

// ProcessRetainLimit reports the native retention budget for a forwarded
// request. An embedding host may keep that prefix plus one sentinel byte, so
// ProcessTerminal can detect truncation without receiving the entire stream.
func (p *Program) ProcessRetainLimit(request host.Request) int {
	isInstance := p.instanceForRequest(request.ID) != nil || p.serviceForProcessRequest(request.ID) != nil
	retain := p.Options.RetainBytes
	if isInstance {
		// Recipes retain only what the native host would: nothing for a file
		// rule, the cache bound for a cached task.
		entry := p.instanceForRequest(request.ID)
		if entry != nil && entry.Rule.Kind == rule.ServiceRule {
			retain = int(entry.Service.LogBytes)
		} else if entry != nil && entry.Rule.Kind == rule.CachedTaskRule && p.Options.CacheRetainBytes > retain {
			retain = p.Options.CacheRetainBytes
		}
	} else if retain < cacheLogDefault {
		// A collected shell operation returns captured output, so it needs a
		// retained-byte budget even without --log-limit.
		retain = cacheLogDefault
	}
	return retain
}

// ProcessTerminal adapts a forwarded process terminal into the same event a
// native ProcessHost would emit, so completion values and failure diagnostics
// match the native path. Captured output is bounded like the native host and
// copied into the program allocator because the caller's bytes are transient.
func (p *Program) ProcessTerminal(request host.Request, stdout []byte, stderr []byte, status int, signal int, outcome int, code string, message string) {
	if entry := p.serviceProbeForRequest(request.ID); entry != nil {
		event := host.ProcessEvent{Kind: host.ProcessTerminal, ID: request.ID, Status: status, Signal: signal, Outcome: host.ProcessOutcome(outcome)}
		if outcome == 1 {
			event.Outcome = host.ProcessTimedOut
		} else if outcome == 2 {
			event.Outcome = host.ProcessCancelled
		} else if outcome == 3 {
			event.Outcome = host.ProcessFailed
		}
		p.completeServiceReadyProbe(entry, event)
		return
	}
	entry := p.instanceForRequest(request.ID)
	if entry == nil {
		entry = p.serviceForProcessRequest(request.ID)
	}
	if entry == nil {
		entry = p.streamInstance(request.NodeID)
	}
	if entry != nil {
		p.emitProcess(entry, ProcessExited, request.ID)
	}
	isInstance := p.instanceForRequest(request.ID) != nil || p.serviceForProcessRequest(request.ID) != nil
	retain := p.ProcessRetainLimit(request)
	event := host.ProcessEvent{Kind: host.ProcessTerminal, ID: request.ID, Status: status, Signal: signal, Outcome: host.ProcessExited, RetainBytes: retain}
	if outcome == 1 {
		event.Outcome = host.ProcessTimedOut
	} else if outcome == 2 {
		event.Outcome = host.ProcessCancelled
	} else if outcome == 3 {
		event.Outcome = host.ProcessFailed
	}
	event.StdoutTruncated = len(stdout) > retain
	if event.StdoutTruncated {
		stdout = stdout[:retain]
	}
	if len(stdout) != 0 {
		event.Stdout = mem.AllocSlice[byte](p.Alloc, len(stdout), len(stdout))
		copy(event.Stdout, stdout)
	}
	event.StderrTruncated = len(stderr) > retain
	if event.StderrTruncated {
		stderr = stderr[:retain]
	}
	if len(stderr) != 0 {
		event.Stderr = mem.AllocSlice[byte](p.Alloc, len(stderr), len(stderr))
		copy(event.Stderr, stderr)
	}
	if code != "" {
		event.Diagnostic.Code = cloneText(p.Alloc, code)
		event.Diagnostic.Message = cloneText(p.Alloc, message)
	}
	if isInstance {
		p.complete(event)
	} else {
		p.completeShell(request, event)
	}
	event.Free(p.Alloc)
}

// completeShell resumes a forwarded evaluator process request, a collected
// shell operation whose node is not a program instance. A non-zero exit is part
// of the operation's value rather than a failure, matching the native pending
// path; only a host, timeout, or cancellation outcome produces a diagnostic.
func (p *Program) completeShell(request host.Request, event host.ProcessEvent) {
	var d diagnostic.Diagnostic
	if event.Outcome == host.ProcessFailed {
		d = failure(p.Alloc, "HOST_FAIL", event.Diagnostic.Message)
	} else if event.Outcome == host.ProcessTimedOut {
		d = failure(p.Alloc, "RECIPE_TIMEOUT", "recipe timed out")
	} else if event.Outcome == host.ProcessCancelled {
		d = failure(p.Alloc, "EXEC_CANCELLED", "recipe cancelled")
	}
	completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Diagnostic: d}
	if d.Code == "" {
		completion.Value, completion.HasValue = shellValue(p.Alloc, event), true
	}
	p.Engine.Complete(completion)
}

// NextOutbound pops the oldest request an embedding host must service. The
// returned request is caller-owned and must be freed after completion.
func (p *Program) NextOutbound() host.NextResult {
	if p == nil || len(p.Outbound) == 0 {
		return host.NextResult{}
	}
	request := p.Outbound[0]
	copy(p.Outbound, p.Outbound[1:])
	p.Outbound = p.Outbound[:len(p.Outbound)-1]
	return host.NextResult{Request: request, OK: true}
}

// Complete feeds one host completion back to the engine. The completion value
// and diagnostic are transferred to the engine.
func (p *Program) Complete(request host.Request, value core.Value, diagnostic diagnostic.Diagnostic) {
	if p == nil {
		value.Free(mem.System)
		diagnostic.Free(mem.System)
		return
	}
	if entry := p.serviceProbeForRequest(request.ID); entry != nil {
		event := host.ProcessEvent{Kind: host.ProcessTerminal, ID: request.ID, Outcome: host.ProcessExited}
		if diagnostic.Code == "RECIPE_TIMEOUT" {
			event.Outcome = host.ProcessTimedOut
		} else if diagnostic.Code != "" {
			event.Outcome = host.ProcessFailed
		} else if value.Kind == core.Record {
			for i := range value.Record {
				if value.Record[i].Key == "status" {
					event.Status = int(value.Record[i].Value.Int)
				}
				if value.Record[i].Key == "signal" {
					event.Signal = int(value.Record[i].Value.Int)
				}
			}
		} else if value.Kind != core.String {
			event.Status = 1
		}
		value.Free(p.Alloc)
		diagnostic.Free(p.Alloc)
		p.completeServiceReadyProbe(entry, event)
		return
	}
	if entry := p.serviceTimerForRequest(request.ID); entry != nil {
		entry.ServiceTimerID = 0
		value.Free(p.Alloc)
		if diagnostic.Code == "" {
			p.requestServiceClock(entry)
		} else {
			diagnostic.Free(p.Alloc)
			p.failServiceHost(entry)
		}
		return
	}
	if entry := p.serviceRestartTimerForRequest(request.ID); entry != nil {
		entry.ServiceRestartTimerID, entry.ServiceRestartDeadline = 0, 0
		if diagnostic.Code != "" {
			value.Free(p.Alloc)
			p.failServiceHost(entry)
			diagnostic.Free(p.Alloc)
			return
		}
		entry.ServiceRestartPending = false
		p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Value: value, HasValue: true, Diagnostic: diagnostic})
		return
	}
	if entry := p.serviceClockForRequest(request.ID); entry != nil {
		if diagnostic.Code != "" || value.Kind != core.Int {
			value.Free(p.Alloc)
			diagnostic.Free(p.Alloc)
			p.failServiceHost(entry)
			return
		}
		now := value.Int
		value.Free(p.Alloc)
		diagnostic.Free(p.Alloc)
		p.completeServiceClock(entry, now)
		return
	}
	if request.Kind == host.RequestTimer || request.Kind == host.RequestProcessCancel {
		value.Free(p.Alloc)
		diagnostic.Free(p.Alloc)
		return
	}
	if request.Kind == host.RequestProcess && (len(host.PayloadArgv(request.Payload)) != 0 || len(host.PayloadStages(request.Payload)) != 0) {
		if entry := p.streamInstance(request.NodeID); entry != nil {
			p.emitProcess(entry, ProcessExited, request.ID)
		}
	}
	p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Value: value, HasValue: diagnostic.Code == "", Diagnostic: diagnostic})
}

func (p *Program) failServiceHost(entry *instance) {
	if entry == nil || entry.Node.State == core.NodeFailed || entry.Node.State == core.NodeCancelled || entry.Node.State == core.NodeComplete {
		return
	}
	entry.ServiceReadyDeadline, entry.ServiceNextProbe, entry.ServiceTimerID, entry.ServiceClockID = 0, 0, 0, 0
	p.Engine.Fail(entry.Node, failure(p.Alloc, "HOST_FAIL", "service host request failed"))
	p.drainCancellations()
}

func (p *Program) completeRequest(request host.Request) {
	completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID}
	name := host.PayloadPath(request.Payload)
	if request.Kind == host.RequestEnvironment {
		value, ok := p.configuredEnvironment(name)
		if ok {
			completion.Value, completion.HasValue = core.NewString(p.Alloc, value), true
		} else {
			completion.Value, completion.HasValue = core.Value{Kind: core.Nil}, true
		}
	} else if request.Kind == host.RequestReadFile {
		op := host.PayloadText(request.Payload, host.FieldOp)
		if op == "" {
			op = host.OpRead
		}
		completion = p.fileCompletion(request, op, name)
	} else if request.Kind == host.RequestWriteFile {
		filename := p.canonicalTarget(name, true)
		data := host.PayloadBytes(request.Payload, host.FieldData)
		ok := p.mkdirParent(filename) && p.Host.WriteFileAtomic(filename, data, 0o644, false) == nil
		if !ok {
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot write file")
		} else {
			completion.Value, completion.HasValue = core.Value{Kind: core.Nil}, true
		}
		mem.FreeString(p.Alloc, filename)
	} else if request.Kind == host.RequestWallTime {
		completion.Value, completion.HasValue = core.Value{Kind: core.Int, Int: p.Host.Now()}, true
	} else if request.Kind == host.RequestMonotonicTime {
		completion.Value, completion.HasValue = core.Value{Kind: core.Int, Int: p.Host.Monotonic()}, true
	} else {
		completion.Diagnostic = failure(p.Alloc, "HOST_FAIL", "unsupported host request")
	}
	p.Engine.Complete(completion)
}

func (p *Program) drainCancellations() {
	for {
		cancellation := p.Engine.NextCancellation()
		if cancellation.RequestID == 0 {
			return
		}
		probeID := int64(0)
		serviceGrace := int64(-1)
		for i := range p.Instances {
			entry := &p.Instances[i]
			if entry.Node.ID == cancellation.NodeID && entry.ServiceProbeID != 0 {
				probeID = entry.ServiceProbeID
				entry.ServiceProbeID = 0
			}
			if entry.Node.ID == cancellation.NodeID {
				if entry.Rule.Kind == rule.ServiceRule {
					serviceGrace = entry.Service.StopGrace
					p.setServiceState(entry, "stopping")
				}
				entry.ServiceReadyDeadline, entry.ServiceNextProbe = 0, 0
				entry.ServiceClockID, entry.ServiceTimerID = 0, 0
			}
		}
		if p.Forwarding {
			grace := serviceGrace
			if grace < 0 {
				grace = 5000
			}
			p.queueProcessCancellation(cancellation.NodeID, cancellation.Generation, cancellation.Attempt, cancellation.RequestID, grace)
			if probeID != 0 {
				p.queueProcessCancellation(cancellation.NodeID, cancellation.Generation, cancellation.Attempt, probeID, 0)
			}
		} else if p.Host != nil {
			if serviceGrace >= 0 {
				p.Host.Stop(cancellation.RequestID, serviceGrace)
			} else {
				p.Host.Cancel(cancellation.RequestID)
			}
			if probeID != 0 {
				p.Host.Cancel(probeID)
			}
		}
	}
}

func (p *Program) queueProcessCancellation(nodeID int64, generation int64, attempt int64, processID int64, graceMS int64) {
	p.nextRequest++
	id := p.nextRequest
	payload := host.ServiceCancelPayload(p.Alloc, processID, graceMS)
	p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: id, NodeID: nodeID, Generation: generation, Attempt: attempt, Kind: host.RequestProcessCancel, Payload: payload})
}

func (p *Program) complete(event host.ProcessEvent) {
	var d diagnostic.Diagnostic
	if event.Outcome == host.ProcessFailed {
		d = failure(p.Alloc, "HOST_FAIL", event.Diagnostic.Message)
	} else if event.Outcome == host.ProcessTimedOut {
		d = failure(p.Alloc, "RECIPE_TIMEOUT", "recipe timed out")
	} else if event.Outcome == host.ProcessCancelled {
		d = failure(p.Alloc, "EXEC_CANCELLED", "recipe cancelled")
	} else if event.Status != 0 {
		d = failure(p.Alloc, "RECIPE_FAIL", "recipe exited unsuccessfully")
		if entry := p.instanceForRequest(event.ID); entry != nil && len(entry.LineSpans) != 0 {
			d.Span = entry.LineSpans[0]
		}
	}
	entry := p.instanceForRequest(event.ID)
	if entry == nil {
		entry = p.serviceForProcessRequest(event.ID)
	}
	if entry != nil && entry.Rule.Kind == rule.ServiceRule && event.ID == entry.ServiceProcessID && entry.ServiceState == "stopping" {
		p.setServiceState(entry, "terminal")
	}
	if entry != nil && entry.Rule.Kind == rule.ServiceRule && !entry.ServiceReady && d.Code == "" {
		d = failure(p.Alloc, "SERVICE_START_FAILED", "service exited before becoming ready")
	}
	if entry != nil && entry.Rule.Kind == rule.ServiceRule && !entry.ServiceReady {
		entry.ServiceReadyDeadline, entry.ServiceNextProbe = 0, 0
	}
	if entry != nil && entry.Rule.Kind == rule.ServiceRule && event.ID == entry.Node.HostRequestID {
		if d.Code == "" {
			d = failure(p.Alloc, "SERVICE_EXITED", "service exited unexpectedly")
		}
		if p.prepareServiceRestart(entry) {
			d.Free(p.Alloc)
			p.Engine.Invalidate(entry.Node)
			p.drainCancellations()
			return
		}
		d.Free(p.Alloc)
		message, code := "service exited before becoming ready", "SERVICE_START_FAILED"
		if entry.ServiceReady {
			message, code = "service exited unexpectedly", "SERVICE_EXITED"
		}
		if entry.ServiceRestartCount > 0 {
			message, code = "service restart attempts were exhausted", "SERVICE_RESTART_EXHAUSTED"
		}
		d = failure(p.Alloc, code, message)
	}
	if d.Code != "" {
		p.attachProcessContext(&d, entry, event)
		p.releaseCacheLock(entry)
	}
	var completedPending pendingRequest
	hasPending := false
	for i := range p.Pending {
		pending := p.Pending[i]
		if pending.ID != event.ID {
			continue
		}
		completedPending, hasPending = pending, true
		freePendingRequest(p.Alloc, &p.Pending[i])
		copy(p.Pending[i:], p.Pending[i+1:])
		p.Pending = p.Pending[:len(p.Pending)-1]
		break
	}
	if hasPending && (completedPending.Capture || completedPending.Stream) {
		if completedPending.Capture && event.StdoutTruncated {
			d.Free(p.Alloc)
			d = failure(p.Alloc, "CAPTURE_LIMIT", "command substitution exceeded capture limit")
		}
		if d.Code == "RECIPE_FAIL" {
			d.Free(p.Alloc)
			d = diagnostic.Diagnostic{}
		}
		completion := core.Completion{NodeID: completedPending.NodeID, Generation: completedPending.Generation, Attempt: completedPending.Attempt, RequestID: event.ID, Diagnostic: d}
		if d.Code == "" {
			completion.Value, completion.HasValue = shellValue(p.Alloc, event), true
			completion.Value.Record = slices.Append(p.Alloc, completion.Value.Record, core.RecordField{Key: cloneText(p.Alloc, "signal"), Value: core.Value{Kind: core.Int, Int: int64(event.Signal)}})
		}
		p.Engine.Complete(completion)
		return
	}
	node := p.nodeForRequest(event.ID)
	if node != nil && entry != nil && node.HostRequestID == event.ID && node.State == core.NodeWaiting && d.Code != "" && d.Code != "EXEC_CANCELLED" && entry.retryCount < p.Options.RetryCount {
		p.nextRequest++
		retryID := p.nextRequest
		retain := p.Options.RetainBytes
		if p.Options.CacheRetainBytes > retain {
			retain = p.Options.CacheRetainBytes
		}
		request := host.ProcessRequest{ID: retryID, Shell: entry.Shell, Script: []byte(entry.Script), Directory: p.Options.Directory, Environment: entry.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}
		if p.Host != nil && p.Host.Start(request) {
			entry.retryCount++
			node.HostRequestID = retryID
			display := processDisplayFromHost(p.Alloc, request)
			p.Pending = slices.Append(p.Alloc, p.Pending, pendingRequest{ID: retryID, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, Program: display.Program, Argv: display.Argv, DisplayTruncated: display.Truncated})
			d.Free(p.Alloc)
			return
		}
	}
	if node != nil && entry != nil && node.HostRequestID == event.ID && node.State == core.NodeWaiting && entry.Rule.Kind == rule.CachedTaskRule && entry.CacheReady && d.Code == "" && event.Status == 0 && !p.Options.CacheDisabled {
		p.cacheCommit(entry, event.Stdout, event.Stderr, event.StdoutTruncated, event.StderrTruncated)
	}
	if node != nil {
		p.Engine.Complete(core.Completion{NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: event.ID, Diagnostic: d})
	} else {
		d.Free(p.Alloc)
	}
}

// attachProcessContext preserves the runtime diagnosis and carries only bounded
// process metadata to renderers. The request script and environment are
// deliberately never copied into a diagnostic.
func (p *Program) attachProcessContext(d *diagnostic.Diagnostic, entry *instance, event host.ProcessEvent) {
	if d == nil || d.Code == "" {
		return
	}
	if entry != nil {
		if d.Span.Start == 0 && d.Span.End == 0 && len(entry.LineSpans) != 0 {
			d.Span = entry.LineSpans[0]
		}
		if d.Source == "" && p.Parsed != nil && p.Parsed.Source != nil && (d.Span.Start != 0 || d.Span.End != 0) {
			p.locateDiagnostic(d, d.Span)
		}
		if d.Target == "" {
			d.Target = cloneText(p.Alloc, entry.Plan.Target)
		}
		if len(d.TargetStack) == 0 {
			d.TargetStack = p.targetStack(entry)
		}
	}
	cause := diagnostic.Cause{Kind: cloneText(p.Alloc, "process"), Program: cloneText(p.Alloc, processProgram(p)), StdoutTruncated: event.StdoutTruncated, StderrTruncated: event.StderrTruncated, StdoutLimit: event.RetainBytes, StderrLimit: event.RetainBytes, OutputWasStreamed: true}
	if entry != nil && len(entry.Shell) != 0 {
		mem.FreeString(p.Alloc, cause.Program)
		cause.Program = cloneText(p.Alloc, entry.Shell[0])
	}
	if event.Outcome == host.ProcessFailed {
		cause.Message = cloneText(p.Alloc, event.Diagnostic.Message)
	} else if event.Outcome == host.ProcessTimedOut {
		cause.Message = cloneText(p.Alloc, "process timed out")
	} else if event.Outcome == host.ProcessCancelled {
		cause.Message = cloneText(p.Alloc, "process cancelled")
	} else {
		cause.Message = cloneText(p.Alloc, "process exited")
		cause.Status, cause.HasStatus = event.Status, true
	}
	if event.Signal != 0 {
		cause.Signal, cause.HasSignal = event.Signal, true
	}
	d.Cause = cause
}

// targetStack follows reverse dynamic edges from the failed instance to its
// requested root. Dynamic edges are established in producer order, making a
// first matching parent deterministic when a target has more than one consumer.
// The bounded walk intentionally retains one repeated target when a cycle is
// the context that led to the failure.
func (p *Program) targetStack(entry *instance) []string {
	if p == nil || entry == nil {
		return nil
	}
	var reversed []string
	current := entry
	for steps := 0; current != nil && steps <= len(p.Instances); steps++ {
		reversed = slices.Append(p.Alloc, reversed, cloneText(p.Alloc, current.Plan.Target))
		current = p.parentInstance(current.Node, entry.runEpoch)
	}
	stack := slices.Make[string](p.Alloc, len(reversed))
	for i := range reversed {
		stack[len(reversed)-1-i] = reversed[i]
	}
	slices.Free(p.Alloc, reversed)
	return stack
}

func (p *Program) parentInstance(child *core.Node, epoch int64) *instance {
	if child == nil {
		return nil
	}
	for i := range p.Instances {
		candidate := &p.Instances[i]
		if candidate.Node == child || (epoch != 0 && candidate.runEpoch != 0 && candidate.runEpoch != epoch) {
			continue
		}
		if slices.Contains(candidate.Node.Dynamic, child) {
			return candidate
		}
	}
	return nil
}

func processProgram(p *Program) string {
	if p != nil && len(p.Options.Shell) != 0 {
		return p.Options.Shell[0]
	}
	return ""
}

func shellValue(a mem.Allocator, event host.ProcessEvent) core.Value {
	fields := []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: int64(event.Status)}}, {Key: "stdout", Value: core.NewBytes(a, event.Stdout)}, {Key: "stderr", Value: core.NewBytes(a, event.Stderr)}}
	value := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	if len(event.Stages) != 0 {
		var stages []core.Value
		for i := range event.Stages {
			stage := event.Stages[i]
			parts := []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: int64(stage.Status)}}, {Key: "signal", Value: core.Value{Kind: core.Int, Int: int64(stage.Signal)}}, {Key: "outcome", Value: core.Value{Kind: core.Int, Int: int64(stage.Outcome)}}}
			stages = slices.Append(a, stages, core.NewRecord(a, parts))
		}
		value.Record = slices.Append(a, value.Record, core.RecordField{Key: cloneText(a, "stages"), Value: core.NewList(a, stages)})
		for i := range stages {
			stages[i].Free(a)
		}
		slices.Free(a, stages)
	}
	return value
}

// Captured stdout is private to the expression; stderr remains a live stream.
func (p *Program) captureStderr(pending pendingRequest, data []byte) {
	if entry := p.streamInstance(pending.NodeID); entry != nil {
		p.recipeStream(entry, Stderr, data)
		return
	}
	if p.Eval.DefinitionEffectSink != nil {
		p.Eval.DefinitionEffectSink(p.Eval.DefinitionEffectState, eval.Effect{Kind: eval.EffectErr, Data: data})
	} else {
		p.emit(Event{Kind: Stderr, NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, RequestID: pending.ID, Data: slices.Clone(p.Alloc, data)})
	}
}

func (p *Program) statementStdout(pending pendingRequest, data []byte) {
	if entry := p.streamInstance(pending.NodeID); entry != nil {
		p.recipeStream(entry, Stdout, data)
		return
	}
	if p.Eval.DefinitionEffectSink != nil {
		p.Eval.DefinitionEffectSink(p.Eval.DefinitionEffectState, eval.Effect{Kind: eval.EffectOut, Data: data})
	} else {
		p.emit(Event{Kind: Stdout, NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, RequestID: pending.ID, Data: slices.Clone(p.Alloc, data)})
	}
}

func (p *Program) streamInstance(node int64) *instance {
	node = p.Eval.ProcessRecipeNode(node)
	for i := range p.Instances {
		if p.Instances[i].Node.ID == node && p.Instances[i].Kash {
			return &p.Instances[i]
		}
	}
	return nil
}

func (p *Program) recipeStream(entry *instance, kind EventKind, data []byte) {
	p.emitNode(entry.Node, entry.Plan.Target, kind, diagnostic.Span{}, data)
	if entry.Rule.Kind != rule.CachedTaskRule {
		return
	}
	if kind == Stdout {
		p.cacheAppend(&entry.CacheStdout, &entry.CacheStdoutTruncated, data, p.Options.CacheRetainBytes)
	} else {
		p.cacheAppend(&entry.CacheStderr, &entry.CacheStderrTruncated, data, p.Options.CacheRetainBytes)
	}
}

func (p *Program) instanceForRequest(id int64) *instance {
	for i := range p.Instances {
		if p.Instances[i].Node.HostRequestID == id && !p.Instances[i].KashRunning {
			return &p.Instances[i]
		}
	}
	return nil
}

func (p *Program) serviceForProcessRequest(id int64) *instance {
	for i := range p.Instances {
		if p.Instances[i].ServiceProcessID == id {
			return &p.Instances[i]
		}
	}
	return nil
}

func (p *Program) emitProcess(entry *instance, kind EventKind, requestID int64) {
	node := entry.Node
	event := Event{Kind: kind, Target: entry.Plan.Target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: requestID}
	for i := range p.Pending {
		pending := &p.Pending[i]
		if pending.ID != requestID { continue }
		event.Program, event.Argv, event.DisplayTruncated = pending.Program, pending.Argv, pending.DisplayTruncated
		if kind == ProcessStarted {
			pending.Started, pending.StartedNS = true, 0
			if p.Host != nil { pending.StartedNS = p.Host.Monotonic() }
		} else if kind == ProcessExited && pending.Started && p.Host != nil {
			finished := p.Host.Monotonic()
			if finished >= pending.StartedNS { event.RuntimeMS, event.HasRuntime = (finished-pending.StartedNS)/1000000, true }
		}
		break
	}
	p.emit(event)
}

func freePendingRequest(a mem.Allocator, pending *pendingRequest) {
	if pending == nil { return }
	mem.FreeString(a, pending.Program)
	freeStrings(a, pending.Argv)
	*pending = pendingRequest{}
}

type processDisplay struct { Program string; Argv []string; Truncated bool }

const maxProcessDisplayStages = 4
const maxProcessDisplayTokens = 32

func appendProcessDisplayToken(a mem.Allocator, raw *[]string, value string, truncated *bool) {
	if len(*raw) >= maxProcessDisplayTokens {
		*truncated = true
		return
	}
	*raw = slices.Append(a, *raw, value)
}

func processDisplayFromPayload(a mem.Allocator, payload core.Value, shell []string) processDisplay {
	var raw []string
	truncated := false
	argv := host.PayloadArgv(payload)
	if len(argv) != 0 {
		for i := range argv { appendProcessDisplayToken(a, &raw, argv[i].Text, &truncated) }
	} else {
		stages := host.PayloadStages(payload)
		if len(stages) != 0 {
			if len(stages) > maxProcessDisplayStages { truncated = true }
			stageCount := len(stages)
			if stageCount > maxProcessDisplayStages { stageCount = maxProcessDisplayStages }
			for i := 0; i < stageCount; i++ {
				if i != 0 { appendProcessDisplayToken(a, &raw, "|", &truncated) }
				for j := range stages[i].List { appendProcessDisplayToken(a, &raw, stages[i].List[j].Text, &truncated) }
			}
		} else {
			shellArgv := host.PayloadList(payload, "shell")
			if len(shellArgv) == 0 { for i := range shell { appendProcessDisplayToken(a, &raw, shell[i], &truncated) }
			} else { for i := range shellArgv { appendProcessDisplayToken(a, &raw, shellArgv[i].Text, &truncated) } }
			script := host.PayloadText(payload, host.FieldScript)
			if script != "" { appendProcessDisplayToken(a, &raw, script, &truncated) }
		}
	}
	display := boundedProcessDisplay(a, raw)
	display.Truncated = display.Truncated || truncated
	slices.Free(a, raw)
	return display
}

func processDisplayFromHost(a mem.Allocator, request host.ProcessRequest) processDisplay {
	var raw []string
	truncated := false
	if len(request.Argv) != 0 {
		for i := range request.Argv { appendProcessDisplayToken(a, &raw, request.Argv[i], &truncated) }
	} else if len(request.Stages) != 0 {
		if len(request.Stages) > maxProcessDisplayStages { truncated = true }
		stageCount := len(request.Stages)
		if stageCount > maxProcessDisplayStages { stageCount = maxProcessDisplayStages }
		for i := 0; i < stageCount; i++ {
			if i != 0 { appendProcessDisplayToken(a, &raw, "|", &truncated) }
			for j := range request.Stages[i].Argv { appendProcessDisplayToken(a, &raw, request.Stages[i].Argv[j], &truncated) }
		}
	} else {
		for i := range request.Shell { appendProcessDisplayToken(a, &raw, request.Shell[i], &truncated) }
		if len(request.Script) != 0 { appendProcessDisplayToken(a, &raw, string(request.Script), &truncated) }
	}
	display := boundedProcessDisplay(a, raw)
	display.Truncated = display.Truncated || truncated
	slices.Free(a, raw)
	return display
}

func boundedProcessDisplay(a mem.Allocator, raw []string) processDisplay {
	const maxBytes = 160
	const maxArgs = 8
	display := processDisplay{}
	if len(raw) == 0 { return display }
	program := raw[0]
	if len(program) > maxBytes { program = truncateProcessText(a, program, maxBytes); display.Truncated = true
	} else { program = cloneText(a, program) }
	display.Program = program
	used := len(program)
	for i := 1; i < len(raw); i++ {
		if len(display.Argv) == maxArgs { display.Truncated = true; break }
		remaining := maxBytes-used
		if remaining <= 0 { display.Truncated = true; break }
		value := raw[i]
		if len(value) > remaining { value = truncateProcessText(a, value, remaining); display.Truncated = true
		} else { value = cloneText(a, value) }
		display.Argv = slices.Append(a, display.Argv, value)
		used += len(value)
		if display.Truncated { break }
	}
	return display
}

func truncateProcessText(a mem.Allocator, value string, limit int) string {
	if limit <= 0 { return cloneText(a, "") }
	if limit <= 3 { return cloneText(a, "..."[:limit]) }
	end := limit-3
	if end > len(value) { end = len(value) }
	for end > 0 && end < len(value) && value[end]&0xc0 == 0x80 { end-- }
	return cloneText(a, value[:end]+"...")
}
