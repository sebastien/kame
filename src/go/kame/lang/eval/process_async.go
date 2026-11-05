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
	Finalizer        *FinalizerState
	FinalizerCleanup bool
	Program          *Program
	Node             *core.Node
	Root             *core.Root
	LauncherRoot     *core.Root
	Graph            *expr.Expr
	Prepared         *captureState
	Context          Context
	Observed         bool
}

func (p *Program) startProcess(e *expr.Expr, c *Context, prepared *captureState) Result {
	task := mem.Alloc[processTask](p.Alloc)
	task.Program, task.Graph = p, expr.Clone(p.Alloc, e)
	task.Finalizer = c.finalizer
	task.FinalizerCleanup = c.finalizer != nil && c.finalizer.Stage >= 2
	task.Graph.Async = false
	task.Context = p.retainExecutionContext(c)
	task.Context.InputClosed = true
	task.Prepared = clonePreparedProcess(p.Alloc, prepared)
	var buffer [strconv.MaxIntBase10Len]byte
	key := core.NewResourceKey(p.Alloc, core.ResourceDefinition, "<process:"+strconv.Itoa(buffer[:], len(p.Processes))+">")
	task.Node = p.Engine.Add(key, runProcessTask, task)
	key.Free(p.Alloc)
	if task.Node == nil {
		freeProcessTask(p.Alloc, task)
		return failure(c.Run, "HOST_FAIL", e.Span, "cannot schedule async process")
	}
	task.Root = p.Engine.RequestRoot(task.Node)
	if c.finalizer == nil {
		task.LauncherRoot = c.Engine.RetainRoot()
	}
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
	slices.Free(c.Run, c.WritePaths)
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
	var task *processTask
	for i := range p.Processes {
		if r.Value.Process == p.Processes[i] {
			task = p.Processes[i]
			break
		}
	}
	r.Free(c.Run)
	if task == nil {
		return failure(c.Run, "EXPR_INVALID", e.Span, "process handle belongs to another invocation")
	}
	if c.Engine == nil {
		return failure(c.Run, "HOST_FAIL", e.Span, "await requires an execution host")
	}
	if !c.Engine.TryDependency(task.Node.Key) {
		return Result{Waiting: true}
	}
	task.Observed = true
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
		result.Environment = slices.Append(p.Alloc, result.Environment, owned(p.Alloc, c.Environment[i]))
	}
	for i := range c.Grants {
		grant := Grant{Capability: c.Grants[i].Capability}
		for j := range c.Grants[i].Names {
			grant.Names = slices.Append(p.Alloc, grant.Names, owned(p.Alloc, c.Grants[i].Names[j]))
		}
		result.Grants = slices.Append(p.Alloc, result.Grants, grant)
	}
	for i := range c.Args {
		result.Args = slices.Append(p.Alloc, result.Args, c.Args[i].Clone(p.Alloc))
	}
	for i := range c.Inputs {
		result.Inputs = slices.Append(p.Alloc, result.Inputs, c.Inputs[i].Clone(p.Alloc))
	}
	for i := range c.Outputs {
		result.Outputs = slices.Append(p.Alloc, result.Outputs, c.Outputs[i].Clone(p.Alloc))
	}
	for i := range c.RuleFrames {
		frame := RuleFrame{FileRule: c.RuleFrames[i].FileRule, ServiceRule: c.RuleFrames[i].ServiceRule}
		for j := range c.RuleFrames[i].Inputs {
			frame.Inputs = slices.Append(p.Alloc, frame.Inputs, c.RuleFrames[i].Inputs[j].Clone(p.Alloc))
		}
		for j := range c.RuleFrames[i].NewerInputs {
			frame.NewerInputs = slices.Append(p.Alloc, frame.NewerInputs, c.RuleFrames[i].NewerInputs[j].Clone(p.Alloc))
		}
		for j := range c.RuleFrames[i].Outputs {
			frame.Outputs = slices.Append(p.Alloc, frame.Outputs, c.RuleFrames[i].Outputs[j].Clone(p.Alloc))
		}
		result.RuleFrames = slices.Append(p.Alloc, result.RuleFrames, frame)
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
	slices.Free(a, context.Environment)
	for i := range context.Args {
		context.Args[i].Free(a)
	}
	slices.Free(a, context.Args)
	for i := range context.Inputs {
		context.Inputs[i].Free(a)
	}
	slices.Free(a, context.Inputs)
	for i := range context.Outputs {
		context.Outputs[i].Free(a)
	}
	slices.Free(a, context.Outputs)
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
		slices.Free(a, frame.NewerInputs)
		slices.Free(a, frame.Inputs)
		slices.Free(a, frame.Outputs)
	}
	slices.Free(a, context.RuleFrames)
	for i := range context.Grants {
		for j := range context.Grants[i].Names {
			mem.FreeString(a, context.Grants[i].Names[j])
		}
		slices.Free(a, context.Grants[i].Names)
	}
	slices.Free(a, context.Grants)
}

func clonePreparedProcess(a mem.Allocator, prepared *captureState) *captureState {
	s := mem.Alloc[captureState](a)
	s.Alloc, s.Stage, s.Input, s.Output, s.Append = a, prepared.Stage, owned(a, prepared.Input), owned(a, prepared.Output), prepared.Append
	for i := range prepared.Stages {
		s.Stages = slices.Append(a, s.Stages, prepared.Stages[i].Clone(a))
	}
	for i := range prepared.Setups {
		s.Setups = slices.Append(a, s.Setups, prepared.Setups[i].Clone(a))
	}
	return s
}
