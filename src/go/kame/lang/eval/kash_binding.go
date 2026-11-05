package eval

import (
	"kame/lang/expr"
)

func (p *Program) kashLet(scope *Scope, e *expr.Expr, c *Context) Result {
	if c.Phase != EvaluatePhase || c.PurePredicate {
		return failure(c.Run, "PHASE_INVALID", e.Span, "Kash let requires runtime execution")
	}
	if p.DryRun {
		c.deferred = true
		return Result{Completed: true}
	}
	b := scope.lookup(e.Text)
	if b == nil || b.Kind != bindingEager {
		return failure(c.Run, "DEF_INVALID", e.Span, "let requires a reserved eager binding")
	}
	if b.Initialized {
		return Result{}
	}
	previous := c.sequencedOperand
	c.sequencedOperand = true
	r := p.evaluate(c.Engine, scope, e.Items[0], c)
	c.sequencedOperand = previous
	if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
		return r
	}
	if wouldStrandCapture(scope, r.Value) {
		return rejectStrandedCapture(c.Run, &r.Value, e.Items[0].Span)
	}
	// Initializers can create other bindings, reallocating the binding slice.
	// Reacquire the slot before transferring its immutable value.
	b = scope.lookup(e.Text)
	b.Value, b.Initialized = r.Value.Clone(scope.Alloc), true
	r.Value.Free(c.Run)
	return Result{}
}
