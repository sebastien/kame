package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
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
	Outcome   core.Value
	Function  bool
}

type commandListOperand struct {
	graph    *expr.Expr
	outcome  core.Value
	function bool
}

func (c *Context) recordListOperand(graph *expr.Expr, outcome core.Value, function bool) {
	if operand := c.captureListOutcome; operand != nil && operand.graph == graph {
		freeValueWithCallables(c.Run, &operand.outcome)
		operand.outcome, operand.function = borrowValue(c.Run, outcome), function
	}
}

func (c *Context) redirectListOperand(from, to *expr.Expr) func() {
	operand := c.captureListOutcome
	if operand == nil || operand.graph != from {
		return func() {}
	}
	operand.graph = to
	return func() { operand.graph = from }
}

func freeCommandListState(a mem.Allocator, value any) {
	state := value.(*commandListState)
	freeValueWithCallables(a, &state.Value)
	state.Failure.Free(a)
	freeValueWithCallables(a, &state.Outcome)
	mem.FreeSlice(nil, state.Output)
	freeKashControlState(a, state.Control)
	mem.Free(a, state)
}

func (p *Program) commandList(e *expr.Expr, c *Context) Result {
	if c.PurePredicate || c.Phase != EvaluatePhase {
		return failure(c.Run, "PHASE_INVALID", e.Span, "command lists require an action phase")
	}
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
	ownTask := c.asyncHandle != nil && c.asyncHandle.task != nil && c.asyncHandle.task.Graph == e
	inspect := e.ActionPrefix == "$?(" || ownTask
	finish := func() Result {
		result := commandListResult(state, e, c.Run)
		if !inspect {
			return result
		}
		if result.Diagnostic.Exit.Requested {
			if e.ActionPrefix == "$?(" && state.Outcome.Kind == core.Record && inspectCaptureExit(result.Diagnostic) {
				result.Value.Free(c.Run)
				result.Value = commandListOutcome(state, e, c).Value
			}
			return result
		}
		outcome := commandListOutcome(state, e, c)
		if e.ActionPrefix == "$?(" {
			result.Free(c.Run)
			return outcome
		}
		if outcome.Diagnostic.Code == "" {
			handle := c.asyncHandle
			handle.setOutcome(outcome.Value, true)
		}
		outcome.Free(c.Run)
		return result
	}
	if state.Done {
		return finish()
	}
	for state.Index < len(e.Items) {
		if !state.Ready && state.Index != 0 {
			operator := e.CommandOps[state.Index-1].Text
			if state.Failure.Code != "" {
				if !eligibleCommandExit(state.Failure) {
					state.Done = true
					return finish()
				}
				if operator == "||" {
					state.Failure.Free(p.Alloc)
					state.Failure = diagnostic.Diagnostic{}
					freeValueWithCallables(p.Alloc, &state.Value)
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
		previousMember := c.captureListMember
		previousOutcome := c.captureListOutcome
		operand := commandListOperand{graph: e.Items[state.Index]}
		if inspect {
			c.captureListOutcome = &operand
		}
		previousBudget, previousStart, previousEnd := c.captureBudget, c.captureBudgetStart, c.captureBudgetEnd
		if e.CommandListCapture && e.Items[state.Index].Kind == expr.CommandCapture {
			c.captureListMember = e.Items[state.Index]
			remaining := limit - len(state.Output)
			if remaining < 1 {
				remaining = 1
			}
			c.captureBudget = remaining
			c.captureBudgetStart, c.captureBudgetEnd = e.Items[state.Index].Span.Start, e.Items[state.Index].Span.End
		}
		r := p.evaluate(c.Engine, c.Scope, e.Items[state.Index], c)
		c.captureListMember = previousMember
		c.captureListOutcome = previousOutcome
		c.captureBudget, c.captureBudgetStart, c.captureBudgetEnd = previousBudget, previousStart, previousEnd
		if r.Waiting || r.Completed {
			freeValueWithCallables(c.Run, &operand.outcome)
			return r
		}
		if e.CommandListCapture && r.Value.Kind == core.String {
			if len(r.Value.Text) > limit-len(state.Output) {
				r.Free(c.Run)
				freeValueWithCallables(c.Run, &operand.outcome)
				state.Failure.Free(p.Alloc)
				state.Failure = failure(p.Alloc, "CAPTURE_LIMIT", e.Span, "command substitution exceeded capture limit").Diagnostic
				freeValueWithCallables(p.Alloc, &state.Value)
				freeValueWithCallables(p.Alloc, &state.Outcome)
				state.Done = true
				return finish()
			}
			appendCommandListOutput(state, p.Alloc, r.Value.Text)
		}
		freeValueWithCallables(p.Alloc, &state.Value)
		state.Value = borrowValue(p.Alloc, r.Value)
		state.HasResult = true
		state.Failure.Free(p.Alloc)
		state.Failure = r.Diagnostic.Clone(p.Alloc)
		freeValueWithCallables(p.Alloc, &state.Outcome)
		state.Outcome, state.Function = borrowValue(p.Alloc, operand.outcome), operand.function
		freeValueWithCallables(c.Run, &operand.outcome)
		p.retainControlEffects(state.Control, c, c.Scope, before, beforePaths)
		r.Free(c.Run)
		state.Index++
		state.Ready = false
	}
	state.Done = true
	return finish()
}

func commandListOutcome(state *commandListState, action *expr.Expr, c *Context) Result {
	if c.Program.DryRun {
		return captureOutcome(c, action, core.Value{}, core.Value{})
	}
	if state.Failure.Code != "" && state.Outcome.Kind != core.Record {
		return processFailureOutcome(c, action, state.Failure, core.Value{Kind: core.String, Text: string(state.Output)})
	}
	if state.Outcome.Kind != core.Record {
		return captureOutcome(c, action, core.Value{Kind: core.Record}, core.Value{Kind: core.String, Text: string(state.Output)})
	}
	result := Result{Value: borrowValue(c.Run, state.Outcome)}
	if !state.Function {
		for i := range result.Value.Record {
			if field := &result.Value.Record[i]; field.Key == "value" {
				freeValueWithCallables(c.Run, &field.Value)
				field.Value = core.NewString(c.Run, string(state.Output))
				break
			}
		}
	}
	return result
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
		result.Value = borrowValue(a, state.Value)
	}
	return result
}
