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
		entry := p.instanceForRequest(event.ID)
		if event.Kind == host.ProcessStderr || event.Kind == host.ProcessStdout {
			for i := range p.Pending {
				if p.Pending[i].ID == event.ID && (p.Pending[i].Capture || p.Pending[i].Stream) {
					if event.Kind == host.ProcessStderr { p.captureStderr(p.Pending[i], event.Data) } else if p.Pending[i].Stream { p.statementStdout(p.Pending[i], event.Data) }
					break
				}
			}
		}
		if entry != nil && event.Kind == host.ProcessStdout {
			p.emitNode(entry.Node, entry.Plan.Target, Stdout, diagnostic.Span{}, event.Data)
		} else if entry != nil && event.Kind == host.ProcessStderr {
			p.emitNode(entry.Node, entry.Plan.Target, Stderr, diagnostic.Span{}, event.Data)
		} else if entry != nil && event.Kind == host.ProcessStarted {
			p.emitNode(entry.Node, entry.Plan.Target, ProcessStarted, diagnostic.Span{}, nil)
		} else if event.Kind == host.ProcessTerminal {
			if entry != nil {
				p.emitNode(entry.Node, entry.Plan.Target, ProcessExited, diagnostic.Span{}, nil)
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
			// Ownership transfers to the outbound queue; the embedding host
			// frees the request when it completes it.
			p.Outbound = slices.Append(p.Alloc, p.Outbound, request)
			continue
		}
		if request.Kind == host.RequestProcess {
			script := host.PayloadText(request.Payload, host.FieldScript)
			values := host.PayloadArgv(request.Payload)
			var argv []string
			for i := range values { argv = slices.Append(p.Alloc, argv, values[i].Text) }
			var stages []host.ProcessStage
			graphs := host.PayloadStages(request.Payload)
			setups := host.PayloadSetups(request.Payload)
			for i := range graphs {
				var arguments []string
				for j := range graphs[i].List { arguments = slices.Append(p.Alloc, arguments, graphs[i].List[j].Text) }
				stage := host.ProcessStage{Argv: arguments}
				if i < len(setups) {
					stage.Directory, stage.TimeoutMS = host.PayloadText(setups[i], host.FieldCwd), host.PayloadInt(setups[i], host.FieldTimeout)
					values := host.PayloadList(setups[i], host.FieldEnvironment)
					for j := range values { stage.Environment = slices.Append(p.Alloc, stage.Environment, values[j].Text) }
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
			for i := range request.Payload.Record { if request.Payload.Record[i].Key == "stream" { stream = request.Payload.Record[i].Value.Bool } }
			capture = capture && !stream
			if capture { retain = p.Options.CaptureLimit }
			if stream { retain = 0 }
			if (script == "" && !capture && !stream) || p.Host == nil || !p.Host.Start(host.ProcessRequest{ID: request.ID, Shell: p.Options.Shell, Argv: argv, Stages: stages, Input: host.PayloadText(request.Payload, host.FieldInput), Output: host.PayloadText(request.Payload, host.FieldOutput), Append: host.PayloadAppend(request.Payload), Script: []byte(script), Directory: p.Options.Directory, Environment: p.Options.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}) {
				p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Diagnostic: failure(p.Alloc, "HOST_FAIL", "cannot start shell request")})
			} else {
				p.Pending = slices.Append(p.Alloc, p.Pending, pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Retries: 0, Capture: capture, Stream: stream})
			}
			slices.Free(p.Alloc, argv)
			for i := range stages { slices.Free(p.Alloc, stages[i].Argv); slices.Free(p.Alloc, stages[i].Environment) }
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
	entry := p.instanceForRequest(request.ID)
	if entry == nil {
		return
	}
	p.emitNode(entry.Node, entry.Plan.Target, ProcessStarted, diagnostic.Span{}, nil)
}

func (p *Program) ProcessStream(request host.Request, stderr bool, data []byte) {
	for i := range request.Payload.Record {
		if request.Payload.Record[i].Key == "stream" && request.Payload.Record[i].Value.Bool {
			pending := pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Stream: true}
			if stderr { p.captureStderr(pending, data) } else { p.statementStdout(pending, data) }
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
	isInstance := p.nodeForRequest(request.ID) != nil
	retain := p.Options.RetainBytes
	if isInstance {
		// Recipes retain only what the native host would: nothing for a file
		// rule, the cache bound for a cached task.
		entry := p.instanceForRequest(request.ID)
		if entry != nil && entry.Rule.Kind == rule.CachedTaskRule && p.Options.CacheRetainBytes > retain {
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
	entry := p.instanceForRequest(request.ID)
	if entry != nil {
		p.emitNode(entry.Node, entry.Plan.Target, ProcessExited, diagnostic.Span{}, nil)
	}
	isInstance := p.nodeForRequest(request.ID) != nil
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
	p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Value: value, HasValue: diagnostic.Code == "", Diagnostic: diagnostic})
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
		if p.Host != nil {
			p.Host.Cancel(cancellation.RequestID)
		}
	}
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
	if d.Code != "" {
		p.attachProcessContext(&d, entry, event)
	}
	for i := range p.Pending {
		pending := p.Pending[i]
		if pending.ID != event.ID {
			continue
		}
		copy(p.Pending[i:], p.Pending[i+1:])
		p.Pending = p.Pending[:len(p.Pending)-1]
		if pending.Capture && event.StdoutTruncated {
			d.Free(p.Alloc)
			d = failure(p.Alloc, "CAPTURE_LIMIT", "command substitution exceeded capture limit")
		}
		if d.Code == "RECIPE_FAIL" {
			d.Free(p.Alloc)
			d = diagnostic.Diagnostic{}
		}
		completion := core.Completion{NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, RequestID: event.ID, Diagnostic: d}
		if d.Code == "" {
			completion.Value, completion.HasValue = shellValue(p.Alloc, event), true
			if pending.Capture || pending.Stream { completion.Value.Record = slices.Append(p.Alloc, completion.Value.Record, core.RecordField{Key: cloneText(p.Alloc, "signal"), Value: core.Value{Kind: core.Int, Int: int64(event.Signal)}}) }
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
		request := host.ProcessRequest{ID: retryID, Shell: p.Options.Shell, Script: []byte(entry.Script), Directory: p.Options.Directory, Environment: p.Options.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}
		if p.Host != nil && p.Host.Start(request) {
			entry.retryCount++
			node.HostRequestID = retryID
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
		for i := range stages { stages[i].Free(a) }
		slices.Free(a, stages)
	}
	return value
}

// Captured stdout is private to the expression; stderr remains a live stream.
func (p *Program) captureStderr(pending pendingRequest, data []byte) {
	if p.Eval.DefinitionEffectSink != nil {
		p.Eval.DefinitionEffectSink(p.Eval.DefinitionEffectState, eval.Effect{Kind: eval.EffectErr, Data: data})
	} else {
		p.emit(Event{Kind: Stderr, NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, RequestID: pending.ID, Data: slices.Clone(p.Alloc, data)})
	}
}

func (p *Program) statementStdout(pending pendingRequest, data []byte) {
	if p.Eval.DefinitionEffectSink != nil { p.Eval.DefinitionEffectSink(p.Eval.DefinitionEffectState, eval.Effect{Kind: eval.EffectOut, Data: data}) } else { p.emit(Event{Kind: Stdout, NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, RequestID: pending.ID, Data: slices.Clone(p.Alloc, data)}) }
}

func (p *Program) instanceForRequest(id int64) *instance {
	for i := range p.Instances {
		if p.Instances[i].Node.HostRequestID == id {
			return &p.Instances[i]
		}
	}
	return nil
}
