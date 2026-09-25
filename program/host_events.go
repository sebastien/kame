package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host"
	"littlemake/host/posix"
	"littlemake/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/os"
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
		if entry != nil && event.Kind == posix.Stdout {
			p.emitNode(entry.Node, entry.Plan.Target, Stdout, diagnostic.Span{}, event.Data)
		} else if entry != nil && event.Kind == posix.Stderr {
			p.emitNode(entry.Node, entry.Plan.Target, Stderr, diagnostic.Span{}, event.Data)
		} else if entry != nil && event.Kind == posix.Started {
			p.emitNode(entry.Node, entry.Plan.Target, ProcessStarted, diagnostic.Span{}, nil)
		} else if event.Kind == posix.Terminal {
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
		if request.Kind == host.RequestProcess {
			script := host.PayloadText(request.Payload, host.FieldScript)
			// The shell operation returns captured output, so it needs a
			// retained-byte budget even when no --log-limit was given.
			retain := p.Options.RetainBytes
			if retain < cacheLogDefault {
				retain = cacheLogDefault
			}
			if script == "" || p.Host == nil || !p.Host.Start(posix.Request{ID: request.ID, Shell: p.Options.Shell, Script: []byte(script), Directory: p.Options.Directory, Environment: p.Options.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}) {
				p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Diagnostic: failure(p.Alloc, "HOST_FAIL", "cannot start shell request")})
			} else {
				p.Pending = slices.Append(p.Alloc, p.Pending, pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Retries: 0})
			}
		} else {
			p.completeRequest(request)
		}
		request.Free(p.Alloc)
	}
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
		temporary := filename + ".littlemake-write.tmp"
		ok := mkdirParent(filename) && os.WriteFile(temporary, data, 0o644) == nil && os.Rename(temporary, filename) == nil
		if !ok {
			os.Remove(temporary)
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot write file")
		} else {
			completion.Value, completion.HasValue = core.Value{Kind: core.Nil}, true
		}
		mem.FreeString(p.Alloc, filename)
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

func (p *Program) complete(event posix.Event) {
	var d diagnostic.Diagnostic
	if event.Outcome == posix.Failed {
		d = failure(p.Alloc, "HOST_FAIL", event.Diagnostic.Message)
	} else if event.Outcome == posix.TimedOut {
		d = failure(p.Alloc, "RECIPE_TIMEOUT", "recipe timed out")
	} else if event.Outcome == posix.Cancelled {
		d = failure(p.Alloc, "EXEC_CANCELLED", "recipe cancelled")
	} else if event.Status != 0 {
		d = failure(p.Alloc, "RECIPE_FAIL", "recipe exited unsuccessfully")
		if entry := p.instanceForRequest(event.ID); entry != nil && len(entry.LineSpans) != 0 {
			d.Span = entry.LineSpans[0]
		}
	}
	for i := range p.Pending {
		pending := p.Pending[i]
		if pending.ID != event.ID {
			continue
		}
		copy(p.Pending[i:], p.Pending[i+1:])
		p.Pending = p.Pending[:len(p.Pending)-1]
		if d.Code == "RECIPE_FAIL" {
			d.Free(p.Alloc)
			d = diagnostic.Diagnostic{}
		}
		completion := core.Completion{NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, RequestID: event.ID, Diagnostic: d}
		if d.Code == "" {
			completion.Value, completion.HasValue = shellValue(p.Alloc, event), true
		}
		p.Engine.Complete(completion)
		return
	}
	node := p.nodeForRequest(event.ID)
	entry := p.instanceForRequest(event.ID)
	if node != nil && entry != nil && node.HostRequestID == event.ID && node.State == core.NodeWaiting && d.Code != "" && d.Code != "EXEC_CANCELLED" && entry.retryCount < p.Options.RetryCount {
		p.nextRequest++
		retryID := p.nextRequest
		retain := p.Options.RetainBytes
		if p.Options.CacheRetainBytes > retain {
			retain = p.Options.CacheRetainBytes
		}
		request := posix.Request{ID: retryID, Shell: p.Options.Shell, Script: []byte(entry.Script), Directory: p.Options.Directory, Environment: p.Options.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}
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
