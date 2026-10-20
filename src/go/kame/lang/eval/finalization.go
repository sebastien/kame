package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type FinalizerState struct {
	Program         *Program
	Parent          *FinalizerState
	NodeID          int64
	Generation      int64
	Span            source.Span
	Source          string
	Path            string
	Context         Context
	Body            core.Value
	Cleanup         core.Value
	Outcome         Result
	Stage           int // body, body join, cleanup, cleanup join, done
	TimeoutMS       int64
	Deadline        int64
	UnwindDeadline  int64
	Stopping        bool
	Expired         bool
	CleanupFailed   bool
	TimeoutRecorded bool
}

func (c *Context) forbidsFinalization() bool {
	return c.Phase != EvaluatePhase || c.needsFinalizationView() || (c.Program != nil && c.Program.FinalizationReadOnly)
}

func (c *Context) needsFinalizationView() bool {
	return c.FinalizationForbidden || c.Phase == RenderingPhase || (c.RecipeNode != 0 && len(c.RuleFrames) != 0)
}

func (p *Program) findFinalizer(c *Context, span source.Span) *FinalizerState {
	if c.Engine == nil {
		return nil
	}
	for i := len(p.finalizers) - 1; i >= 0; i-- {
		s := p.finalizers[i]
		if s.NodeID == c.Engine.NodeID() && s.Generation != c.Engine.Generation() && s.Stage == 4 {
			for j := range p.Processes {
				if p.Processes[j].Finalizer == s {
					p.Processes[j].Finalizer = nil
					p.Processes[j].Context.finalizer = nil
				}
			}
			copy(p.finalizers[i:], p.finalizers[i+1:])
			p.finalizers = p.finalizers[:len(p.finalizers)-1]
			freeFinalizer(p.Alloc, s)
		}
	}
	for i := range p.finalizers {
		s := p.finalizers[i]
		if s.NodeID == c.Engine.NodeID() && s.Generation == c.Engine.Generation() && s.Span.Start == span.Start && s.Span.End == span.End && s.Source == c.Source && s.Path == c.CallPath {
			return s
		}
	}
	return nil
}

func acceptsNoArguments(p *Program, v core.Value) bool {
	if v.Kind != core.Callable || v.Callable == nil {
		return false
	}
	f := v.Callable.(*Function)
	if f.Kind == FunctionOperation {
		o := p.operationForCallable(f)
		return o != nil && o.MinArity == 0
	}
	if f.Kind == FunctionInvocation {
		return f.Owner == p && !f.KashConstructor
	}
	if f.NativeCall != nil {
		return f.Arity == 0
	}
	return len(f.Parameters) == 0 || (len(f.Parameters) == 1 && f.Parameters[0].Rest)
}

// Finally is the runtime implementation used by the ordinary library callable.
func (c *Context) Finally(values []core.Value) Result {
	if c == nil {
		return failure(mem.System, "FEATURE_UNSUP", source.Span{}, "finally! requires an execution context")
	}
	defer discardCallValues(c.Run, values)
	p := c.Program
	if p == nil {
		return failure(c.Run, "FEATURE_UNSUP", c.Span, "finally! requires an execution program")
	}
	if c.forbidsFinalization() {
		return failure(c.Run, "PHASE_INVALID", c.Span, "finally! requires an execution context without deferred recipe effects")
	}
	if len(values) < 2 || len(values) > 3 {
		return arityFailure(c.Run, c.Span, "finally!", len(values), 2, 3)
	}
	if s := p.findFinalizer(c, c.Span); s != nil {
		return p.advanceFinalizer(s, c)
	}
	timeout := int64(5000)
	first := 0
	if len(values) == 3 {
		if values[0].Kind != core.Record {
			return failure(c.Run, "EXPR_INVALID", c.ArgumentSpan(0), "finally! options must be a record")
		}
		seen := false
		for i := range values[0].Record {
			f := values[0].Record[i]
			if f.Key != "timeout-ms" || seen {
				return failure(c.Run, "EXPR_INVALID", c.ArgumentSpan(0), "finally! accepts only one timeout-ms option")
			}
			if f.Value.Kind != core.Int || f.Value.Int < 1 || f.Value.Int > 60000 {
				return failure(c.Run, "EXPR_INVALID", c.ArgumentSpan(0), "timeout-ms must be an integer from 1 through 60000")
			}
			seen, timeout = true, f.Value.Int
		}
		first = 1
	}
	for i := first; i < len(values); i++ {
		if !acceptsNoArguments(p, values[i]) {
			return failure(c.Run, "EXPR_INVALID", c.ArgumentSpan(i), "finally! callbacks must accept zero arguments")
		}
	}
	if p.DryRun {
		c.deferred = true
		return Result{Completed: true}
	}
	if c.Engine == nil || p.FinalizationNow == nil || p.FinalizationQuiet == nil || p.DefinitionEffectSink == nil {
		return failure(c.Run, "FEATURE_UNSUP", c.Span, "finally! requires a graceful-finalization execution driver")
	}
	s := mem.Alloc[FinalizerState](p.Alloc)
	s.Program, s.Parent, s.NodeID, s.Generation = p, c.finalizer, c.Engine.NodeID(), c.Engine.Generation()
	s.Span, s.Source, s.Path = c.Span, owned(p.Alloc, c.Source), owned(p.Alloc, c.CallPath)
	s.TimeoutMS, s.Context = timeout, retainFinalizerContext(p.Alloc, c)
	s.Body, s.Cleanup = borrowValue(p.Alloc, values[first]), borrowValue(p.Alloc, values[first+1])
	p.finalizers = slices.Append(p.Alloc, p.finalizers, s)
	c.Engine.SetTeardown(beginFinalizerTeardown, advanceFinalizerTeardown, p)
	return p.advanceFinalizer(s, c)
}

func cloneFinalizerResult(a mem.Allocator, r *Result) Result {
	return Result{Value: borrowValue(a, r.Value), Diagnostic: r.Diagnostic.Clone(a), Completed: r.Completed}
}

func (p *Program) finalizerUnwind(s *FinalizerState) {
	outer := s
	for outer.Parent != nil {
		outer = outer.Parent
	}
	deadline := p.FinalizationNow(p.FinalizationHost) + outer.TimeoutMS*1000000
	for current := s; current != nil; current = current.Parent {
		if current.Deadline != 0 && current.Deadline < deadline {
			deadline = current.Deadline
		}
	}
	for current := s; current != nil; current = current.Parent {
		if current.UnwindDeadline == 0 || deadline < current.UnwindDeadline {
			current.UnwindDeadline = deadline
		}
	}
}

// A handled error did not exit these body scopes. A later, unrelated failure
// gets a fresh unwind deadline; an active enclosing cleanup deadline stays put.
func (c *Context) handledFinalizerFailure() {
	for s := c.finalizer; s != nil; s = s.Parent {
		if s.Stage == 0 {
			s.UnwindDeadline = 0
		}
	}
}

func (p *Program) finalizerTasks(s *FinalizerState, c *Context, cleanup bool, cancel bool) Result {
	for i := range p.Processes {
		task := p.Processes[i]
		if task.Finalizer != s || task.FinalizerCleanup != cleanup {
			continue
		}
		if cancel {
			c.Engine.ForgetDependency(task.Node.Key)
			if task.Root != nil {
				p.Engine.Release(task.Root)
				task.Root = nil
			}
			if task.LauncherRoot != nil {
				p.Engine.Release(task.LauncherRoot)
				task.LauncherRoot = nil
			}
		} else if !task.Observed {
			if !c.Engine.TryDependency(task.Node.Key) {
				return Result{Waiting: true}
			}
			if d := c.Engine.DependencyDiagnostic(task.Node.Key); d.Code != "" {
				return Result{Diagnostic: d.Clone(c.Run)}
			}
		}
		if !p.FinalizationQuiet(p.FinalizationHost, task.Node.ID, task.Node.Generation) {
			return Result{Waiting: true}
		}
	}
	return Result{}
}

func (p *Program) advanceFinalizer(s *FinalizerState, caller *Context) Result {
	if s.Stage == 4 {
		return cloneFinalizerResult(caller.Run, &s.Outcome)
	}
	c := &s.Context
	c.Resume(caller.Engine)
	c.finalizer, c.RecoverFailures = s, true
	if s.Stage >= 2 && s.Deadline != 0 && p.FinalizationNow(p.FinalizationHost) >= s.Deadline {
		s.Expired = true
	}
	if s.Expired {
		// An enclosing cleanup deadline also bounds the bodies of scopes entered
		// during cleanup. Settle every nested scope before releasing this one.
		for i := len(p.finalizers) - 1; i >= 0; i-- {
			child := p.finalizers[i]
			if child == s || child.Stage == 4 {
				continue
			}
			for parent := child.Parent; parent != nil; parent = parent.Parent {
				if parent != s {
					continue
				}
				child.Expired = true
				r := p.advanceFinalizer(child, caller)
				if r.Waiting {
					return r
				}
				finalizerFailure(p.Alloc, &s.Outcome.Diagnostic, &r.Diagnostic)
				r.Free(caller.Run)
				break
			}
		}
		if !s.TimeoutRecorded {
			p.Engine.Interrupt(p.Engine.LookupID(s.NodeID))
			d := failure(p.Alloc, "FINALIZE_TIMEOUT", s.Span, "cleanup exhausted its finalization deadline")
			attachSource(&d, c)
			finalizerFailure(p.Alloc, &s.Outcome.Diagnostic, &d.Diagnostic)
			d.Free(p.Alloc)
			s.TimeoutRecorded = true
		}
		r := p.finalizerTasks(s, c, false, true)
		waiting := r.Waiting
		r.Free(c.Run)
		if waiting {
			return Result{Waiting: true}
		}
		r = p.finalizerTasks(s, c, true, true)
		waiting = r.Waiting
		r.Free(c.Run)
		if waiting || !p.FinalizationQuiet(p.FinalizationHost, s.NodeID, s.Generation) {
			return Result{Waiting: true}
		}
		s.Stage = 4
		releaseFinalizerCallbacks(p.Alloc, s)
		return cloneFinalizerResult(caller.Run, &s.Outcome)
	}
	if s.Stage == 0 {
		r := c.CallAt(s.Body, nil, 0)
		if r.Waiting {
			p.finishFinalizerEffects(c, false)
			return r
		}
		if r.Stream != nil {
			r.Free(c.Run)
			r = failure(c.Run, "EXPR_INVALID", s.Span, "an open stream cannot escape finally!")
		}
		s.Outcome, s.Stage = r, 1
		if r.Diagnostic.Code != "" {
			p.finalizerUnwind(s)
		}
		p.finishFinalizerEffects(c, r.Diagnostic.Code == "")
	}
	if s.Stage == 1 {
		r := p.finalizerTasks(s, c, false, s.Outcome.Diagnostic.Code != "")
		if r.Waiting {
			return r
		}
		if r.Diagnostic.Code != "" {
			finalizerFailure(p.Alloc, &s.Outcome.Diagnostic, &r.Diagnostic)
			p.finalizerUnwind(s)
			r.Free(c.Run)
			return p.advanceFinalizer(s, caller)
		}
		r.Free(c.Run)
		if !p.FinalizationQuiet(p.FinalizationHost, s.NodeID, s.Generation) {
			return Result{Waiting: true}
		}
		s.Deadline = p.FinalizationNow(p.FinalizationHost) + s.TimeoutMS*1000000
		if s.Outcome.Diagnostic.Code != "" && s.UnwindDeadline != 0 && s.UnwindDeadline < s.Deadline {
			s.Deadline = s.UnwindDeadline
		}
		for parent := s.Parent; parent != nil; parent = parent.Parent {
			if parent.Stage >= 2 && parent.Deadline != 0 && parent.Deadline < s.Deadline {
				s.Deadline = parent.Deadline
			}
		}
		s.Stage = 2
	}
	if s.Stage == 2 {
		if p.FinalizationNow(p.FinalizationHost) >= s.Deadline {
			s.Expired = true
			return p.advanceFinalizer(s, caller)
		}
		c.TimeoutMS = (s.Deadline - p.FinalizationNow(p.FinalizationHost) + 999999) / 1000000
		r := c.CallAt(s.Cleanup, nil, 1)
		if r.Waiting {
			p.finishFinalizerEffects(c, false)
			return r
		}
		if p.FinalizationNow(p.FinalizationHost) >= s.Deadline {
			r.Free(c.Run)
			p.finishFinalizerEffects(c, false)
			s.Expired = true
			return p.advanceFinalizer(s, caller)
		}
		if r.Stream != nil {
			r.Free(c.Run)
			r = failure(c.Run, "EXPR_INVALID", s.Span, "cleanup cannot return an open stream")
		}
		p.finishFinalizerEffects(c, r.Diagnostic.Code == "")
		s.CleanupFailed = r.Diagnostic.Code != ""
		finalizerFailure(p.Alloc, &s.Outcome.Diagnostic, &r.Diagnostic)
		r.Free(c.Run)
		s.Stage = 3
	}
	if s.Stage == 3 {
		if p.FinalizationNow(p.FinalizationHost) >= s.Deadline {
			s.Expired = true
			return p.advanceFinalizer(s, caller)
		}
		r := p.finalizerTasks(s, c, true, s.CleanupFailed)
		if r.Waiting {
			return r
		}
		if r.Diagnostic.Code != "" {
			finalizerFailure(p.Alloc, &s.Outcome.Diagnostic, &r.Diagnostic)
			s.CleanupFailed = true
			r.Free(c.Run)
			return p.advanceFinalizer(s, caller)
		}
		r.Free(c.Run)
		s.Stage = 4
		releaseFinalizerCallbacks(p.Alloc, s)
	}
	return cloneFinalizerResult(caller.Run, &s.Outcome)
}

func (p *Program) finishFinalizerEffects(c *Context, success bool) {
	if success {
		deadline := p.FinalizerDeadline(c.Engine.NodeID())
		for i := range c.Effects {
			effect := c.Effects[i]
			effect.FinalizationDeadlineNS = deadline
			p.DefinitionEffectSink(p.DefinitionEffectState, effect)
		}
	}
	FreeEffects(c.Run, c.Effects)
	c.Effects = nil
	for i := range c.WritePaths {
		mem.FreeString(c.Run, c.WritePaths[i])
	}
	slices.Free(c.Run, c.WritePaths)
	c.WritePaths = nil
}

func beginFinalizerTeardown(value any, node int64, generation int64) bool {
	p := value.(*Program)
	found := false
	preserveRequest := false
	for i := len(p.finalizers) - 1; i >= 0; i-- {
		s := p.finalizers[i]
		if s.NodeID == node && s.Generation == generation && s.Stage != 4 {
			preserveRequest = s.Stage >= 2 && !s.Expired
			break
		}
	}
	for i := range p.finalizers {
		s := p.finalizers[i]
		if s.NodeID != node || s.Generation != generation || s.Stage == 4 {
			continue
		}
		found, s.Stopping = true, true
		p.finalizerUnwind(s)
		d := Result{}
		if p.finalizationCause.Code == "" {
			d = failure(p.Alloc, "EXEC_CANCELLED", s.Span, "execution cancelled")
		} else {
			d.Diagnostic = p.finalizationCause.Clone(p.Alloc)
		}
		finalizerFailure(p.Alloc, &s.Outcome.Diagnostic, &d.Diagnostic)
		cancelled := d.Diagnostic.Code == "EXEC_CANCELLED" || d.Diagnostic.Cancelled
		d.Free(p.Alloc)
		s.Outcome.Diagnostic.Protected = s.Outcome.Diagnostic.Protected || cancelled
		s.Outcome.Diagnostic.Cancelled = s.Outcome.Diagnostic.Cancelled || cancelled
		if s.Stage < 2 {
			s.Stage = 1
			p.finishFinalizerEffects(&s.Context, false)
		}
	}
	if found {
		n := p.Engine.LookupID(node)
		n.TeardownPreserveRequest = preserveRequest
	}
	return found
}

func advanceFinalizerTeardown(engine *core.EngineContext, node int64) core.ProducerResult {
	p := engine.TeardownState().(*Program)
	for i := len(p.finalizers) - 1; i >= 0; i-- {
		s := p.finalizers[i]
		if s.NodeID != node || s.Generation != engine.Generation() || !s.Stopping || s.Stage == 4 {
			continue
		}
		c := Context{Engine: engine, Run: p.Alloc}
		r := p.advanceFinalizer(s, &c)
		waiting := r.Waiting
		r.Free(p.Alloc)
		if waiting {
			if engine.Submitted() {
				return core.ProducerSubmitted
			}
			return core.ProducerActive
		}
		if s.Parent != nil {
			finalizerFailure(p.Alloc, &s.Parent.Outcome.Diagnostic, &s.Outcome.Diagnostic)
		}
	}
	for i := range p.finalizers {
		s := p.finalizers[i]
		if s.NodeID == node && s.Generation == engine.Generation() && s.Stopping && s.Parent == nil {
			engine.TeardownDiagnostic(s.Outcome.Diagnostic.Clone(p.Alloc))
			break
		}
	}
	return core.ProducerCompleted
}

// PollFinalizers services deadlines even when a host request cannot complete.
func (p *Program) PollFinalizers() {
	if p.DeferStopping {
		p.AdvanceFinalization()
	}
	if p.FinalizationNow == nil {
		return
	}
	now := p.FinalizationNow(p.FinalizationHost)
	for i := range p.finalizers {
		s := p.finalizers[i]
		if s.Stage >= 2 && s.Stage < 4 && s.Deadline != 0 && now >= s.Deadline && !s.Expired {
			s.Expired = true
			p.Engine.Interrupt(p.Engine.LookupID(s.NodeID))
		}
		if (s.Stage == 1 || s.Stage == 3 || s.Expired) && p.FinalizationQuiet(p.FinalizationHost, s.NodeID, s.Generation) {
			n := p.Engine.LookupID(s.NodeID)
			if n != nil && !n.Submitted && n.State == core.NodeWaiting {
				n.State = core.NodeReady
			}
		}
	}
}

func (p *Program) Finalizing() bool {
	for i := range p.finalizers {
		if p.finalizers[i].Stage != 4 {
			return true
		}
	}
	return p.defersPending()
}

func (p *Program) BeginFinalization() {
	if !p.DeferStopping {
		p.DeferStopping = true
		p.CancelProcesses()
	}
	for i := range p.finalizers {
		s := p.finalizers[i]
		if s.Stage != 4 {
			p.Engine.Teardown(p.Engine.LookupID(s.NodeID))
		}
	}
}

// BeginFinalizationWith preserves an invocation timeout or other established
// shutdown cause instead of relabelling it as cancellation. The cause is
// borrowed only during registration; every reached scope owns its copy.
func (p *Program) BeginFinalizationWith(cause diagnostic.Diagnostic) {
	p.finalizationCause = cause
	p.BeginFinalization()
	p.finalizationCause = diagnostic.Diagnostic{}
}

// FinalizerBudget identifies only work reached while cleanup is active. Hosts
// use it to shield those requests from the body's saved cancellation, never to
// grant authority or start unrelated work.
func (p *Program) FinalizerDeadline(node int64) int64 {
	deadline := int64(0)
	for i := range p.DeferScopes {
		s := p.DeferScopes[i]
		if s.Node == nil || s.Done || !s.ClockStarted {
			continue
		}
		owned := node == 0 || node == s.Node.ID
		for j := range s.Actions {
			action := s.Actions[j]
			if action.Node != nil && (action.Node.ID == node || p.ProcessInGroup(node, action.Context.ScriptGroup)) {
				owned = true
			}
		}
		limit := s.StartedAt + DeferTimeoutMS*1000000
		if owned && (deadline == 0 || limit < deadline) {
			deadline = limit
		}
	}
	for i := range p.finalizers {
		s := p.finalizers[i]
		if (node == 0 || s.NodeID == node) && s.Stage >= 2 && s.Stage < 4 && s.Deadline != 0 && (deadline == 0 || s.Deadline < deadline) {
			deadline = s.Deadline
		}
	}
	for i := range p.Processes {
		t := p.Processes[i]
		if t.Node.ID != node {
			continue
		}
		// Work started in the body of a scope nested inside cleanup still
		// inherits the enclosing cleanup deadline, even before its own cleanup.
		for s := t.Finalizer; s != nil; s = s.Parent {
			if s.Stage >= 2 && s.Deadline != 0 && (deadline == 0 || s.Deadline < deadline) {
				deadline = s.Deadline
			}
		}
	}
	return deadline
}

func (p *Program) FinalizerBudget(node int64) int64 {
	if p.FinalizationNow == nil {
		return 0
	}
	deadline := p.FinalizerDeadline(node)
	if deadline == 0 {
		return 0
	}
	remaining := deadline - p.FinalizationNow(p.FinalizationHost)
	if remaining <= 0 {
		return -1
	}
	return (remaining + 999999) / 1000000
}

// An output driver can observe a pending CLI signal while the evaluator is
// suspended inside a sink. Record its protected outcome without reentering the
// engine or running teardown from the signal handler.
func (p *Program) FinalizerOutputCancelled(deadline int64) {
	for i := range p.finalizers {
		s := p.finalizers[i]
		if s.Stage < 2 || s.Stage >= 4 || s.Deadline != deadline {
			continue
		}
		if !s.Outcome.Diagnostic.Cancelled && s.Outcome.Diagnostic.Code != "EXEC_CANCELLED" {
			d := failure(p.Alloc, "EXEC_CANCELLED", s.Span, "execution cancelled")
			finalizerFailure(p.Alloc, &s.Outcome.Diagnostic, &d.Diagnostic)
			d.Free(p.Alloc)
		}
		s.Outcome.Diagnostic.Protected, s.Outcome.Diagnostic.Cancelled = true, true
	}
}

func releaseFinalizerCallbacks(a mem.Allocator, s *FinalizerState) {
	freeCallables(a, &s.Body)
	s.Body.Free(a)
	freeCallables(a, &s.Cleanup)
	s.Cleanup.Free(a)
	freeFinalizerContext(a, &s.Context)
	s.Body, s.Cleanup = core.Value{}, core.Value{}
}

func freeFinalizer(a mem.Allocator, s *FinalizerState) {
	releaseFinalizerCallbacks(a, s)
	s.Outcome.Free(a)
	mem.FreeString(a, s.Source)
	mem.FreeString(a, s.Path)
	mem.Free(a, s)
}
