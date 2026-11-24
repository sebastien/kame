package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
	"slices"
	"strings"
)

func (p *Program) kashLet(scope *Scope, e *expr.Expr, c *Context) Result {
	if !c.pureValueBlock && (c.Phase != EvaluatePhase || c.PurePredicate) {
		return failure(c.Run, "PHASE_INVALID", e.Span, "Kash let requires runtime execution")
	}
	if p.DryRun && !c.pureValueBlock {
		c.deferred = true
		return Result{Completed: true}
	}
	b := scope.localResolved(e.Text, e.LetBinding)
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
	scope.hasDescendantCapture = scope.hasDescendantCapture || wouldStrandCapture(scope, r.Value)
	// Initializers can create other bindings, reallocating the binding slice.
	// Reacquire the slot before transferring its immutable value.
	b = scope.localResolved(e.Text, e.LetBinding)
	b.Value, b.Initialized = r.Value, true
	b.Value.Share(scope.Alloc)
	return Result{}
}

func (p *Program) kashSet(scope *Scope, e *expr.Expr, c *Context) Result {
	if strings.HasPrefix(e.Text, "env/") {
		return p.kashSetEnvironment(scope, e, c)
	}
	if !c.pureValueBlock && (c.Phase != EvaluatePhase || c.PurePredicate) {
		return failure(c.Run, "PHASE_INVALID", e.Span, "set requires an active block")
	}
	owner := scope
	for owner != nil && owner.localResolved(e.Text, e.LetBinding) == nil {
		if owner.assignmentBoundary {
			owner = nil
			break
		}
		owner = owner.Parent
	}
	if owner == nil {
		return failure(c.Run, "PARSE_ERR", e.Span, "set requires an existing let in the same invocation")
	}
	b := owner.localResolved(e.Text, e.LetBinding)
	if b.Captured {
		return failure(c.Run, "PARSE_ERR", e.Span, "set cannot assign a captured let")
	}
	if b.Kind != bindingEager || !b.Initialized {
		if p.DryRun && b.Kind == bindingEager {
			c.deferred = true
			return Result{Completed: true}
		}
		return failure(c.Run, "PARSE_ERR", e.Span, "set requires an existing let in the same invocation")
	}
	previous := c.sequencedOperand
	c.sequencedOperand = true
	r := p.evaluate(c.Engine, scope, e.Items[0], c)
	c.sequencedOperand = previous
	if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
		return r
	}
	owner.hasDescendantCapture = owner.hasDescendantCapture || wouldStrandCapture(owner, r.Value)
	// The operand can grow the scope's binding storage.
	b = owner.localResolved(e.Text, e.LetBinding)
	old := b.Value
	b.Value = r.Value
	b.Value.Share(owner.Alloc)
	freeValueWithCallables(owner.Alloc, &old)
	return Result{}
}

func (p *Program) kashSetEnvironment(scope *Scope, e *expr.Expr, c *Context) Result {
	if c.pureValueBlock || c.Phase != EvaluatePhase || c.PurePredicate {
		return failure(c.Run, "PHASE_INVALID", e.Span, "environment assignment requires an action phase")
	}
	if p.DryRun {
		return Result{}
	}
	name := strings.TrimPrefix(e.Text, "env/")
	previous := c.sequencedOperand
	c.sequencedOperand = true
	defer func() { c.sequencedOperand = previous }()
	if len(e.Items) == 2 {
		target := e.Items[1]
		start, end, path := c.operationStart, c.operationEnd, c.CallPath
		c.operationStart, c.operationEnd, c.CallPath = e.Span.Start, e.Span.End, path+"/env-name"
		state, _ := c.OperationState().(*kashControlState)
		c.operationStart, c.operationEnd, c.CallPath = start, end, path
		if state != nil {
			replayControlEffects(state, c)
		} else {
			before, paths := len(c.Effects), len(c.WritePaths)
			r := p.evaluate(c.Engine, scope, target, c)
			if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
				return r
			}
			defer r.Free(c.Run)
			if r.Value.Kind != core.String || !expr.ValidEnvironmentName(r.Value.Text) {
				return failure(c.Run, "EXPR_INVALID", target.Span, "environment assignment requires a valid name string")
			}
			state = mem.Alloc[kashControlState](p.Alloc)
			state.Value = r.Value.Clone(p.Alloc)
			p.retainControlEffects(state, c, scope, before, paths)
			c.operationStart, c.operationEnd, c.CallPath = e.Span.Start, e.Span.End, path+"/env-name"
			c.SetOperationState(state, freeKashControlState)
			c.operationStart, c.operationEnd, c.CallPath = start, end, path
		}
		name = state.Value.Text
	}
	r := p.evaluate(c.Engine, scope, e.Items[0], c)
	if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
		return r
	}
	defer r.Free(c.Run)
	switch r.Value.Kind {
	case core.Nil:
		if !slices.Contains(c.EnvironmentUnset, name) {
			_, inherited := (*ExecutionUnit)(nil).EnvironmentValue(c.Environment, name)
			_, present := c.Unit.EnvironmentValue(c.Environment, name)
			if !c.directSourceUnit() || inherited || !present {
				return failure(c.Run, "CAP_DENIED", e.Span, "environment removal is not authorized for "+name)
			}
		}
		c.setEnvironment(name, "", false)
	case core.String, core.Int, core.Float:
		value, ok := processScalar(c.Run, r.Value)
		if !ok {
			return failure(c.Run, "EXPR_INVALID", e.Items[0].Span, "environment value must not contain NUL")
		}
		c.setEnvironment(name, value, true)
		mem.FreeString(c.Run, value)
	default:
		return failure(c.Run, "EXPR_INVALID", e.Items[0].Span, "environment assignment requires a string, number, path, or nil")
	}
	return Result{}
}
