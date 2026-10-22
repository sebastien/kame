package eval

import (
	"kame/core"
	"kame/lang/expr"
	"solod.dev/so/mem"
)

type kashLoopState struct {
	Control   *kashControlState
	Budget    kashInvocation
	Iterable  core.Value
	Ready     bool
	InBody    bool
	Done      bool
	Iteration int
}

func freeKashLoop(a mem.Allocator, value any) {
	state := value.(*kashLoopState)
	freeKashControlState(a, state.Control)
	freeCallables(a, &state.Iterable)
	state.Iterable.Free(a)
	mem.Free(a, state)
}

func (p *Program) kashLoop(scope *Scope, e *expr.Expr, c *Context) Result {
	if c.Phase != EvaluatePhase || c.PurePredicate {
		return failure(c.Run, "PHASE_INVALID", e.Span, "Kash iteration requires runtime execution")
	}
	if p.DryRun {
		return Result{}
	}
	if c.Engine == nil {
		return failure(c.Run, "HOST_FAIL", e.Span, "Kash iteration requires an execution engine")
	}
	start, end := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	var state *kashLoopState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*kashLoopState)
	}
	if state == nil {
		state = mem.Alloc[kashLoopState](p.Alloc)
		state.Control = mem.Alloc[kashControlState](p.Alloc)
		state.Budget.TimedIndex = -1
		state.Budget.TimeoutLimitMS = c.TimeoutMS
		c.SetOperationState(state, freeKashLoop)
	}
	c.operationStart, c.operationEnd = start, end
	replayControlEffects(state.Control, c)
	if state.Done {
		return Result{}
	}
	if e.Kind == expr.KashFor && !state.Ready {
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		r := p.evaluate(c.Engine, scope, e.Items[0].Items[0], c)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		if r.Value.Kind != core.List {
			freeCallables(c.Run, &r.Value)
			r.Free(c.Run)
			return failure(c.Run, "EXPR_INVALID", e.Span, "for iterable must be a list")
		}
		state.Iterable, state.Ready = borrowValue(p.Alloc, r.Value), true
		freeCallables(c.Run, &r.Value)
		r.Free(c.Run)
		p.retainControlEffects(state.Control, c, scope, before, beforePaths)
	}
	previousPath, previousTimeout := c.CallPath, c.TimeoutMS
	c.CallPath = invocationPath(c.Run, previousPath, e.Span.Start, state.Iteration)
	r := p.advanceKashLoop(scope, e, state, c)
	mem.FreeString(c.Run, c.CallPath)
	c.CallPath, c.TimeoutMS = previousPath, previousTimeout
	return r
}

func (p *Program) advanceKashLoop(scope *Scope, e *expr.Expr, state *kashLoopState, c *Context) Result {
	if e.Kind == expr.KashFor && state.Iteration >= len(state.Iterable.List) {
		state.Done = true
		return Result{}
	}
	// One clock boundary per iteration yields to the host even for pure infinite
	// loops, so interruption and deadlines remain serviceable on native and WASM.
	clock := p.kashBudget(&state.Budget, c)
	if clock.Waiting || clock.Diagnostic.Code != "" {
		return clock
	}
	if state.Budget.TimeoutLimitMS > 0 {
		c.TimeoutMS = state.Budget.RemainingMS
	}
	control, branch := state.Control, e.Items[0]
	if !state.InBody {
		if e.Kind == expr.KashWhile {
			before, beforePaths := len(c.Effects), len(c.WritePaths)
			r := p.evaluate(c.Engine, scope, branch.Items[0], c)
			if r.Waiting || r.Diagnostic.Code != "" {
				return r
			}
			selected := r.Value.Kind != core.Nil && !(r.Value.Kind == core.Bool && !r.Value.Bool)
			freeCallables(c.Run, &r.Value)
			r.Free(c.Run)
			p.retainControlEffects(control, c, scope, before, beforePaths)
			if !selected {
				state.Done = true
				return Result{}
			}
		}
		control.Scope = newScope(p.Alloc, scope)
		if e.Kind == expr.KashFor {
			item := borrowValue(p.Alloc, state.Iterable.List[state.Iteration])
			control.Scope.setValue(e.Text, item)
			item.Free(p.Alloc)
		}
		initialized := p.kashBodyDefinitions(control.Scope, branch.Body, c)
		if initialized.Diagnostic.Code != "" {
			return initialized
		}
		state.InBody = true
		state.Budget.Index++
	}
	r := p.kashBodyBudget(control, branch.Body, c, &state.Budget)
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	r.Free(c.Run)
	control.Scope.Free()
	control.Scope = nil
	control.Value.Free(p.Alloc)
	control.Index = 0
	state.InBody = false
	state.Iteration++
	state.Budget.Index++
	if e.Kind == expr.KashFor && state.Iteration == len(state.Iterable.List) {
		state.Done = true
		return Result{}
	}
	// Submit the next iteration's clock now, with no recursive evaluation.
	return p.kashBudget(&state.Budget, c)
}
