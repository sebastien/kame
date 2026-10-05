package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type localDefinition struct {
	Expression *expr.Expr
	Scope *Scope
	Active bool
	Done bool
	Value core.Value
	Diagnostic diagnostic.Diagnostic
	Effects []Effect
	EffectsHeld bool
	EffectAttempt int64
}

func (p *Program) localValue(local *localDefinition, c *Context) Result {
	if local.Done {
		replayLocalEffects(local, c)
		return Result{Value: local.Value.Clone(c.Run), Diagnostic: local.Diagnostic.Clone(c.Run)}
	}
	if local.Active { return failure(c.Run, "DEP_CYCLE", local.Expression.Span, "cyclic branch definition") }
	local.Active = true
	before := len(c.Effects)
	callerScope := c.Scope
	r := p.evaluate(c.Engine, local.Scope, local.Expression, c)
	c.Scope = callerScope
	local.Active = false
	if !r.Waiting {
		if r.Value.HasTransientCallable() && r.Diagnostic.Code == "" { r.Free(c.Run); return failure(c.Run, "EXPR_INVALID", local.Expression.Span, "callable cannot cross evaluation boundary") }
		local.Done, local.Value, local.Diagnostic = true, r.Value.Clone(local.Scope.Alloc), r.Diagnostic.Clone(local.Scope.Alloc)
		for i := before; i < len(c.Effects); i++ {
			effect := c.Effects[i]
			if p.DefinitionEffectSink != nil && r.Diagnostic.Code == "" { p.DefinitionEffectSink(p.DefinitionEffectState, effect) } else { local.Effects = slices.Append(local.Scope.Alloc, local.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(local.Scope.Alloc, effect.Data)}) }
		}
		if p.DefinitionEffectSink != nil && r.Diagnostic.Code == "" {
			for i := before; i < len(c.Effects); i++ { c.Effects[i].Free(c.Run) }
			c.Effects = c.Effects[:before]
			local.EffectsHeld = true
		}
		local.EffectAttempt = c.Engine.Attempt()
	}
	return r
}

func replayLocalEffects(local *localDefinition, c *Context) {
	if !local.Done || local.EffectsHeld || local.EffectAttempt == c.Engine.Attempt() { return }
	for i := range local.Effects { effect := local.Effects[i]; c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(c.Run, effect.Data)}) }
	local.EffectAttempt = c.Engine.Attempt()
}

type kashControlState struct {
	Branch int
	Selected bool
	Scope *Scope
	Index int
	Value core.Value
	Effects []Effect
	Subject core.Value
	SubjectReady bool
}

func freeKashControlState(a mem.Allocator, value any) {
	state := value.(*kashControlState)
	state.Scope.Free()
	state.Value.Free(a)
	state.Subject.Free(a)
	FreeEffects(a, state.Effects)
	mem.Free(a, state)
}

func (p *Program) retainControlEffects(state *kashControlState, c *Context, scope *Scope, before int) {
	for i := before; i < len(c.Effects); i++ {
		effect := c.Effects[i]
		if p.DefinitionEffectSink != nil { p.DefinitionEffectSink(p.DefinitionEffectState, effect) } else { state.Effects = slices.Append(p.Alloc, state.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(p.Alloc, effect.Data)}) }
	}
	if p.DefinitionEffectSink != nil {
		for i := before; i < len(c.Effects); i++ { c.Effects[i].Free(c.Run) }
		c.Effects = c.Effects[:before]
		for current := scope; current != nil; current = current.Parent { for i := range current.Bindings { b := current.Bindings[i]; if b.Kind == bindingLocalDefinition && b.Local.Done { b.Local.EffectsHeld = true } } }
	}
}

func (p *Program) kashControl(scope *Scope, e *expr.Expr, c *Context) Result {
	if c.Engine == nil { return failure(c.Run, "HOST_FAIL", e.Span, "Kash control requires an execution engine") }
	previousStart, previousEnd := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	var state *kashControlState
	if stored := c.OperationState(); stored != nil { state = stored.(*kashControlState) }
	if state == nil { state = mem.Alloc[kashControlState](p.Alloc); c.SetOperationState(state, freeKashControlState) }
	c.operationStart, c.operationEnd = previousStart, previousEnd
	for i := range state.Effects { effect := state.Effects[i]; c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(c.Run, effect.Data)}) }
	if e.Kind == expr.KashMatch && !state.SubjectReady {
		before := len(c.Effects)
		r := p.evaluate(c.Engine, scope, e.Body[0], c)
		if r.Waiting || r.Diagnostic.Code != "" { return r }
		if r.Value.Kind != core.String && r.Value.Kind != core.Pattern { r.Free(c.Run); return failure(c.Run, "EXPR_INVALID", e.Body[0].Span, "match subject must be text") }
		state.Subject, state.SubjectReady = r.Value.Clone(p.Alloc), true
		r.Free(c.Run)
		p.retainControlEffects(state, c, scope, before)
	}
	for !state.Selected && state.Branch < len(e.Items) {
		branch := e.Items[state.Branch]
		selected := true
		before := len(c.Effects)
		if len(branch.Items) != 0 {
			r := p.evaluate(c.Engine, scope, branch.Items[0], c)
			if r.Waiting || r.Diagnostic.Code != "" { return r }
			if e.Kind == expr.KashMatch {
				matched := matchPattern(c.Run, state.Subject.Text, &r.Value, branch.Items[0].Span)
				for i := range matched.Names { if matched.Names[i] == "env" && matched.Diag.Code == "" { denied := failure(c.Run, "DEF_INVALID", branch.Items[0].Span, "env is reserved in Kash"); matched.Diag = denied.Diagnostic } }
				selected = matched.Matched
				if selected {
					state.Scope = newScope(p.Alloc, scope)
					for i := range matched.Names { if matched.Names[i] != "" { value := core.NewString(p.Alloc, matched.Captures[i]); state.Scope.setValue(matched.Names[i], value); value.Free(p.Alloc) } }
				}
				for i := range matched.Names { mem.FreeString(c.Run, matched.Names[i]) }
				for i := range matched.Captures { mem.FreeString(c.Run, matched.Captures[i]) }
				slices.Free(c.Run, matched.Names)
				slices.Free(c.Run, matched.Captures)
				if matched.Diag.Code != "" { r.Free(c.Run); return Result{Diagnostic: matched.Diag} }
			} else { selected = r.Value.Kind != core.Nil && !(r.Value.Kind == core.Bool && !r.Value.Bool) }
			r.Free(c.Run)
		}
		p.retainControlEffects(state, c, scope, before)
		if !selected { state.Branch++; continue }
		state.Selected = true
		if state.Scope == nil { state.Scope = newScope(p.Alloc, scope) }
		for i := range branch.Body {
			d := branch.Body[i]
			if d.Kind != expr.KashDefinition { continue }
			for j := range state.Scope.Bindings { if state.Scope.Bindings[j].Name == d.Text { return failure(c.Run, "DEF_INVALID", d.Span, "branch definition conflicts with a match capture") } }
			if d.Bool {
				function := mem.Alloc[Function](p.Alloc)
				function.Kind, function.Parameters, function.Expression, function.Scope = FunctionDefinition, d.Parameters, d.Items[0], state.Scope
				state.Scope.setFunction(d.Text, function)
			} else {
				local := mem.Alloc[localDefinition](p.Alloc)
				local.Expression, local.Scope = d.Items[0], state.Scope
				state.Scope.appendBinding(binding{Kind: bindingLocalDefinition, Name: owned(p.Alloc, d.Text), Local: local})
			}
		}
	}
	if !state.Selected { return Result{Value: core.Value{Kind: core.Nil}} }
	body := e.Items[state.Branch].Body
	for state.Index < len(body) {
		statement := body[state.Index]
		if statement.Kind == expr.KashDefinition || statement.Kind == expr.KashComment { state.Index++; continue }
		before := len(c.Effects)
		r := p.evaluate(c.Engine, state.Scope, statement, c)
		if r.Waiting || r.Diagnostic.Code != "" { return r }
		p.retainControlEffects(state, c, state.Scope, before)
		for i := range state.Scope.Bindings { b := state.Scope.Bindings[i]; if b.Kind == bindingLocalDefinition && b.Local.Done { b.Local.EffectsHeld = true } }
		state.Value.Free(p.Alloc)
		state.Value = r.Value.Clone(p.Alloc)
		r.Free(c.Run)
		state.Index++
	}
	return Result{Value: state.Value.Clone(c.Run)}
}
