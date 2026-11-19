package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
)

func (p *Program) nativeKash(e *expr.Expr, scope *Scope, c *Context) Result {
	if e.Kind == expr.ValueBlock && (c.Engine == nil || c.Phase != EvaluatePhase || c.PurePredicate) {
		previousBlock := c.pureValueBlock
		c.pureValueBlock = true
		if !previousBlock {
			c.pureLoopIterations = 0
		}
		defer func() { c.pureValueBlock = previousBlock }()
		if c.Engine == nil {
			previousPhase := c.Phase
			if c.Phase == EvaluatePhase {
				c.Phase = PlanningPhase
			}
			defer func() { c.Phase = previousPhase }()
			return p.evaluatePureValueBlock(e, scope, c)
		}
	}
	start, end := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	r := p.invokeKashBody(nil, e, scope, c.Args, c)
	c.operationStart, c.operationEnd = start, end
	return r
}

func (p *Program) evaluatePureValueBlock(e *expr.Expr, scope *Scope, c *Context) Result {
	state := mem.Alloc[kashControlState](p.Alloc)
	state.Scope = newScope(p.Alloc, scope)
	defer freeKashControlState(p.Alloc, state)
	previousDepth := c.loopDepth
	c.loopDepth = 0
	defer func() { c.loopDepth = previousDepth }()
	initialized := p.kashBodyDefinitions(state.Scope, e.Body, c)
	if initialized.Diagnostic.Code != "" {
		return initialized
	}
	r := p.kashBody(state, e.Body, c)
	if !r.Returned && r.Diagnostic.Code == "" && !r.Waiting {
		r.Free(c.Run)
		return Result{Value: core.Value{Kind: core.Nil}}
	}
	r.Returned = false
	if r.Value.HasTransientCallable() {
		r.Free(c.Run)
		return failure(c.Run, "EXPR_INVALID", e.Span, "value block cannot return a temporary callable")
	}
	return r
}
