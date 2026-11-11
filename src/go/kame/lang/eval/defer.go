package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"

	"strconv"
)

// Cleanup has its own finite budget, including after the caller's deadline.
const DeferTimeoutMS int64 = 5000

// DeferScope belongs to one Kash invocation, not an indentation block. Its root
// survives cancellation of the demanding node so hosts can finish unwinding it.
type DeferScope struct {
	Program             *Program
	Owner               int64
	Group               int64
	CallPath            string
	Actions             []*deferredAction
	Index               int
	Node                *core.Node
	Root                *core.Root
	Done                bool
	Observed            bool
	Diagnostic          diagnostic.Diagnostic
	ClockID             int64
	StartedAt           int64
	ClockStarted        bool
	InheritedDeadlineNS int64
}

type deferredAction struct {
	Program    *Program
	Expression *expr.Expr
	Context    Context
	Prepared   *captureState
	Node       *core.Node
	Root       *core.Root
	Evaluated  bool
}

func (p *Program) NewDeferScope(group int64, owner int64) *DeferScope {
	s := mem.Alloc[DeferScope](p.Alloc)
	s.Program, s.Group, s.Owner = p, group, owner
	p.DeferScopes = append(p.DeferScopes, s)
	return s
}

func (p *Program) registerDefer(e *expr.Expr, c *Context) Result {
	if c.Phase == PlanningPhase || c.Phase == ResolvingPhase {
		return failure(c.Run, "PHASE_INVALID", e.Span, "defer requires runtime execution")
	}
	if c.Engine == nil || c.Deferred == nil {
		return failure(c.Run, "EXPR_INVALID", e.Span, "defer requires a Kash invocation")
	}
	if p.DryRun {
		return Result{}
	}
	if c.Deferred.Node != nil {
		return failure(c.Run, "EXPR_INVALID", e.Span, "cannot register cleanup in a closing invocation; use a nested Kash callable")
	}
	if e.Items[0].Kind == expr.CommandGraph {
		previous := c.PrepareDeferred
		c.PrepareDeferred = e.Items[0]
		r := p.capture(e.Items[0], c)
		c.PrepareDeferred = previous
		return r
	}
	previousStart, previousEnd := c.operationStart, c.operationEnd
	c.operationStart, c.operationEnd = e.Span.Start, e.Span.End
	if c.OperationState() == nil {
		registered := mem.Alloc[bool](p.Alloc)
		c.SetOperationState(registered, freeDeferRegistration)
		p.appendDefer(e.Items[0], c, nil)
	}
	c.operationStart, c.operationEnd = previousStart, previousEnd
	return Result{}
}

func freeDeferRegistration(a mem.Allocator, state any) { mem.Free(a, state.(*bool)) }

func (p *Program) appendDefer(e *expr.Expr, c *Context, prepared *captureState) {
	action := mem.Alloc[deferredAction](p.Alloc)
	action.Program, action.Expression, action.Context = p, expr.Clone(p.Alloc, e), p.retainExecutionContext(c)
	action.Context.Finalizing, action.Context.evaluationDepth = true, 0
	// Each action owns and joins any async work launched by its expression.
	p.NextScriptGroup++
	action.Context.ScriptGroup = p.NextScriptGroup
	if prepared != nil {
		action.Prepared = clonePreparedProcess(p.Alloc, prepared)
	}
	c.Deferred.Actions = append(c.Deferred.Actions, action)
}

func (p *Program) startDefers(s *DeferScope, aborting bool) {
	if s == nil || s.Done {
		return
	}
	if aborting {
		p.cancelScriptGroup(s.Group)
	}
	if s.Node != nil {
		return
	}
	if len(s.Actions) == 0 && p.groupQuiet(s.Owner, s.Group) {
		s.Done = true
		return
	}
	s.Index = len(s.Actions) - 1

	key := core.NewResourceKey(p.Alloc, core.ResourceDefinition, "<defer-scope:"+strconv.Itoa(int(s.Group))+">")
	s.Node = p.Engine.Add(key, runDefers, s)
	key.Free(p.Alloc)
	s.Root = p.Engine.RequestRoot(s.Node)
}

func (p *Program) finishDefers(s *DeferScope, c *Context, aborting bool) Result {
	if s == nil {
		return Result{}
	}
	p.startDefers(s, aborting)
	if !s.Done && !c.Engine.TryDependency(s.Node.Key) {
		return Result{Waiting: true}
	}
	s.Observed = true
	return Result{Diagnostic: s.Diagnostic.Clone(c.Run)}
}

func (p *Program) groupQuiet(owner int64, group int64) bool {
	return p.ProcessesQuiet == nil || p.ProcessesQuiet(p.ProcessesQuietState, owner, group)
}

// ProcessInGroup includes deferred-expression workers as well as async graphs.
func (p *Program) ProcessInGroup(node int64, group int64) bool {
	for i := range p.Processes {
		task := p.Processes[i]
		if task.Node.ID == node && task.Context.ScriptGroup == group {
			return true
		}
	}
	for i := range p.DeferScopes {
		for j := range p.DeferScopes[i].Actions {
			action := p.DeferScopes[i].Actions[j]
			if action.Node != nil && action.Node.ID == node && action.Context.ScriptGroup == group {
				return true
			}
		}
	}
	return false
}

func runDefers(engine *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	s := engine.Context().(*DeferScope)
	p, a := s.Program, engine.Allocator()
	if !p.groupQuiet(s.Owner, s.Group) {
		return core.ProducerActive
	}
	if !s.ClockStarted && p.FinalizationNow != nil {
		s.StartedAt, s.ClockStarted = p.FinalizationNow(p.FinalizationHost), true
	}
	for s.Index >= 0 {
		action := s.Actions[s.Index]
		if action.Node != nil {
			if !engine.TryDependency(action.Node.Key) {
				return core.ProducerWaiting
			}
			p.cancelScriptGroup(action.Context.ScriptGroup)
			if !p.groupQuiet(action.Node.ID, action.Context.ScriptGroup) {
				return core.ProducerActive
			}
			if d := engine.DependencyDiagnostic(action.Node.Key); d.Code != "" {
				s.recordFailure(d)
			}
			p.Engine.Release(action.Root)
			action.Root = nil
			s.Index--
			continue
		}
		// Refresh the remaining stack budget before each action. Time is supplied
		// by the ordinary host clock on both native and forwarded WASM runtimes.
		c := Context{Program: p, Engine: engine, Run: a, Requests: p.Requests}
		if s.ClockID == 0 {
			s.ClockID = c.Submit(host.RequestMonotonicTime, core.Value{})
			return core.ProducerSubmitted
		}
		if c.Completion().RequestID != s.ClockID {
			return core.ProducerWaiting
		}
		completion := c.TakeCompletion()
		s.ClockID = 0
		if completion.Diagnostic.Code != "" || completion.Value.Kind != core.Int {
			completion.Value.Free(a)
			completion.Diagnostic.Free(a)
			r := failure(a, "HOST_FAIL", source.Span{}, "cannot obtain cleanup clock")
			s.recordFailure(r.Diagnostic)
			r.Free(a)
			break
		}
		now := completion.Value.Int
		completion.Value.Free(a)
		if !s.ClockStarted {
			s.StartedAt, s.ClockStarted = now, true
		}
		remainingNS := DeferTimeoutMS*1000000 - (now - s.StartedAt)
		if s.InheritedDeadlineNS != 0 && s.InheritedDeadlineNS-now < remainingNS {
			remainingNS = s.InheritedDeadlineNS - now
		}
		remaining := remainingNS / 1000000
		if remaining <= 0 {
			r := failure(a, "RECIPE_TIMEOUT", action.Expression.Span, "cleanup budget exhausted; remaining deferred actions were not run")
			s.recordFailure(r.Diagnostic)
			r.Free(a)
			break
		}
		if action.Context.TimeoutMS == 0 || action.Context.TimeoutMS > remaining {
			action.Context.TimeoutMS = remaining
		}
		if action.Prepared != nil {
			for i := range action.Prepared.Setups {
				setup := &action.Prepared.Setups[i]
				for j := range setup.Record {
					field := &setup.Record[j]
					if field.Key == host.FieldTimeout && (field.Value.Int == 0 || field.Value.Int > action.Context.TimeoutMS) {
						field.Value.Int = action.Context.TimeoutMS
					}
				}
			}
		}

		key := core.NewResourceKey(a, core.ResourceDefinition, "<defer-action:"+strconv.Itoa(int(s.Node.ID))+":"+strconv.Itoa(s.Index)+">")
		action.Node = p.Engine.Add(key, runDeferredAction, action)
		key.Free(a)
		action.Root = p.Engine.RequestRoot(action.Node)
	}
	s.Done = true
	engine.Publish(core.Value{})
	return core.ProducerCompleted
}

func (s *DeferScope) recordFailure(d diagnostic.Diagnostic) {
	if s.Diagnostic.Code == "" {
		s.Diagnostic = d.Clone(s.Program.Alloc)
		return
	}
	s.Diagnostic.Notes = append(s.Diagnostic.Notes, owned(s.Program.Alloc, "additional deferred action failed: "+d.Code+": "+d.Message))
}

func runDeferredAction(engine *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	action := engine.Context().(*deferredAction)
	p := action.Program
	c := action.Context
	c.InputClosed = true
	c.Engine, c.Run, c.Requests = engine, engine.Allocator(), p.Requests
	c.CleanupDeadlineNS = p.FinalizerDeadline(engine.NodeID())
	c.Resume(engine)
	if action.Prepared != nil {
		c.operationStart, c.operationEnd = action.Expression.Span.Start, action.Expression.Span.End
		c.SetOperationState(action.Prepared, freeCaptureState)
		action.Prepared = nil
	}
	r := Result{}
	if !action.Evaluated {
		r = p.EvaluateWith(action.Expression, &c)
		if !r.Waiting && r.Diagnostic.Code == "" {
			action.Evaluated = true
		}
	}
	if !r.Waiting && r.Diagnostic.Code == "" {
		r.Free(c.Run)
		r = p.joinProcesses(&c)
	}
	if (r.Diagnostic.Code == "EXEC_CANCELLED" || r.Diagnostic.Code == "FINALIZE_TIMEOUT") && p.FinalizerBudget(engine.NodeID()) < 0 {
		r.Free(c.Run)
		r = failure(c.Run, "RECIPE_TIMEOUT", action.Expression.Span, "deferred cleanup budget exhausted")
	}
	if (!r.Waiting || action.Evaluated) && p.DefinitionEffectSink != nil {
		for i := range c.Effects {
			p.DefinitionEffectSink(p.DefinitionEffectState, c.Effects[i])
		}
	}
	FreeEffects(c.Run, c.Effects)
	for i := range c.WritePaths {
		mem.FreeString(c.Run, c.WritePaths[i])
	}
	mem.FreeSlice(nil, c.WritePaths)
	if r.Waiting {
		r.Free(c.Run)
		if engine.Submitted() {
			return core.ProducerSubmitted
		}
		return core.ProducerWaiting
	}
	if r.Diagnostic.Code != "" {
		engine.Fail(r.Diagnostic)
		r.Diagnostic = diagnostic.Diagnostic{}
		r.Free(c.Run)
		return core.ProducerFailed
	}
	freeCallables(c.Run, &r.Value)
	r.Free(c.Run)
	engine.Publish(core.Value{})
	return core.ProducerCompleted
}

// Drain surviving Kash invocation scopes after value-language finalizers, in
// reverse creation order (inner before outer), using the shared host driver.
func (p *Program) AdvanceFinalization() bool {
	for i := range p.finalizers {
		if p.finalizers[i].Stage != 4 {
			return false
		}
	}
	for i := len(p.DeferScopes) - 1; i >= 0; i-- {
		s := p.DeferScopes[i]
		if s.Done {
			continue
		}
		p.startDefers(s, true)
		if !s.Done {
			return false
		}
	}
	return true
}

func (p *Program) defersPending() bool {
	if !p.DeferStopping {
		return false
	}
	for i := range p.DeferScopes {
		if !p.DeferScopes[i].Done {
			return true
		}
	}
	return false
}

func (p *Program) FinalizationDiagnostic(a mem.Allocator) diagnostic.Diagnostic {
	var result diagnostic.Diagnostic
	for i := range p.DeferScopes {
		if p.DeferScopes[i].Observed {
			continue
		}
		d := p.DeferScopes[i].Diagnostic
		if d.Code == "" {
			continue
		}
		if result.Code == "" {
			result = d.Clone(a)
		} else {
			result.Notes = append(result.Notes, owned(a, "additional cleanup failure: "+d.Code+": "+d.Message))
		}
	}
	return result
}

func (p *Program) CancelDefers() {
	for i := range p.DeferScopes {
		s := p.DeferScopes[i]
		if s.Root != nil {
			p.Engine.Release(s.Root)
			s.Root = nil
		}
		for j := range s.Actions {
			if s.Actions[j].Root != nil {
				p.Engine.Release(s.Actions[j].Root)
				s.Actions[j].Root = nil
			}
		}
	}
}

func (p *Program) freeDefers() {
	for i := range p.DeferScopes {
		s := p.DeferScopes[i]
		p.freeDeferScope(s)
	}
	mem.FreeSlice(nil, p.DeferScopes)
}

func (p *Program) freeDeferScope(s *DeferScope) {
	for j := range s.Actions {
		action := s.Actions[j]
		if action.Prepared != nil {
			freeCaptureState(p.Alloc, action.Prepared)
		}
		freeExecutionContext(p.Alloc, &action.Context)
		expr.Free(p.Alloc, action.Expression)
		mem.Free(p.Alloc, action)
	}
	mem.FreeSlice(nil, s.Actions)
	s.Diagnostic.Free(p.Alloc)
	mem.FreeString(p.Alloc, s.CallPath)
	mem.Free(p.Alloc, s)
}
