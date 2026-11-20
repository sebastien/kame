package eval

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/expr"
	"strings"
)

func (p *Program) killCommand(graph, stage *expr.Expr, c *Context) Result {
	if len(graph.Items) != 1 || graph.Topology || graph.Async || len(graph.Body) != 0 || c.PrepareDeferred != nil || !commandOnlyWords(stage) {
		return failure(c.Run, "FEATURE_UNSUP", stage.Span, "this kill form requires callable process stages")
	}
	if c.Engine == nil || c.Requests == nil {
		return failure(c.Run, "HOST_FAIL", stage.Span, "kill requires an execution host")
	}
	start, end, path := c.operationStart, c.operationEnd, c.CallPath
	c.operationStart, c.operationEnd, c.CallPath = graph.Span.Start, graph.Span.End, path+"/kill"
	defer func() {
		c.operationStart, c.operationEnd, c.CallPath = start, end, path
	}()
	if state, _ := c.OperationState().(*kashControlState); state != nil {
		replayControlEffects(state, c)
	} else {
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		words := stage.Items[1:]
		operands := p.argumentValues(c.Scope, words, c, stage.Span)
		if operands.Result.Waiting || operands.Result.Completed || operands.Result.Diagnostic.Code != "" {
			return operands.Result
		}
		defer freeValuesWithCallables(c.Run, operands.Values)
		values, signal := operands.Values, ""
		if len(values) != 0 && values[0].Kind == core.String && strings.HasPrefix(values[0].Text, "-") {
			signal = host.ProcessSignalName(values[0].Text[1:])
			if signal == "" {
				return failure(c.Run, "EXPR_INVALID", words[0].Span, "kill: unknown signal: "+values[0].Text)
			}
			values, words = values[1:], words[1:]
		}
		if len(values) == 0 {
			return failure(c.Run, "EXPR_INVALID", stage.Span, "kill requires an owned process handle")
		}
		handles, r := p.processOperands("kill", values, words, c)
		if r.Diagnostic.Code != "" {
			return r
		}
		p.killProcesses(c, handles, signal)
		state := mem.Alloc[kashControlState](p.Alloc)
		p.retainControlEffects(state, c, c.Scope, before, beforePaths)
		c.SetOperationState(state, freeKashControlState)
	}
	if graph.ActionPrefix == "$?(" {
		return captureOutcome(c, graph, core.Value{Kind: core.Record}, core.Value{Kind: core.String})
	}
	if graph.Kind == expr.CommandCapture {
		return Result{Value: core.NewString(c.Run, "")}
	}
	return Result{}
}
