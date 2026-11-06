package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type commandListState struct {
	Index     int
	Ready     bool
	Done      bool
	HasResult bool
	Value     core.Value
	Failure   diagnostic.Diagnostic
	Output    []byte
	Control   *kashControlState
}

func freeCommandListState(a mem.Allocator, value any) {
	state := value.(*commandListState)
	state.Value.Free(a)
	state.Failure.Free(a)
	slices.Free(a, state.Output)
	freeKashControlState(a, state.Control)
	mem.Free(a, state)
}

func (p *Program) commandList(e *expr.Expr, c *Context) Result {
	limit := p.CaptureLimit
	if limit <= 0 {
		limit = DefaultCaptureLimit
	}
	previousStart, previousEnd := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	var state *commandListState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*commandListState)
	}
	if state == nil {
		state = mem.Alloc[commandListState](p.Alloc)
		state.Control = mem.Alloc[kashControlState](p.Alloc)
		c.SetOperationState(state, freeCommandListState)
	}
	c.operationStart, c.operationEnd = previousStart, previousEnd
	replayControlEffects(state.Control, c)
	if state.Done {
		return commandListResult(state, e, c.Run)
	}
	for state.Index < len(e.Items) {
		if !state.Ready && state.Index != 0 {
			operator := e.CommandOps[state.Index-1].Text
			if state.Failure.Code != "" {
				if !eligibleCommandExit(state.Failure) {
					state.Done = true
					return commandListResult(state, e, c.Run)
				}
				if operator == "||" {
					state.Failure.Free(p.Alloc)
					state.Failure = diagnostic.Diagnostic{}
					state.Value.Free(p.Alloc)
					state.Value = core.Value{}
					state.HasResult = false
				} else {
					state.Index++
					continue
				}
			} else if operator == "||" {
				state.Index++
				continue
			}
			state.Ready = true
		}
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		previousBudget, previousStart, previousEnd := c.captureBudget, c.captureBudgetStart, c.captureBudgetEnd
		if e.CommandListCapture && e.Items[state.Index].Kind == expr.CommandCapture {
			remaining := limit - len(state.Output)
			if remaining < 1 {
				remaining = 1
			}
			c.captureBudget = remaining
			c.captureBudgetStart, c.captureBudgetEnd = e.Items[state.Index].Span.Start, e.Items[state.Index].Span.End
		}
		r := p.evaluate(c.Engine, c.Scope, e.Items[state.Index], c)
		c.captureBudget, c.captureBudgetStart, c.captureBudgetEnd = previousBudget, previousStart, previousEnd
		if r.Waiting {
			return r
		}
		if e.CommandListCapture && r.Value.Kind == core.String {
			if len(r.Value.Text) > limit-len(state.Output) {
				r.Free(c.Run)
				state.Failure.Free(p.Alloc)
				state.Failure = failure(p.Alloc, "CAPTURE_LIMIT", e.Span, "command substitution exceeded capture limit").Diagnostic
				state.Value.Free(p.Alloc)
				state.Value = core.Value{}
				state.Done = true
				return commandListResult(state, e, c.Run)
			}
			appendCommandListOutput(state, p.Alloc, r.Value.Text)
		}
		state.Value.Free(p.Alloc)
		state.Value = r.Value.Clone(p.Alloc)
		state.HasResult = true
		state.Failure.Free(p.Alloc)
		state.Failure = r.Diagnostic.Clone(p.Alloc)
		p.retainControlEffects(state.Control, c, c.Scope, before, beforePaths)
		r.Free(c.Run)
		state.Index++
		state.Ready = false
	}
	state.Done = true
	return commandListResult(state, e, c.Run)
}

func appendCommandListOutput(state *commandListState, a mem.Allocator, text string) {
	if text == "" {
		return
	}
	output := mem.AllocSlice[byte](a, len(state.Output)+len(text), len(state.Output)+len(text))
	copy(output, state.Output)
	copy(output[len(state.Output):], []byte(text))
	mem.FreeSlice(a, state.Output)
	state.Output = output
}

func eligibleCommandExit(d diagnostic.Diagnostic) bool {
	return !d.Protected && d.Code == "RECIPE_FAIL" && d.Cause.Kind == "process" && d.Cause.HasStatus
}

func commandListResult(state *commandListState, e *expr.Expr, a mem.Allocator) Result {
	if e.CommandCondition {
		if state.Failure.Code == "" {
			return Result{Value: core.Value{Kind: core.Bool, Bool: true}}
		}
		if eligibleCommandExit(state.Failure) {
			return Result{Value: core.Value{Kind: core.Bool, Bool: false}}
		}
	}
	result := Result{Diagnostic: state.Failure.Clone(a)}
	if e.CommandListCapture {
		result.Value = core.NewString(a, string(state.Output))
	} else if state.HasResult {
		result.Value = state.Value.Clone(a)
	}
	return result
}
