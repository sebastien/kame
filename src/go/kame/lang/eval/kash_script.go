package eval

import (
	"kame/core"
	"kame/host"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// KashConstructor is a pure, invocation-owned script constructor.
func (p *Program) KashConstructor() core.Value {
	for i := range p.KashFunctions {
		if p.KashFunctions[i].KashConstructor {
			return kashFunctionValue(p, p.KashFunctions[i])
		}
	}
	f := mem.Alloc[Function](p.Alloc)
	f.Kind, f.Owner, f.KashConstructor = FunctionInvocation, p, true
	p.KashFunctions = slices.Append(p.Alloc, p.KashFunctions, f)
	return kashFunctionValue(p, f)
}

func kashFunctionValue(p *Program, f *Function) core.Value {
	text := ""
	if f.KashConstructor {
		text = "kash"
	}
	return core.Value{Kind: core.Callable, Callable: f, CallableOwner: p, Text: text}
}

func (p *Program) IsKashConstructor(v core.Value) bool {
	if v.Kind != core.Callable || v.CallableOwner != p {
		return false
	}
	f := v.Callable.(*Function)
	return f.Kind == FunctionInvocation && f.KashConstructor
}

type kashConstruction struct{ Function *Function }

func freeKashConstruction(a mem.Allocator, state any) { mem.Free(a, state.(*kashConstruction)) }

func (p *Program) constructKash(values []core.Value, c *Context, span source.Span) Result {
	if len(values) != 1 {
		return arityFailure(c.Run, span, "kash", len(values), 1, 1)
	}
	if values[0].Kind != core.String {
		return failure(c.Run, "EXPR_INVALID", span, "kash requires script text")
	}
	if stored := c.OperationState(); stored != nil {
		state := stored.(*kashConstruction)
		return Result{Value: kashFunctionValue(p, state.Function)}
	}
	parsed := script.ParseKash(p.Alloc, "<kash>", values[0].Text)
	if len(parsed.Diagnostics) != 0 {
		d := parsed.Diagnostics[0]
		r := failure(c.Run, d.Code, span, d.Message)
		parsed.Free()
		return r
	}
	f := mem.Alloc[Function](p.Alloc)
	f.Kind, f.Owner, f.KashScript, f.Scope = FunctionInvocation, p, parsed, c.Scope
	f.Scope.Retain()
	p.KashFunctions = slices.Append(p.Alloc, p.KashFunctions, f)
	if c.Engine != nil {
		state := mem.Alloc[kashConstruction](p.Alloc)
		state.Function = f
		c.SetOperationState(state, freeKashConstruction)
	}
	return Result{Value: kashFunctionValue(p, f)}
}

func (p *Program) freeKashFunctions() {
	// Keep every function alive while captured scopes discard their bindings.
	for i := range p.KashFunctions {
		f := p.KashFunctions[i]
		f.Scope.Free()
		if f.KashScript != nil {
			f.KashScript.Free()
		}
	}
	for i := range p.KashFunctions {
		mem.Free(p.Alloc, p.KashFunctions[i])
	}
	slices.Free(p.Alloc, p.KashFunctions)
}

type kashInvocation struct {
	Program        *Program
	Parsed         *script.Script
	OwnParsed      bool
	Scope          *Scope
	Group          int64
	Index          int
	Value          core.Value
	Effects        []Effect
	WritePaths     []string
	ClockID        int64
	StartedAt      int64
	ClockStarted   bool
	TimedIndex     int
	RemainingMS    int64
	TimeoutLimitMS int64
}

func freeKashInvocation(a mem.Allocator, value any) {
	state := value.(*kashInvocation)
	state.Program.cancelScriptGroup(state.Group)
	state.Scope.Free()
	if state.OwnParsed {
		state.Parsed.Free()
	}
	freeCallables(a, &state.Value)
	state.Value.Free(a)
	FreeEffects(a, state.Effects)
	for i := range state.WritePaths {
		mem.FreeString(a, state.WritePaths[i])
	}
	slices.Free(a, state.WritePaths)
	mem.Free(a, state)
}

// RunKash executes rendered recipe text in the current engine and policy.
func (p *Program) RunKash(text string, c *Context, span source.Span) Result {
	previousStart, previousEnd := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = span.Start, span.End
	var parsed *script.Script
	var state *kashInvocation
	if stored := c.OperationState(); stored != nil {
		state = stored.(*kashInvocation)
		parsed = state.Parsed
	}
	if parsed == nil {
		parsed = script.ParseKash(p.Alloc, "<recipe>", text)
		if len(parsed.Diagnostics) != 0 {
			d := parsed.Diagnostics[0]
			r := failure(c.Run, d.Code, d.Span, d.Message)
			parsed.Free()
			c.operationStart, c.operationEnd = previousStart, previousEnd
			return r
		}
	}
	r := p.invokeKash(parsed, c.Scope, c.Args, c)
	if state == nil {
		if stored := c.OperationState(); stored != nil {
			state = stored.(*kashInvocation)
			state.OwnParsed = true
		} else {
			parsed.Free()
		}
	}
	c.operationStart, c.operationEnd = previousStart, previousEnd
	return r
}

func (p *Program) invokeKash(parsed *script.Script, scope *Scope, args []core.Value, c *Context) Result {
	if c.Phase == PlanningPhase || c.Phase == ResolvingPhase {
		return failure(c.Run, "PHASE_INVALID", c.Span, "Kash execution is invalid in this phase")
	}
	if c.Engine == nil {
		return failure(c.Run, "HOST_FAIL", c.Span, "Kash execution requires an execution engine")
	}
	var state *kashInvocation
	if stored := c.OperationState(); stored != nil {
		state = stored.(*kashInvocation)
	}
	if state == nil {
		state = mem.Alloc[kashInvocation](p.Alloc)
		state.Program = p
		state.TimedIndex, state.TimeoutLimitMS = -1, c.TimeoutMS
		state.Parsed, state.Scope = parsed, newScope(p.Alloc, scope)
		p.NextScriptGroup++
		state.Group = p.NextScriptGroup
		for i := range parsed.Items {
			item := parsed.Items[i]
			if item.Kind != script.Definition {
				continue
			}
			d := item.Definition
			if d.Function {
				f := mem.Alloc[Function](p.Alloc)
				f.Kind, f.Parameters, f.Expression, f.Scope, f.ParametersOwned, f.Definition = FunctionDefinition, convertParameters(p.Alloc, d.Parameters), d.Expression, state.Scope, true, d
				state.Scope.setFunction(d.Name, f)
			} else {
				local := mem.Alloc[localDefinition](p.Alloc)
				local.Expression, local.Scope = d.Expression, state.Scope
				state.Scope.appendBinding(binding{Kind: bindingLocalDefinition, Name: owned(p.Alloc, d.Name), Local: local})
			}
		}
		c.SetOperationState(state, freeKashInvocation)
	}
	previousArgs, previousHasArgs, previousScope := c.Args, c.HasArgs, c.Scope
	previousGroup, previousPath, previousSource := c.ScriptGroup, c.CallPath, c.Source
	previousTimeout := c.TimeoutMS
	c.Args, c.HasArgs, c.Scope, c.ScriptGroup = args, true, state.Scope, state.Group
	c.CallPath = invocationPath(c.Run, previousPath, -1, int(state.Group))
	// Constructed scripts have independent authored spans; recipe text keeps its
	// source name so the build runtime can map rendered offsets to recipe lines.
	c.Source = parsed.Source.Name
	r := p.advanceKash(state, c)
	mem.FreeString(c.Run, c.CallPath)
	c.Args, c.HasArgs, c.Scope = previousArgs, previousHasArgs, previousScope
	c.ScriptGroup, c.CallPath, c.Source = previousGroup, previousPath, previousSource
	c.TimeoutMS = previousTimeout
	if r.Diagnostic.Code != "" {
		p.cancelScriptGroup(state.Group)
	}
	return r
}

func (p *Program) advanceKash(state *kashInvocation, c *Context) Result {
	for i := range state.Effects {
		effect := state.Effects[i]
		c.Effects = slices.Append(c.Run, c.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(c.Run, effect.Data)})
	}
	for i := range state.WritePaths {
		c.WritePaths = slices.Append(c.Run, c.WritePaths, owned(c.Run, state.WritePaths[i]))
	}
	for state.Index < len(state.Parsed.Items) {
		item := state.Parsed.Items[state.Index]
		if item.Kind != script.Command && item.Kind != script.Expression {
			state.Index++
			continue
		}
		if c.TimeoutMS > 0 {
			clock := p.kashBudget(state, c)
			if clock.Waiting || clock.Diagnostic.Code != "" {
				return clock
			}
			c.TimeoutMS = state.RemainingMS
		}
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		r := p.evaluate(c.Engine, state.Scope, item.Expression, c)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		for i := before; i < len(c.Effects); i++ {
			effect := c.Effects[i]
			state.Effects = slices.Append(p.Alloc, state.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(p.Alloc, effect.Data)})
		}
		for i := beforePaths; i < len(c.WritePaths); i++ {
			state.WritePaths = slices.Append(p.Alloc, state.WritePaths, owned(p.Alloc, c.WritePaths[i]))
		}
		for i := range state.Scope.Bindings {
			b := state.Scope.Bindings[i]
			if b.Kind == bindingLocalDefinition && b.Local.Done {
				b.Local.EffectsHeld = true
			}
		}
		freeCallables(p.Alloc, &state.Value)
		state.Value.Free(p.Alloc)
		state.Value = r.Value
		state.Index++
	}
	joined := p.joinProcesses(c)
	if joined.Waiting || joined.Diagnostic.Code != "" {
		return joined
	}
	joined.Free(c.Run)
	p.cancelScriptGroup(state.Group)
	if state.Value.HasTransientCallable() {
		return failure(c.Run, "EXPR_INVALID", c.Span, "script result cannot retain a temporary callable")
	}
	return Result{Value: state.Value.Clone(c.Run)}
}

func (p *Program) kashBudget(state *kashInvocation, c *Context) Result {
	_ = p
	if state.TimedIndex == state.Index {
		return Result{}
	}
	if state.ClockID == 0 {
		state.ClockID = c.Submit(host.RequestMonotonicTime, core.Value{})
		if state.ClockID == 0 {
			return failure(c.Run, "HOST_FAIL", c.Span, "cannot obtain script clock")
		}
		return Result{Waiting: true}
	}
	if c.Completion().RequestID != state.ClockID {
		return Result{Waiting: true}
	}
	completion := c.TakeCompletion()
	state.ClockID = 0
	if completion.Diagnostic.Code != "" {
		return Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
	}
	now := completion.Value.Int
	completion.Value.Free(c.Run)
	if !state.ClockStarted {
		state.StartedAt, state.ClockStarted = now, true
	}
	elapsed := (now - state.StartedAt) / 1000000
	if elapsed >= state.TimeoutLimitMS {
		return failure(c.Run, "RECIPE_TIMEOUT", c.Span, "recipe timed out")
	}
	state.RemainingMS, state.TimedIndex = state.TimeoutLimitMS-elapsed, state.Index
	return Result{}
}

func (p *Program) cancelScriptGroup(group int64) {
	for i := range p.Processes {
		task := p.Processes[i]
		if task.Context.ScriptGroup != group {
			continue
		}
		if task.Root != nil {
			p.Engine.Release(task.Root)
			task.Root = nil
		}
		if task.LauncherRoot != nil {
			p.Engine.Release(task.LauncherRoot)
			task.LauncherRoot = nil
		}
	}
}
