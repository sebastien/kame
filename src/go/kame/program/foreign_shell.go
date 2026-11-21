package program

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"strings"
)

func (p *Program) dispatchForeignShell(request host.Request) {
	finish := func(code, message string) {
		p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt,
			RequestID: request.ID, Diagnostic: failure(p.Alloc, code, message)})
	}
	endpoint, ok := p.Host.(host.ForeignShellHost)
	if !ok || p.Forwarding {
		finish("FEATURE_UNSUP", "host does not support foreign shells")
		return
	}
	if p.BackgroundNode != nil && p.BackgroundNode(request.NodeID) {
		finish("FEATURE_UNSUP", "foreign shell calls cannot run in a background job")
		return
	}
	list := func(key string) []string {
		values := host.PayloadList(request.Payload, key)
		result := make([]string, len(values))
		for i := range values {
			result[i] = strings.Clone(values[i].Text)
		}
		return result
	}
	call := host.ForeignShellCall{
		Worker: host.PayloadInt(request.Payload, "worker"), Directory: strings.Clone(host.PayloadText(request.Payload, host.FieldCwd)),
		Adapter: strings.Clone(host.PayloadText(request.Payload, "shell")),
		Command: strings.Clone(host.PayloadText(request.Payload, "command")), Environment: list(host.FieldEnvironment),
		Set: list("set"), Unset: list("unset"), Args: list("args"), TimeoutMS: p.Options.TimeoutMS,
	}
	if call.Worker == 0 {
		if p.Options.ResolveTool == nil {
			finish("FEATURE_UNSUP", "host cannot resolve the foreign shell executable")
			return
		}
		resolved := p.Options.ResolveTool(p.Alloc, p.toolRequestName(call.Adapter), call.Directory, call.Environment)
		call.Executable = strings.Clone(resolved)
		mem.FreeString(p.Alloc, resolved)
		if call.Executable == "" {
			finish("TOOL_MISSING", "cannot resolve "+call.Adapter)
			return
		}
	}
	if !endpoint.StartForeignShell(request.ID, call) {
		finish("HOST_FAIL", "foreign shell worker is unavailable")
		return
	}
	p.Pending = append(p.Pending, pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation,
		Attempt: request.Attempt, Stream: true, ForeignShell: true})
}

func (p *Program) completeForeignShell(pending pendingRequest, event host.ProcessEvent) {
	result := core.Completion{NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, RequestID: pending.ID}
	switch event.Outcome {
	case host.ProcessExited:
		list := func(items []string) core.Value {
			values := make([]core.Value, 0, len(items))
			for _, item := range items {
				values = append(values, core.NewString(p.Alloc, item))
			}
			return core.Value{Kind: core.List, List: values}
		}
		reply := event.ForeignShell
		if reply == nil {
			result.Diagnostic = failure(p.Alloc, "HOST_FAIL", "foreign shell reply was missing")
			break
		}
		result.Value = core.Value{Kind: core.Record, Record: []core.RecordField{
			{Key: cloneText(p.Alloc, "status"), Value: core.Value{Kind: core.Int, Int: int64(event.Status)}},
			{Key: cloneText(p.Alloc, "before"), Value: list(reply.Before)},
			{Key: cloneText(p.Alloc, "after"), Value: list(reply.After)},
			{Key: cloneText(p.Alloc, "functions"), Value: list(reply.Functions)},
		}}
		result.HasValue = true
	case host.ProcessCancelled:
		result.Diagnostic = failure(p.Alloc, "EXEC_CANCELLED", "foreign shell exchange cancelled")
	case host.ProcessTimedOut:
		result.Diagnostic = failure(p.Alloc, "RECIPE_TIMEOUT", "foreign shell exchange timed out")
	default:
		result.Diagnostic = failure(p.Alloc, "HOST_FAIL", "foreign shell exchange failed")
	}
	p.Engine.Complete(result)
}
