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

// KashSelector is the built-in interpreter value selected by the name kash.
func (p *Program) KashSelector() core.Value {
	if p.kashSelector == nil {
		p.kashSelector = mem.Alloc[Function](p.Alloc)
		p.kashSelector.Kind, p.kashSelector.Owner = FunctionInterpreter, p
	}
	return core.Value{Kind: core.Callable, Callable: p.kashSelector, CallableOwner: p, Text: "kash"}
}

func (p *Program) IsKashSelector(v core.Value) bool {
	return v.Kind == core.Callable && p.kashSelector != nil && v.Callable == p.kashSelector
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
	ExplicitReturn  bool
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
		parsed = script.ParseKashRuleBody(p.Alloc, "<recipe>", text)
		if len(parsed.Diagnostics) != 0 {
			d := parsed.Diagnostics[0]
			r := failure(c.Run, d.Code, d.Span, d.Message)
			parsed.Free()
			c.operationStart, c.operationEnd = previousStart, previousEnd
			return r
		}
		if c.SourceOrigin != 0 {
			// Parse offsets belong to the recipe text. The driver remaps them
			// to the file after evaluation, which still uses the file origin.
			entry := &source.Source{Name: parsed.Source.Name}
			for _, item := range parsed.Items {
				setExpressionOrigin(item.Expression, c.SourceOrigin, entry)
				if item.Definition != nil {
					setExpressionOrigin(item.Definition.Expression, c.SourceOrigin, entry)
				}
			}
		}
	}
	r := p.invokeKashBody(parsed, nil, c.Scope, c.Args, c)
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

func (p *Program) invokeKashBody(parsed *script.Script, native *expr.Expr, scope *Scope, args []core.Value, c *Context) Result {
	if (c.Phase != EvaluatePhase || c.PurePredicate) && (native == nil || native.Kind != expr.ValueBlock) {
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
		if parsed != nil {
			for _, item := range parsed.Items {
				if item.Module != nil {
					return failure(c.Run, "FEATURE_UNSUP", item.Span, "module declarations require a file-backed source")
				}
				if item.Kind == script.Include {
					return failure(c.Run, "FEATURE_UNSUP", item.Span, "include requires a file-backed source")
				}
			}
		}
		state = mem.Alloc[kashInvocation](p.Alloc)
		state.Program = p
		state.TimedIndex, state.TimeoutLimitMS = -1, c.TimeoutMS
		state.Parsed, state.Scope = parsed, newScope(p.Alloc, scope)
		state.Scope.assignmentBoundary = native == nil || native.Kind != expr.KashWith
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
			state.ExplicitReturn = native.Kind == expr.ValueBlock || native.Kind == expr.KashFunction
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
				if e.Kind == expr.CommandGraph || e.Kind == expr.CommandList || expr.IsKashControl(e.Kind) {
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
				state.Scope.reserveEager(d.Expression, false)
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
		c.Effects = append(c.Effects, effect.clone())
	}
	for i := range state.WritePaths {
		c.WritePaths = append(c.WritePaths, owned(c.Run, state.WritePaths[i]))
	}
	if !state.Finishing {
		r := p.advanceKashBody(state, c)
		if r.Waiting || r.Yielded {
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
		if c.hasControlOutputSink() {
			// Reached public output precedes teardown, and must not be replayed
			// when an exited invocation suspends in its join or cleanup.
			p.streamControlOutput(c, 0)
			kept := 0
			for i := range state.Effects {
				if c.streamsControlOutput(state.Effects[i].Kind) {
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
	return Result{Value: borrowValue(c.Run, state.Value), Returned: state.Returned && (state.PropagateReturn || state.Parsed != nil && state.Parsed.RuleBody), LoopTransfer: state.LoopTransfer}
}

func (p *Program) advanceKashBody(state *kashInvocation, c *Context) Result {
	for state.Index < len(state.Items) {
		if stage := c.functionStage; stage != nil && stage.cancelled && stage.node != nil && stage.node.ID == c.Engine.NodeID() {
			return failure(c.Run, "EXEC_CANCELLED", c.Span, "callable output consumer closed")
		}
		item := state.Items[state.Index]
		if item.Kind == script.Definition && item.Definition.Eager {
			item.Kind, item.Expression = script.Expression, item.Definition.Expression
		}
		if item.Kind != script.Command && item.Kind != script.Expression {
			state.Index++
			continue
		}
		if c.TimeoutMS > 0 && !c.pureValueBlock {
			clock := p.kashBudget(state, c)
			if clock.Waiting || clock.Diagnostic.Code != "" {
				return clock
			}
			c.TimeoutMS = state.RemainingMS
		}
		before, beforePaths := len(c.Effects), len(c.WritePaths)
		r := p.evaluate(c.Engine, state.Scope, item.Expression, c)
		if r.Waiting || r.Diagnostic.Code != "" {
			if c.commandOutput != nil && !r.Waiting {
				p.streamControlOutput(c, before)
			}
			if r.Diagnostic.Exit.Requested {
				p.retainKashEffects(state, c, before, beforePaths)
			}
			return r
		}
		p.retainKashEffects(state, c, before, beforePaths)
		if c.commandOutput != nil && c.commandOutput.overflow {
			r.Free(c.Run)
			return failure(c.Run, "CAPTURE_LIMIT", item.Span, "command substitution exceeded capture limit")
		}
		for i := range state.Scope.Bindings {
			b := state.Scope.Bindings[i]
			if b.Kind == bindingLocalDefinition && b.Local.Done {
				b.Local.EffectsHeld = true
			}
		}
		if r.Yielded {
			if item.Expression.Kind == expr.KashYield {
				state.Index++
			}
			return r
		}
		freeCallables(p.Alloc, &state.Value)
		state.Value.Free(p.Alloc)
		if (item.Kind == script.Expression || state.ExplicitReturn) && !r.Returned {
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
	if !state.Native && state.Value.HasTransientCallable() {
		return failure(c.Run, "EXPR_INVALID", c.Span, "script result cannot retain a temporary callable")
	}
	return Result{}
}

func (p *Program) retainKashEffects(state *kashInvocation, c *Context, before int, beforePaths int) {
	if c.commandOutput != nil || c.functionStage != nil || c.RecipeOutput != nil || (c.generator != nil && p.DefinitionEffectSink != nil) {
		p.streamControlOutput(c, before)
	}
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
	if stage := c.functionStage; stage != nil && c.Engine.NodeID() == stage.node.ID {
		if r := stage.flush(c); r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		if stage.cancelled && !c.Finalizing {
			return failure(c.Run, "EXEC_CANCELLED", c.Span, "callable stage output closed")
		}
	}
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
	for handle := range p.callableProcessOutcomes {
		if handle.script == group {
			freeValueWithCallables(mem.System, &handle.outcome)
			delete(p.callableProcessOutcomes, handle)
		}
	}
	for i := range p.Processes {
		task := p.Processes[i]
		if task.Context.ScriptGroup != group {
			continue
		}
		// The owning lifetime has joined or deliberately discarded this outcome;
		// a later session-wide join must not re-raise its cancellation/failure.
		task.observe()
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
