package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"slices"

	"strconv"
)

// The invocation root owns work independently of whichever value demanded it.
type processTask struct {
	Finalizer        *FinalizerState
	FinalizerCleanup bool
	Program          *Program
	Node             *core.Node
	Root             *core.Root
	LauncherRoot     *core.Root
	LauncherID       int64 // node whose evaluation started the graph
	Graph            *expr.Expr
	Prepared         *captureState
	Context          Context
	Observed         bool
	Index            int
	Handle           *ProcessHandle
}

// ProcessHandle is an opaque invocation-owned handle. Pass its pointer, not a
// struct copy. Once execution is retired, only immutable result data remains;
// saved handles keep that data alive through Go's GC, not the execution graph.
type ProcessHandle struct {
	owner      *Program
	task       *processTask
	done       bool
	state      core.NodeState
	value      core.Value
	diagnostic diagnostic.Diagnostic
	observed   bool
}

func (h *ProcessHandle) Root() *core.Root {
	if h.task != nil {
		return h.task.Root
	}
	return nil
}

func (h *ProcessHandle) State() core.NodeState {
	if h.task != nil {
		return h.task.Node.State
	}
	return h.state
}

// Diagnostic is borrowed; clone it before retaining or modifying it.
func (h *ProcessHandle) Diagnostic() diagnostic.Diagnostic {
	if h.task != nil {
		return h.task.Node.Diagnostic
	}
	return h.diagnostic
}

func (h *ProcessHandle) Observed() bool { return h.observed }

func (task *processTask) observe() {
	task.Observed, task.Handle.observed = true, true
}

func (p *Program) startProcess(e *expr.Expr, c *Context, prepared *captureState) Result {
	task := mem.Alloc[processTask](p.Alloc)
	task.Program, task.Graph = p, expr.Clone(p.Alloc, e)
	if c.Engine != nil {
		task.LauncherID = c.Engine.NodeID()
	}
	task.Finalizer = c.finalizer
	task.FinalizerCleanup = c.finalizer != nil && c.finalizer.Stage >= 2
	task.Graph.Async = false
	task.Context = p.retainExecutionContext(c)
	task.Context.InputClosed = true
	task.Prepared = clonePreparedProcess(p.Alloc, prepared)

	task.Index = p.nextProcess
	key := core.NewResourceKey(p.Alloc, core.ResourceDefinition, "<process:"+strconv.Itoa(task.Index)+">")
	task.Node = p.Engine.Add(key, runProcessTask, task)
	key.Free(p.Alloc)
	if task.Node == nil {
		freeProcessTask(p.Alloc, task)
		return failure(c.Run, "HOST_FAIL", e.Span, "cannot schedule async process")
	}
	task.Root = p.Engine.RequestRoot(task.Node)
	task.Handle = &ProcessHandle{owner: p, task: task}
	if c.finalizer == nil {
		task.LauncherRoot = c.Engine.RetainRoot()
	}
	p.Processes = append(p.Processes, task)
	p.nextProcess++
	if p.ProcessStarted != nil {
		p.ProcessStarted(task.LauncherID, task.Handle)
	}
	// Queue startup before the caller can advance to its next statement.
	p.Engine.DispatchRoot(task.Node)
	return Result{Value: core.Value{Kind: core.Process, Process: task.Handle}}
}

func runProcessTask(engine *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	task := engine.Context().(*processTask)
	c := task.Context
	c.Engine, c.Run, c.Requests = engine, engine.Allocator(), task.Program.Requests
	c.Resume(engine)
	if task.Prepared != nil {
		c.operationStart, c.operationEnd = task.Graph.Span.Start, task.Graph.Span.End
		c.SetOperationState(task.Prepared, freeCaptureState)
		task.Prepared = nil
	}
	r := task.Program.capture(task.Graph, &c)
	if !r.Waiting && r.Diagnostic.Code == "" && task.Program.DefinitionEffectSink != nil {
		for i := range c.Effects {
			task.Program.DefinitionEffectSink(task.Program.DefinitionEffectState, c.Effects[i])
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
		attachSource(&r, &c)
		engine.Fail(r.Diagnostic)
		r.Diagnostic = diagnostic.Diagnostic{}
		r.Free(c.Run)
		return core.ProducerFailed
	}
	engine.Publish(r.Value)
	r.Value = core.Value{}
	r.Free(c.Run)
	return core.ProducerCompleted
}

func (p *Program) awaitProcess(e *expr.Expr, c *Context) Result {
	if len(e.Items) != 2 {
		return failure(c.Run, "EXPR_INVALID", e.Span, "await requires one process handle")
	}
	if c.Phase != EvaluatePhase {
		return failure(c.Run, "PHASE_INVALID", e.Span, "await is invalid in this phase")
	}
	r := p.evaluate(c.Engine, c.Scope, e.Items[1], c)
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	if r.Value.Kind != core.Process {
		r.Free(c.Run)
		return failure(c.Run, "EXPR_INVALID", e.Span, "await requires a process handle")
	}
	if p.DryRun && r.Value.Process == p {
		r.Free(c.Run)
		return Result{Value: core.Value{Kind: core.Nil}}
	}
	handle, ok := r.Value.Process.(*ProcessHandle)
	r.Free(c.Run)
	if !ok || handle == nil || handle.owner != p || (!handle.done && (handle.task == nil || handle.task.Handle != handle)) {
		return failure(c.Run, "EXPR_INVALID", e.Span, "process handle belongs to another invocation")
	}
	if c.Engine == nil {
		return failure(c.Run, "HOST_FAIL", e.Span, "await requires an execution host")
	}
	if handle.done {
		handle.observed = true
		if handle.diagnostic.Code != "" {
			return Result{Diagnostic: handle.diagnostic.Clone(c.Run)}
		}
		return Result{Value: handle.value.Clone(c.Run)}
	}
	task := handle.task
	if !c.Engine.TryDependency(task.Node.Key) {
		return Result{Waiting: true}
	}
	task.observe()
	if d := c.Engine.DependencyDiagnostic(task.Node.Key); d.Code != "" {
		return Result{Diagnostic: d.Clone(c.Run)}
	}
	current := c.Engine.Value(task.Node.Key)
	return Result{Value: current.Value.Clone(c.Run)}
}

func (p *Program) joinProcesses(c *Context) Result {
	for i := range p.Processes {
		task := p.Processes[i]
		if c.ScriptGroup != 0 && task.Context.ScriptGroup != c.ScriptGroup {
			continue
		}
		if task.Observed {
			continue
		}
		if !c.Engine.TryDependency(task.Node.Key) {
			return Result{Waiting: true}
		}
		task.observe()
		if d := c.Engine.DependencyDiagnostic(task.Node.Key); d.Code != "" {
			return Result{Diagnostic: d.Clone(c.Run)}
		}
	}
	return Result{Value: core.Value{Kind: core.Nil}}
}

// Shutdown releases independent task and creator interest before the host reaps
// process groups. Handles remain opaque values until their program is freed.
func (p *Program) CancelProcesses() {
	if p == nil || p.Engine == nil {
		return
	}
	for i := range p.Processes {
		task := p.Processes[i]
		if task.Context.Finalizing {
			continue
		}
		if task.FinalizerCleanup && task.Finalizer != nil && !task.Finalizer.Expired && task.Finalizer.Stage < 4 {
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

// ProcessRecipeNode supplies build attribution for an independent async node.
func (p *Program) ProcessRecipeNode(node int64) int64 {
	for i := range p.Processes {
		if p.Processes[i].Node.ID == node {
			return p.Processes[i].Context.RecipeNode
		}
	}
	return node
}

func freeProcessTask(a mem.Allocator, task *processTask) {
	if task.Prepared != nil {
		freeCaptureState(a, task.Prepared)
	}
	expr.Free(a, task.Graph)
	freeExecutionContext(a, &task.Context)
	mem.Free(a, task)
}

// Async work and deferred actions must retain the same authority and lexical frame.
func (p *Program) retainExecutionContext(c *Context) Context {
	result := *c
	result.Engine, result.Requests, result.Effects, result.WritePaths = nil, nil, nil, nil
	result.Run, result.PrepareDeferred = p.Alloc, nil
	result.Cwd, result.Source, result.CallPath = owned(p.Alloc, c.Cwd), owned(p.Alloc, c.Source), owned(p.Alloc, c.CallPath)
	result.Environment, result.Grants, result.Args, result.Inputs, result.Outputs, result.RuleFrames, result.Frames = nil, nil, nil, nil, nil, nil, nil
	c.Scope.Retain()
	for i := range c.Environment {
		result.Environment = append(result.Environment, owned(p.Alloc, c.Environment[i]))
	}
	for i := range c.Grants {
		grant := Grant{Capability: c.Grants[i].Capability}
		for j := range c.Grants[i].Names {
			grant.Names = append(grant.Names, owned(p.Alloc, c.Grants[i].Names[j]))
		}
		result.Grants = append(result.Grants, grant)
	}
	for i := range c.Args {
		result.Args = append(result.Args, c.Args[i].Clone(p.Alloc))
	}
	for i := range c.Inputs {
		result.Inputs = append(result.Inputs, c.Inputs[i].Clone(p.Alloc))
	}
	for i := range c.Outputs {
		result.Outputs = append(result.Outputs, c.Outputs[i].Clone(p.Alloc))
	}
	for i := range c.RuleFrames {
		frame := RuleFrame{FileRule: c.RuleFrames[i].FileRule, ServiceRule: c.RuleFrames[i].ServiceRule}
		for j := range c.RuleFrames[i].Inputs {
			frame.Inputs = append(frame.Inputs, c.RuleFrames[i].Inputs[j].Clone(p.Alloc))
		}
		for j := range c.RuleFrames[i].NewerInputs {
			frame.NewerInputs = append(frame.NewerInputs, c.RuleFrames[i].NewerInputs[j].Clone(p.Alloc))
		}
		for j := range c.RuleFrames[i].Outputs {
			frame.Outputs = append(frame.Outputs, c.RuleFrames[i].Outputs[j].Clone(p.Alloc))
		}
		result.RuleFrames = append(result.RuleFrames, frame)
	}
	return result
}

func freeExecutionContext(a mem.Allocator, context *Context) {
	context.Scope.Free()
	mem.FreeString(a, context.Cwd)
	mem.FreeString(a, context.Source)
	mem.FreeString(a, context.CallPath)
	for i := range context.Environment {
		mem.FreeString(a, context.Environment[i])
	}
	mem.FreeSlice(nil, context.Environment)
	for i := range context.Args {
		context.Args[i].Free(a)
	}
	mem.FreeSlice(nil, context.Args)
	for i := range context.Inputs {
		context.Inputs[i].Free(a)
	}
	mem.FreeSlice(nil, context.Inputs)
	for i := range context.Outputs {
		context.Outputs[i].Free(a)
	}
	mem.FreeSlice(nil, context.Outputs)
	for i := range context.RuleFrames {
		frame := context.RuleFrames[i]
		for j := range frame.Inputs {
			frame.Inputs[j].Free(a)
		}
		for j := range frame.Outputs {
			frame.Outputs[j].Free(a)
		}
		for j := range frame.NewerInputs {
			frame.NewerInputs[j].Free(a)
		}
		mem.FreeSlice(nil, frame.NewerInputs)
		mem.FreeSlice(nil, frame.Inputs)
		mem.FreeSlice(nil, frame.Outputs)
	}
	mem.FreeSlice(nil, context.RuleFrames)
	for i := range context.Grants {
		for j := range context.Grants[i].Names {
			mem.FreeString(a, context.Grants[i].Names[j])
		}
		mem.FreeSlice(nil, context.Grants[i].Names)
	}
	mem.FreeSlice(nil, context.Grants)
}

func clonePreparedProcess(a mem.Allocator, prepared *captureState) *captureState {
	s := mem.Alloc[captureState](a)
	s.Alloc, s.Stage, s.Input, s.Output, s.Append = a, prepared.Stage, owned(a, prepared.Input), owned(a, prepared.Output), prepared.Append
	for i := range prepared.Stages {
		s.Stages = append(s.Stages, prepared.Stages[i].Clone(a))
	}
	for i := range prepared.Setups {
		s.Setups = append(s.Setups, prepared.Setups[i].Clone(a))
	}
	return s
}

// AsyncProcessCount is the next stable ID, not the number of live tasks.
// AsyncProcess exposes a launcher's ID and root, nil after retirement.
// ProcessStarted avoids rescanning historical IDs for interactive job control.
func (p *Program) AsyncProcessCount() int { return p.nextProcess }

func (p *Program) asyncProcess(i int) *processTask {
	for _, task := range p.Processes {
		if task.Index == i {
			return task
		}
	}
	return nil
}

func (p *Program) AsyncProcess(i int) (int64, *core.Root) {
	if task := p.asyncProcess(i); task != nil {
		return task.LauncherID, task.Root
	}
	return 0, nil
}

// ObserveAsyncProcess marks async graph i as observed by its job's report,
// so the session join does not raise its outcome again (052).
func (p *Program) ObserveAsyncProcess(i int) {
	if task := p.asyncProcess(i); task != nil {
		task.observe()
	}
}

func (p *Program) ObserveProcess(handle *ProcessHandle) {
	if handle != nil && handle.owner == p {
		handle.observed = true
		if handle.task != nil && handle.task.Handle == handle {
			handle.task.observe()
		}
	}
}

// CancelAsyncProcess discards async graph i, cancelling it if still running,
// as an owning lifetime that does not join it.
func (p *Program) CancelAsyncProcess(i int) {
	if task := p.asyncProcess(i); task != nil {
		p.CancelProcess(task.Handle)
	}
}

func (p *Program) CancelProcess(handle *ProcessHandle) {
	if handle == nil || handle.owner != p || handle.task == nil || handle.task.Handle != handle {
		return
	}
	task := handle.task
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

// RetireProcesses runs only at a driver boundary, after host events have been
// drained. The quiet callback must acknowledge physical reaping, not just
// cancellation. Unreported failures remain owned by the invocation join.
func (p *Program) RetireProcesses(quiet func(any, int64, int64) bool, state any) {
	if quiet == nil {
		return
	}
	for {
		removed := false
		for i := len(p.Processes) - 1; i >= 0; i-- {
			task := p.Processes[i]
			node := task.Node
			if node.State != core.NodeComplete && node.State != core.NodeFailed && node.State != core.NodeCancelled {
				continue
			}
			if !task.Observed && node.State != core.NodeComplete {
				continue
			}
			if !quiet(state, node.ID, node.Generation) {
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
			if node.Interest != 0 || len(node.Dependents) != 0 || len(node.Subs) != 0 {
				continue
			}
			value := node.Latest.CloneOwned(mem.System)
			d := node.Diagnostic.Clone(mem.System)
			id, status := node.ID, node.State
			if !p.Engine.Retire(node) {
				value.Free(mem.System)
				d.Free(mem.System)
				continue
			}
			handle := task.Handle
			handle.task, handle.done, handle.state = nil, true, status
			handle.value, handle.diagnostic = value, d
			p.retireProgress(id, "", nil)
			freeProcessTask(p.Alloc, task)
			p.Processes = slices.Delete(p.Processes, i, i+1)
			removed = true
		}
		for i := len(p.releasedStatements) - 1; i >= 0; i-- {
			node := p.releasedStatements[i]
			if quiet(state, node.ID, node.Generation) && p.retireStatement(node) {
				p.releasedStatements = slices.Delete(p.releasedStatements, i, i+1)
				removed = true
			}
		}
		if !removed {
			return
		}
	}
}
