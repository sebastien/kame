package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"slices"
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
	p.KashFunctions = append(p.KashFunctions, f)
	return kashFunctionValue(p, f)
}

func kashFunctionValue(p *Program, f *Function) core.Value {
	text := ""
	if f.KashConstructor {
		text = "kash"
	}
	value := core.Value{Kind: core.Callable, Callable: f, CallableOwner: p, Text: text}
	if f.KashSignature.Mode == core.SignatureContent {
		value.CallableSignature = &f.KashSignature
	}
	return value
}

func (p *Program) IsKashConstructor(v core.Value) bool {
	if v.Kind != core.Callable || v.CallableOwner != p {
		return false
	}
	f := v.Callable.(*Function)
	return f.Kind == FunctionInvocation && f.KashConstructor
}

// KameSelector is the shell value selected by the name kame. The string "kame"
// remains an external executable. The selector is not a script constructor.
func (p *Program) KameSelector() core.Value {
	if p.kameSelector == nil {
		f := mem.Alloc[Function](p.Alloc)
		f.Kind, f.Owner, f.KameSelector = FunctionInvocation, p, true
		p.kameSelector = f
	}
	return core.Value{Kind: core.Callable, Callable: p.kameSelector, CallableOwner: p, Text: "kame"}
}

func (p *Program) IsKameSelector(v core.Value) bool {
	if v.Kind != core.Callable || v.CallableOwner != p || v.Callable == nil {
		return false
	}
	f := v.Callable.(*Function)
	return f.Kind == FunctionInvocation && f.KameSelector
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
	// Only the global scope has no captured invocation-local bindings. Its
	// referenced definitions and other reads remain tracked as dependencies.
	if f.Scope == p.Scope {
		digest := core.NewDigest()
		digest.Text("kame-global-kash-callable-v1")
		digest.Text(parsed.Source.Text)
		f.KashSignature.Mode = core.SignatureContent
		digest.Sum(f.KashSignature.Digest[:])
	}
	f.Scope.Retain()
	p.KashFunctions = append(p.KashFunctions, f)
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
	mem.FreeSlice(nil, p.KashFunctions)
}

type kashInvocation struct {
	Program         *Program
	Parsed          *script.Script
	Items           []script.ScriptItem
	Native          bool
	OwnParsed       bool
	Scope           *Scope
	Group           int64
	Index           int
	Value           core.Value
	Effects         []Effect
	WritePaths      []string
	ClockID         int64
	StartedAt       int64
	ClockStarted    bool
	TimedIndex      int
	RemainingMS     int64
	TimeoutLimitMS  int64
	Deferred        *DeferScope
	Finishing       bool
	Primary         diagnostic.Diagnostic
	PropagateReturn bool
	Returned        bool
	LoopTransfer    expr.Kind
}

func freeKashInvocation(a mem.Allocator, value any) {
	state := value.(*kashInvocation)
	state.Program.cancelScriptGroup(state.Group)
	state.Primary.Free(a)
	state.Scope.Free()
	if state.OwnParsed {
		state.Parsed.Free()
	}
	if state.Native {
		mem.FreeSlice(nil, state.Items)
	}
	freeCallables(a, &state.Value)
	state.Value.Free(a)
	FreeEffects(a, state.Effects)
	for i := range state.WritePaths {
		mem.FreeString(a, state.WritePaths[i])
	}
	mem.FreeSlice(nil, state.WritePaths)
	mem.Free(a, state)
}

// RunKash parses authored recipe text once and retains its AST across suspension.
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
	return p.invokeKashBody(parsed, nil, scope, args, c)
}

func (p *Program) invokeKashBody(parsed *script.Script, native *expr.Expr, scope *Scope, args []core.Value, c *Context) Result {
	if c.Phase != EvaluatePhase || c.PurePredicate {
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
		state.Deferred = p.NewDeferScope(state.Group, c.Engine.NodeID())
		state.Deferred.CallPath = owned(p.Alloc, c.CallPath)
		state.Deferred.InheritedDeadlineNS = c.CleanupDeadlineNS
		if deadline := p.FinalizerDeadline(c.Engine.NodeID()); deadline != 0 && (state.Deferred.InheritedDeadlineNS == 0 || deadline < state.Deferred.InheritedDeadlineNS) {
			state.Deferred.InheritedDeadlineNS = deadline
		}
		if native != nil {
			state.Native = true
			state.PropagateReturn = native.Kind == expr.KashWith
			initialized := p.kashBodyDefinitions(state.Scope, native.Body, c)
			if initialized.Diagnostic.Code != "" {
				freeKashInvocation(p.Alloc, state)
				return initialized
			}
			for i := range native.Body {
				e := native.Body[i]
				kind := script.Expression
				if e.Kind == expr.KashDefinition || e.Kind == expr.KashComment {
					continue
				}
				if e.Kind == expr.CommandGraph || e.Kind == expr.CommandList || e.Kind == expr.KashReturn || expr.IsKashControl(e.Kind) {
					kind = script.Command
				}
				state.Items = append(state.Items, script.ScriptItem{Kind: kind, Expression: e, Span: e.Span})
			}
		} else {
			state.Items = parsed.Items
		}
		for i := range state.Items {
			item := state.Items[i]
			if item.Kind != script.Definition {
				continue
			}
			d := item.Definition
			if d.Eager {
				state.Scope.reserveEager(d.Name)
				continue
			}
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
	previousDeferred := c.Deferred
	previousTimeout := c.TimeoutMS
	previousLoopDepth := c.loopDepth
	if !state.PropagateReturn {
		c.loopDepth = 0
	}
	c.Args, c.HasArgs, c.Scope, c.ScriptGroup = args, true, state.Scope, state.Group
	if state.PropagateReturn {
		c.HasArgs = previousHasArgs
	}
	c.Deferred = state.Deferred
	c.CallPath = invocationPath(c.Run, previousPath, -1, int(state.Group))
	// Constructed scripts have independent authored spans; direct recipe offsets
	// are mapped back to their source by the build runtime.
	if parsed != nil {
		c.Source = parsed.Source.Name
	}
	r := p.advanceKash(state, c)
	mem.FreeString(c.Run, c.CallPath)
	c.Args, c.HasArgs, c.Scope = previousArgs, previousHasArgs, previousScope
	c.ScriptGroup, c.CallPath, c.Source = previousGroup, previousPath, previousSource
	c.Deferred = previousDeferred
	c.TimeoutMS = previousTimeout
	c.loopDepth = previousLoopDepth
	if r.Diagnostic.Code != "" {
		p.cancelScriptGroup(state.Group)
	}
	return r
}

func (p *Program) advanceKash(state *kashInvocation, c *Context) Result {
	for i := range state.Effects {
		effect := state.Effects[i]
		c.Effects = append(c.Effects, Effect{Kind: effect.Kind, Span: effect.Span, Data: slices.Clone(effect.Data)})
	}
	for i := range state.WritePaths {
		c.WritePaths = append(c.WritePaths, owned(c.Run, state.WritePaths[i]))
	}
	if !state.Finishing {
		r := p.advanceKashBody(state, c)
		if r.Waiting {
			return r
		}
		state.Finishing = true
		state.Primary = r.Diagnostic.Clone(p.Alloc)
		r.Free(c.Run)
		if state.Primary.Code != "" && !state.Primary.Exit.Requested {
			p.cancelScriptGroup(state.Group)
		}
	}
	if state.Primary.Exit.Requested {
		if p.DefinitionEffectSink != nil {
			// Reached public output precedes teardown, and must not be replayed
			// when an exited invocation suspends in its join or cleanup.
			p.streamControlOutput(c, 0)
			kept := 0
			for i := range state.Effects {
				if state.Effects[i].Kind == EffectOut || state.Effects[i].Kind == EffectErr {
					state.Effects[i].Free(p.Alloc)
				} else {
					state.Effects[kept] = state.Effects[i]
					kept++
				}
			}
			state.Effects = state.Effects[:kept]
		}
		return p.FinishExit(&state.Primary, state.Deferred, c)
	}
	r := p.finishDefers(state.Deferred, c, state.Primary.Code != "")
	if r.Waiting {
		return r
	}
	if state.Primary.Code != "" {
		primary := state.Primary.Clone(c.Run)
		if r.Diagnostic.Code != "" {
			primary.Notes = append(primary.Notes, owned(c.Run, "deferred cleanup failed: "+r.Diagnostic.Code+": "+r.Diagnostic.Message))
			for i := range r.Diagnostic.Notes {
				primary.Notes = append(primary.Notes, owned(c.Run, r.Diagnostic.Notes[i]))
			}
		}
		r.Free(c.Run)
		return Result{Diagnostic: primary}
	}
	if r.Diagnostic.Code != "" {
		return r
	}
	r.Free(c.Run)
	return Result{Value: state.Value.Clone(c.Run), Returned: state.PropagateReturn && state.Returned, LoopTransfer: state.LoopTransfer}
}

func (p *Program) advanceKashBody(state *kashInvocation, c *Context) Result {
	for state.Index < len(state.Items) {
		item := state.Items[state.Index]
		if item.Kind == script.Definition && item.Definition.Eager {
			item.Kind, item.Expression = script.Expression, item.Definition.Expression
		}
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
			if r.Diagnostic.Exit.Requested {
				p.retainKashEffects(state, c, before, beforePaths)
			}
			return r
		}
		p.retainKashEffects(state, c, before, beforePaths)
		for i := range state.Scope.Bindings {
			b := state.Scope.Bindings[i]
			if b.Kind == bindingLocalDefinition && b.Local.Done {
				b.Local.EffectsHeld = true
			}
		}
		freeCallables(p.Alloc, &state.Value)
		state.Value.Free(p.Alloc)
		if item.Kind == script.Expression && !r.Returned {
			freeCallables(p.Alloc, &r.Value)
			r.Value.Free(p.Alloc)
			state.Value = core.Value{}
		} else {
			state.Value = r.Value
		}
		if r.Returned || r.LoopTransfer != expr.Invalid {
			state.Returned = r.Returned
			state.LoopTransfer = r.LoopTransfer
			state.Index = len(state.Items)
		} else {
			state.Index++
		}
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

func (p *Program) retainKashEffects(state *kashInvocation, c *Context, before int, beforePaths int) {
	for i := before; i < len(c.Effects); i++ {
		effect := c.Effects[i]
		effect.Data = slices.Clone(effect.Data)
		state.Effects = append(state.Effects, effect)
	}
	for i := beforePaths; i < len(c.WritePaths); i++ {
		state.WritePaths = append(state.WritePaths, owned(p.Alloc, c.WritePaths[i]))
	}
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
	if state.TimeoutLimitMS > 0 && elapsed >= state.TimeoutLimitMS {
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
		// The owning lifetime has joined or deliberately discarded this outcome;
		// a later session-wide join must not re-raise its cancellation/failure.
		task.Observed = true
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
