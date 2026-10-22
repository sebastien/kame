package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type localDefinition struct {
	Expression    *expr.Expr
	Scope         *Scope
	Active        bool
	Done          bool
	Value         core.Value
	Diagnostic    diagnostic.Diagnostic
	Effects       []Effect
	WritePaths    []string
	EffectsHeld   bool
	EffectAttempt int64
}

func (p *Program) localValue(local *localDefinition, c *Context) Result {
	if c.PurePredicate {
		if local.Done {
			return Result{Value: local.Value.Clone(c.Run), Diagnostic: local.Diagnostic.Clone(c.Run)}
		}
		if local.Active {
			return failure(c.Run, "DEP_CYCLE", local.Expression.Span, "cyclic predicate definition")
		}
		local.Active = true
		r := p.evaluate(nil, local.Scope, local.Expression, c)
		local.Active = false
		return r
	}
	if local.Done {
		replayLocalEffects(local, c)
		return Result{Value: local.Value.Clone(c.Run), Diagnostic: local.Diagnostic.Clone(c.Run)}
	}
	if local.Active {
		return failure(c.Run, "DEP_CYCLE", local.Expression.Span, "cyclic branch definition")
	}
	local.Active = true
	before, beforePaths := len(c.Effects), len(c.WritePaths)
	callerScope := c.Scope
	r := p.evaluate(c.Engine, local.Scope, local.Expression, c)
	c.Scope = callerScope
	local.Active = false
	if !r.Waiting {
		if r.Value.HasTransientCallable() && r.Diagnostic.Code == "" {
			r.Free(c.Run)
			return failure(c.Run, "EXPR_INVALID", local.Expression.Span, "callable is invocation-scoped and cannot be stored as a definition; apply it immediately (for example, with map)")
		}
		local.Done, local.Value, local.Diagnostic = true, r.Value.Clone(local.Scope.Alloc), r.Diagnostic.Clone(local.Scope.Alloc)
		for i := beforePaths; i < len(c.WritePaths); i++ {
			local.WritePaths = slices.Append(local.Scope.Alloc, local.WritePaths, owned(local.Scope.Alloc, c.WritePaths[i]))
		}
		for i := before; i < len(c.Effects); i++ {
			effect := c.Effects[i]
			if p.DefinitionEffectSink == nil || r.Diagnostic.Code != "" || (effect.Kind != EffectOut && effect.Kind != EffectErr) {
				local.Effects = slices.Append(local.Scope.Alloc, local.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(local.Scope.Alloc, effect.Data)})
			}
		}
		if p.DefinitionEffectSink != nil && r.Diagnostic.Code == "" {
			p.streamControlOutput(c, before)
			local.EffectsHeld = true
		}
		local.EffectAttempt = c.Engine.Attempt()
	}
	return r
}

func replayLocalEffects(local *localDefinition, c *Context) {
	if !local.Done || local.EffectsHeld || local.EffectAttempt == c.Engine.Attempt() {
		return
	}
	for i := range local.Effects {
		effect := local.Effects[i]
		c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(c.Run, effect.Data)})
	}
	for i := range local.WritePaths {
		c.WritePaths = slices.Append(c.Run, c.WritePaths, owned(c.Run, local.WritePaths[i]))
	}
	local.EffectAttempt = c.Engine.Attempt()
}

type kashControlState struct {
	Branch       int
	Selected     bool
	Scope        *Scope
	Index        int
	Value        core.Value
	Effects      []Effect
	WritePaths   []string
	Subject      core.Value
	SubjectReady bool
}

func freeKashControlState(a mem.Allocator, value any) {
	state := value.(*kashControlState)
	state.Scope.Free()
	state.Value.Free(a)
	state.Subject.Free(a)
	FreeEffects(a, state.Effects)
	for i := range state.WritePaths {
		mem.FreeString(a, state.WritePaths[i])
	}
	slices.Free(a, state.WritePaths)
	mem.Free(a, state)
}

func (p *Program) retainControlEffects(state *kashControlState, c *Context, scope *Scope, before int, beforePaths int) {
	for i := beforePaths; i < len(c.WritePaths); i++ {
		state.WritePaths = slices.Append(p.Alloc, state.WritePaths, owned(p.Alloc, c.WritePaths[i]))
	}
	for i := before; i < len(c.Effects); i++ {
		effect := c.Effects[i]
		if p.DefinitionEffectSink == nil || (effect.Kind != EffectOut && effect.Kind != EffectErr) {
			state.Effects = slices.Append(p.Alloc, state.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(p.Alloc, effect.Data)})
		}
	}
	if p.DefinitionEffectSink != nil {
		p.streamControlOutput(c, before)
		for current := scope; current != nil; current = current.Parent {
			for i := range current.Bindings {
				b := current.Bindings[i]
				if b.Kind == bindingLocalDefinition && b.Local.Done {
					b.Local.EffectsHeld = true
				}
			}
		}
	}
}

// Streaming sinks consume public output, never recipe-owned writes or yields.
func (p *Program) streamControlOutput(c *Context, before int) {
	kept := before
	for i := before; i < len(c.Effects); i++ {
		if c.Effects[i].Kind == EffectOut || c.Effects[i].Kind == EffectErr {
			p.DefinitionEffectSink(p.DefinitionEffectState, c.Effects[i])
			c.Effects[i].Free(c.Run)
		} else {
			c.Effects[kept] = c.Effects[i]
			kept++
		}
	}
	c.Effects = c.Effects[:kept]
}

func replayControlEffects(state *kashControlState, c *Context) {
	for i := range state.Effects {
		effect := state.Effects[i]
		c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(c.Run, effect.Data)})
	}
	for i := range state.WritePaths {
		c.WritePaths = slices.Append(c.Run, c.WritePaths, owned(c.Run, state.WritePaths[i]))
	}
}

func (p *Program) kashControl(scope *Scope, e *expr.Expr, c *Context) Result {
	if c.Engine == nil {
		return failure(c.Run, "HOST_FAIL", e.Span, "Kash control requires an execution engine")
	}
	previousStart, previousEnd := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	var state *kashControlState
	if stored := c.OperationState(); stored != nil {
		state = stored.(*kashControlState)
	}
	if state == nil {
		state = mem.Alloc[kashControlState](p.Alloc)
		c.SetOperationState(state, freeKashControlState)
	}
	c.operationStart, c.operationEnd = previousStart, previousEnd
	replayControlEffects(state, c)
	if e.Kind == expr.KashMatch && !state.SubjectReady {
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		r := p.evaluate(c.Engine, scope, e.Body[0], c)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		if r.Value.Kind != core.String && r.Value.Kind != core.Pattern {
			r.Free(c.Run)
			return failure(c.Run, "EXPR_INVALID", e.Body[0].Span, "match subject must be text")
		}
		state.Subject, state.SubjectReady = r.Value.Clone(p.Alloc), true
		r.Free(c.Run)
		p.retainControlEffects(state, c, scope, before, beforePaths)
	}
	for !state.Selected && state.Branch < len(e.Items) {
		branch := e.Items[state.Branch]
		selected := true
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		if len(branch.Items) != 0 {
			r := p.evaluate(c.Engine, scope, branch.Items[0], c)
			if r.Waiting || r.Diagnostic.Code != "" {
				return r
			}
			if e.Kind == expr.KashMatch {
				matched := matchPattern(c.Run, state.Subject.Text, &r.Value, branch.Items[0].Span)
				for i := range matched.Names {
					if matched.Names[i] == "env" && matched.Diag.Code == "" {
						denied := failure(c.Run, "DEF_INVALID", branch.Items[0].Span, "env is reserved in Kash")
						matched.Diag = denied.Diagnostic
					}
				}
				selected = matched.Matched
				if selected {
					state.Scope = newScope(p.Alloc, scope)
					for i := range matched.Names {
						if matched.Names[i] != "" {
							value := core.NewString(p.Alloc, matched.Captures[i])
							state.Scope.setValue(matched.Names[i], value)
							value.Free(p.Alloc)
						}
					}
				}
				for i := range matched.Names {
					mem.FreeString(c.Run, matched.Names[i])
				}
				for i := range matched.Captures {
					mem.FreeString(c.Run, matched.Captures[i])
				}
				slices.Free(c.Run, matched.Names)
				slices.Free(c.Run, matched.Captures)
				if matched.Diag.Code != "" {
					r.Free(c.Run)
					return Result{Diagnostic: matched.Diag}
				}
			} else {
				selected = r.Value.Kind != core.Nil && !(r.Value.Kind == core.Bool && !r.Value.Bool)
			}
			r.Free(c.Run)
		}
		p.retainControlEffects(state, c, scope, before, beforePaths)
		if !selected {
			state.Branch++
			continue
		}
		state.Selected = true
		if state.Scope == nil {
			state.Scope = newScope(p.Alloc, scope)
		}
		initialized := p.kashBodyDefinitions(state.Scope, branch.Body, c)
		if initialized.Diagnostic.Code != "" {
			return initialized
		}
	}
	if !state.Selected {
		return Result{Value: core.Value{Kind: core.Nil}}
	}
	return p.kashBody(state, e.Items[state.Branch].Body, c)
}

func (p *Program) kashBodyDefinitions(scope *Scope, body []*expr.Expr, c *Context) Result {
	for i := range body {
		d := body[i]
		if d.Kind != expr.KashDefinition {
			continue
		}
		for j := range scope.Bindings {
			if scope.Bindings[j].Name == d.Text {
				return failure(c.Run, "DEF_INVALID", d.Span, "body definition conflicts with a local binding")
			}
		}
		if d.Bool {
			function := mem.Alloc[Function](p.Alloc)
			function.Kind, function.Parameters, function.Expression, function.Scope = FunctionDefinition, d.Parameters, d.Items[0], scope
			scope.setFunction(d.Text, function)
		} else {
			local := mem.Alloc[localDefinition](p.Alloc)
			local.Expression, local.Scope = d.Items[0], scope
			scope.appendBinding(binding{Kind: bindingLocalDefinition, Name: owned(p.Alloc, d.Text), Local: local})
		}
	}
	return Result{}
}

func (p *Program) kashBody(state *kashControlState, body []*expr.Expr, c *Context) Result {
	return p.kashBodyBudget(state, body, c, nil)
}

func (p *Program) kashBodyBudget(state *kashControlState, body []*expr.Expr, c *Context, budget *kashInvocation) Result {
	for state.Index < len(body) {
		statement := body[state.Index]
		if statement.Kind == expr.KashDefinition || statement.Kind == expr.KashComment {
			state.Index++
			continue
		}
		if budget != nil {
			clock := p.kashBudget(budget, c)
			if clock.Waiting || clock.Diagnostic.Code != "" {
				return clock
			}
			if budget.TimeoutLimitMS > 0 {
				c.TimeoutMS = budget.RemainingMS
			}
		}
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		r := p.evaluate(c.Engine, state.Scope, statement, c)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		p.retainControlEffects(state, c, state.Scope, before, beforePaths)
		for i := range state.Scope.Bindings {
			b := state.Scope.Bindings[i]
			if b.Kind == bindingLocalDefinition && b.Local.Done {
				b.Local.EffectsHeld = true
			}
		}
		state.Value.Free(p.Alloc)
		state.Value = core.Value{}
		if statement.Kind == expr.CommandGraph || expr.IsKashControl(statement.Kind) {
			state.Value = r.Value.Clone(p.Alloc)
		} else {
			freeCallables(c.Run, &r.Value)
		}
		r.Free(c.Run)
		state.Index++
		if budget != nil {
			budget.Index++
		}
	}
	return Result{Value: state.Value.Clone(c.Run)}
}
