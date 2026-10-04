package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

// The invocation root owns work independently of whichever value demanded it.
type processTask struct {
	Program *Program
	Node *core.Node
	Root *core.Root
	LauncherRoot *core.Root
	Graph *expr.Expr
	Prepared *captureState
	Context Context
	Observed bool
}

func (p *Program) startProcess(e *expr.Expr, c *Context, prepared *captureState) Result {
	task := mem.Alloc[processTask](p.Alloc)
	task.Program, task.Graph = p, expr.Clone(p.Alloc, e)
	task.Graph.Async = false
	task.Context = Context{Program: p, Scope: c.Scope, Run: p.Alloc, Cwd: owned(p.Alloc, c.Cwd), Source: owned(p.Alloc, c.Source), HasArgs: c.HasArgs, HasEnvironment: c.HasEnvironment, Phase: c.Phase, DirectHostRequests: c.DirectHostRequests, DependencyObserver: c.DependencyObserver, ResolverState: c.ResolverState, ResolveDefinition: c.ResolveDefinition, ToolResolver: c.ToolResolver}
	task.Context.ScriptGroup, task.Context.TimeoutMS = c.ScriptGroup, c.TimeoutMS
	task.Context.RecipeNode = c.RecipeNode
	for i := range c.Environment { task.Context.Environment = slices.Append(p.Alloc, task.Context.Environment, owned(p.Alloc, c.Environment[i])) }
	c.Scope.Retain()
	for i := range c.Grants {
		grant := Grant{Capability: c.Grants[i].Capability}
		for j := range c.Grants[i].Names { grant.Names = slices.Append(p.Alloc, grant.Names, owned(p.Alloc, c.Grants[i].Names[j])) }
		task.Context.Grants = slices.Append(p.Alloc, task.Context.Grants, grant)
	}
	for i := range c.Args { task.Context.Args = slices.Append(p.Alloc, task.Context.Args, c.Args[i].Clone(p.Alloc)) }
	for i := range c.Inputs { task.Context.Inputs = slices.Append(p.Alloc, task.Context.Inputs, c.Inputs[i].Clone(p.Alloc)) }
	for i := range c.Outputs { task.Context.Outputs = slices.Append(p.Alloc, task.Context.Outputs, c.Outputs[i].Clone(p.Alloc)) }
	for i := range c.RuleFrames {
		frame := RuleFrame{FileRule: c.RuleFrames[i].FileRule}
		for j := range c.RuleFrames[i].Inputs { frame.Inputs = slices.Append(p.Alloc, frame.Inputs, c.RuleFrames[i].Inputs[j].Clone(p.Alloc)) }
        for j := range c.RuleFrames[i].NewerInputs { frame.NewerInputs = slices.Append(p.Alloc, frame.NewerInputs, c.RuleFrames[i].NewerInputs[j].Clone(p.Alloc)) }
		for j := range c.RuleFrames[i].Outputs { frame.Outputs = slices.Append(p.Alloc, frame.Outputs, c.RuleFrames[i].Outputs[j].Clone(p.Alloc)) }
		task.Context.RuleFrames = slices.Append(p.Alloc, task.Context.RuleFrames, frame)
	}
	task.Prepared = mem.Alloc[captureState](p.Alloc)
	s := task.Prepared
	s.Alloc, s.Stage, s.Input, s.Output, s.Append = p.Alloc, prepared.Stage, owned(p.Alloc, prepared.Input), owned(p.Alloc, prepared.Output), prepared.Append
	for i := range prepared.Stages { s.Stages = slices.Append(p.Alloc, s.Stages, prepared.Stages[i].Clone(p.Alloc)) }
	for i := range prepared.Setups { s.Setups = slices.Append(p.Alloc, s.Setups, prepared.Setups[i].Clone(p.Alloc)) }
	var buffer [strconv.MaxIntBase10Len]byte
	key := core.NewResourceKey(p.Alloc, core.ResourceDefinition, "<process:"+strconv.Itoa(buffer[:], len(p.Processes))+">")
	task.Node = p.Engine.Add(key, runProcessTask, task)
	key.Free(p.Alloc)
	if task.Node == nil { freeProcessTask(p.Alloc, task); return failure(c.Run, "HOST_FAIL", e.Span, "cannot schedule async process") }
	task.Root = p.Engine.RequestRoot(task.Node)
	task.LauncherRoot = c.Engine.RetainRoot()
	p.Processes = slices.Append(p.Alloc, p.Processes, task)
	// Queue startup before the caller can advance to its next statement.
	p.Engine.DispatchRoot(task.Node)
	return Result{Value: core.Value{Kind: core.Process, Process: task}}
}

func runProcessTask(engine *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	task := engine.Context().(*processTask)
	c := task.Context
	c.Engine, c.Run, c.Requests = engine, engine.Allocator(), task.Program.Requests
	if task.Prepared != nil {
		c.operationStart, c.operationEnd = task.Graph.Span.Start, task.Graph.Span.End
		c.SetOperationState(task.Prepared, freeCaptureState)
		task.Prepared = nil
	}
	r := task.Program.capture(task.Graph, &c)
	if !r.Waiting && r.Diagnostic.Code == "" && task.Program.DefinitionEffectSink != nil {
		for i := range c.Effects { task.Program.DefinitionEffectSink(task.Program.DefinitionEffectState, c.Effects[i]) }
	}
	FreeEffects(c.Run, c.Effects)
	for i := range c.WritePaths { mem.FreeString(c.Run, c.WritePaths[i]) }
	slices.Free(c.Run, c.WritePaths)
	if r.Waiting { r.Free(c.Run); if engine.Submitted() { return core.ProducerSubmitted }; return core.ProducerWaiting }
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
	if len(e.Items) != 2 { return failure(c.Run, "EXPR_INVALID", e.Span, "await requires one process handle") }
	if c.Phase == PlanningPhase || c.Phase == ResolvingPhase { return failure(c.Run, "PHASE_INVALID", e.Span, "await is invalid in this phase") }
	r := p.evaluate(c.Engine, c.Scope, e.Items[1], c)
	if r.Waiting || r.Diagnostic.Code != "" { return r }
	if r.Value.Kind != core.Process { r.Free(c.Run); return failure(c.Run, "EXPR_INVALID", e.Span, "await requires a process handle") }
	if p.DryRun && r.Value.Process == p { r.Free(c.Run); return Result{Value: core.Value{Kind: core.Nil}} }
	var task *processTask
	for i := range p.Processes { if r.Value.Process == p.Processes[i] { task = p.Processes[i]; break } }
	r.Free(c.Run)
	if task == nil { return failure(c.Run, "EXPR_INVALID", e.Span, "process handle belongs to another invocation") }
	if c.Engine == nil { return failure(c.Run, "HOST_FAIL", e.Span, "await requires an execution host") }
	if !c.Engine.TryDependency(task.Node.Key) { return Result{Waiting: true} }
	task.Observed = true
	if d := c.Engine.DependencyDiagnostic(task.Node.Key); d.Code != "" { return Result{Diagnostic: d.Clone(c.Run)} }
	current := c.Engine.Value(task.Node.Key)
	return Result{Value: current.Value.Clone(c.Run)}
}

func (p *Program) joinProcesses(c *Context) Result {
	for i := range p.Processes {
		task := p.Processes[i]
		if c.ScriptGroup != 0 && task.Context.ScriptGroup != c.ScriptGroup { continue }
		if task.Observed { continue }
		if !c.Engine.TryDependency(task.Node.Key) { return Result{Waiting: true} }
		if d := c.Engine.DependencyDiagnostic(task.Node.Key); d.Code != "" { return Result{Diagnostic: d.Clone(c.Run)} }
	}
	return Result{Value: core.Value{Kind: core.Nil}}
}

// Shutdown releases independent task and creator interest before the host reaps
// process groups. Handles remain opaque values until their program is freed.
func (p *Program) CancelProcesses() {
	if p == nil || p.Engine == nil { return }
	for i := range p.Processes {
		task := p.Processes[i]
		if task.Root != nil { p.Engine.Release(task.Root); task.Root = nil }
		if task.LauncherRoot != nil { p.Engine.Release(task.LauncherRoot); task.LauncherRoot = nil }
	}
}

// ProcessRecipeNode supplies build attribution for an independent async node.
func (p *Program) ProcessRecipeNode(node int64) int64 {
	for i := range p.Processes { if p.Processes[i].Node.ID == node { return p.Processes[i].Context.RecipeNode } }
	return node
}

func freeProcessTask(a mem.Allocator, task *processTask) {
	if task.Prepared != nil { freeCaptureState(a, task.Prepared) }
	task.Context.Scope.Free()
	expr.Free(a, task.Graph)
	mem.FreeString(a, task.Context.Cwd)
	mem.FreeString(a, task.Context.Source)
	for i := range task.Context.Environment { mem.FreeString(a, task.Context.Environment[i]) }
	slices.Free(a, task.Context.Environment)
	for i := range task.Context.Args { task.Context.Args[i].Free(a) }
	slices.Free(a, task.Context.Args)
	for i := range task.Context.Inputs { task.Context.Inputs[i].Free(a) }
	slices.Free(a, task.Context.Inputs)
	for i := range task.Context.Outputs { task.Context.Outputs[i].Free(a) }
	slices.Free(a, task.Context.Outputs)
	for i := range task.Context.RuleFrames {
		frame := task.Context.RuleFrames[i]
		for j := range frame.Inputs { frame.Inputs[j].Free(a) }
		for j := range frame.Outputs { frame.Outputs[j].Free(a) }
        for j := range frame.NewerInputs { frame.NewerInputs[j].Free(a) }
        slices.Free(a, frame.NewerInputs)
		slices.Free(a, frame.Inputs)
		slices.Free(a, frame.Outputs)
	}
	slices.Free(a, task.Context.RuleFrames)
	for i := range task.Context.Grants {
		for j := range task.Context.Grants[i].Names { mem.FreeString(a, task.Context.Grants[i].Names[j]) }
		slices.Free(a, task.Context.Grants[i].Names)
	}
	slices.Free(a, task.Context.Grants)
	mem.Free(a, task)
}
