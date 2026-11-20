package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"unicode/utf8"
)

type commandOutput struct {
	data     []byte
	limit    int
	overflow bool
}

// append reports the transition to overflow so a producer is stopped only once.
func (o *commandOutput) append(data []byte) bool {
	if o.overflow {
		return false
	}
	if len(data) > o.limit-len(o.data) {
		o.overflow = true
		return true
	}
	o.data = append(o.data, data...)
	return false
}

func (p *Program) registerCommandOutput(request int64, output *commandOutput) {
	if p.commandOutputs == nil {
		p.commandOutputs = make(map[int64]*commandOutput)
	}
	p.commandOutputs[request] = output
}

func (p *Program) CaptureCommandOutput(request int64, data []byte) (consumed, overflow bool) {
	if output := p.commandOutputs[request]; output != nil {
		return true, output.append(data)
	}
	return false, false
}

func (p *Program) ReleaseCommandOutput(request int64) { delete(p.commandOutputs, request) }

type functionCommandCapture struct {
	output  commandOutput
	effects *kashControlState
	result  Result
	done    bool
}

func freeFunctionCommandCapture(a mem.Allocator, value any) {
	state := value.(*functionCommandCapture)
	state.result.Free(a)
	freeKashControlState(a, state.effects)
}

func (p *Program) captureFunctionCommand(function *Function, graph, stage *expr.Expr, c *Context) Result {
	start, end, path := c.operationStart, c.operationEnd, c.CallPath
	c.operationStart, c.operationEnd, c.CallPath = graph.Span.Start, graph.Span.End, path+"/function-command"
	state, _ := c.OperationState().(*functionCommandCapture)
	if state == nil {
		limit := p.CaptureLimit
		if limit <= 0 {
			limit = DefaultCaptureLimit
		}
		if c.captureBudget > 0 && graph.Span.Start == c.captureBudgetStart && graph.Span.End == c.captureBudgetEnd {
			limit = min(limit, c.captureBudget)
		}
		state = &functionCommandCapture{output: commandOutput{limit: limit}, effects: mem.Alloc[kashControlState](p.Alloc)}
		c.SetOperationState(state, freeFunctionCommandCapture)
	}
	c.operationStart, c.operationEnd, c.CallPath = start, end, path
	if state.done {
		replayControlEffects(state.effects, c)
		return cloneFinalizerResult(c.Run, &state.result)
	}
	before, beforePaths := len(c.Effects), len(c.WritePaths)
	scope, output := c.Scope, c.commandOutput
	capture := graph.Kind == expr.CommandCapture
	if capture {
		c.commandOutput = &state.output
	}
	result := p.call(function, commandArguments(stage), c, graph.Span)
	c.Scope = scope
	if capture && !result.Waiting {
		p.streamControlOutput(c, before)
	}
	c.commandOutput = output
	if result.Waiting || result.Completed {
		return result
	}
	if state.output.overflow {
		result.Free(c.Run)
		result = failure(c.Run, "CAPTURE_LIMIT", graph.Span, "command substitution exceeded capture limit")
	} else if capture && result.Diagnostic.Exit.Requested {
		if utf8.Valid(state.output.data) {
			freeValueWithCallables(c.Run, &result.Value)
			result.Value = core.NewString(c.Run, string(state.output.data))
		} else {
			result.Free(c.Run)
			result = failure(c.Run, "CAPTURE_ENCODING", graph.Span, "command substitution output is not valid UTF-8")
		}
	} else {
		result = p.finishFunctionCommand(graph, c, state, result, capture)
	}
	p.retainControlEffects(state.effects, c, scope, before, beforePaths)
	state.result, state.done = cloneFinalizerResult(p.Alloc, &result), true
	return result
}

func (p *Program) finishFunctionCommand(graph *expr.Expr, c *Context, state *functionCommandCapture, result Result, capture bool) Result {
	ownTask := c.asyncHandle != nil && c.asyncHandle.task != nil && c.asyncHandle.task.Graph == graph
	outcome := graph.ActionPrefix == "$?("
	accepted := graph.AcceptExit && eligibleCommandExit(result.Diagnostic) && !result.Diagnostic.Cause.HasSignal
	listOperand := c.captureListOutcome != nil && c.captureListOutcome.graph == graph
	if outcome || ownTask || listOperand {
		if !functionOutcomeAllowed(result.Diagnostic) {
			return result
		}
		record := functionCommandOutcome(c, graph, result, capture, accepted)
		if listOperand && (!capture || utf8.Valid(state.output.data)) {
			c.recordListOperand(graph, record.Value, true)
		}
		if outcome {
			result.Free(c.Run)
			return record
		}
		if ownTask {
			handle := c.asyncHandle
			handle.setOutcome(record.Value, true)
		}
		record.Free(c.Run)
	}
	freeCallables(c.Run, &result.Value)
	result.Value.Free(c.Run)
	if accepted {
		result.Diagnostic.Free(c.Run)
	}
	if capture {
		if utf8.Valid(state.output.data) {
			result.Value = core.NewString(c.Run, string(state.output.data))
		} else if !ownTask {
			result.Free(c.Run)
			return failure(c.Run, "CAPTURE_ENCODING", graph.Span, "command substitution output is not valid UTF-8")
		}
	}
	return result
}

func functionOutcomeAllowed(d diagnostic.Diagnostic) bool {
	if d.Protected || d.Cancelled || d.Exit.Requested {
		return false
	}
	switch d.Code {
	case "PARSE_ERR", "PHASE_INVALID", "CAP_DENIED", "EXEC_CANCELLED", "CAPTURE_LIMIT", "FINALIZE_TIMEOUT":
		return false
	default:
		return true
	}
}

func functionCommandOutcome(c *Context, graph *expr.Expr, body Result, capture, accepted bool) Result {
	status := int64(0)
	if d := body.Diagnostic; d.Code != "" {
		status = 1
		if d.Cause.HasStatus {
			status = int64(d.Cause.Status)
		}
	}
	completion := core.Value{Kind: core.Record, Record: []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: status}}}}
	action := *graph
	action.AcceptExit = accepted
	r := captureOutcome(c, &action, completion, body.Value)
	for i := range r.Value.Record {
		field := &r.Value.Record[i]
		switch field.Key {
		case "stdoutCaptured":
			field.Value.Bool = capture
		case "error":
			if body.Diagnostic.Code != "" && !accepted {
				field.Value.Free(c.Run)
				field.Value = outcomeErrorRecord(c.Run, body.Diagnostic)
			}
		}
	}
	return r
}

func outcomeErrorRecord(a mem.Allocator, d diagnostic.Diagnostic) core.Value {
	return core.NewRecord(a, []core.RecordField{
		{Key: "code", Value: core.Value{Kind: core.String, Text: d.Code}},
		{Key: "message", Value: core.Value{Kind: core.String, Text: d.Message}},
		{Key: "source", Value: core.Value{Kind: core.String, Text: d.Source}},
		{Key: "start", Value: core.Value{Kind: core.Int, Int: int64(d.Span.Start)}},
		{Key: "end", Value: core.Value{Kind: core.Int, Int: int64(d.Span.End)}},
	})
}
