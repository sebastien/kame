package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host"
	"littlemake/host/posix"
	"littlemake/lang/eval"
	"littlemake/lang/rule"
	"littlemake/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/time"
)

type Result struct {
	Path       string
	Value      core.Value
	Fresh      bool
	Diagnostic diagnostic.Diagnostic
}

func (r *Result) Free(a mem.Allocator) { if r.Path != "" { mem.FreeString(a, r.Path) }; r.Value.Free(a); r.Diagnostic.Free(a); *r = Result{} }

type instanceState struct { Program *Program; Index int }
type instanceResult struct { Node *core.Node; Plan Plan; Diagnostic diagnostic.Diagnostic }
type renderResult struct { Commands string; Effects []eval.Effect; WritePaths []string; LineSpans []diagnostic.Span; Waiting bool; Diagnostic diagnostic.Diagnostic }
type inputsResult struct { Inputs []string; ResourceInputs []PlanInput; Owned bool; Waiting bool; Diagnostic diagnostic.Diagnostic }
type renderDependencyState struct { Program *Program; Index int }
type externalFileState struct { Program *Program; Name string }
type externalValueState struct { Program *Program; Name string; Kind core.ResourceKind }

func freeInstanceState(a mem.Allocator, value any) { mem.Free(a, value.(*instanceState)) }
func freeExternalFileState(a mem.Allocator, value any) { state := value.(*externalFileState); if state.Name != "" { mem.FreeString(a, state.Name) }; mem.Free(a, state) }
func freeExternalValueState(a mem.Allocator, value any) { state := value.(*externalValueState); if state.Name != "" { mem.FreeString(a, state.Name) }; mem.Free(a, state) }

func produceExternalFile(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*externalFileState)
	name := state.Program.canonicalTarget(state.Name, true)
	_, err := os.Stat(name)
	mem.FreeString(state.Program.Alloc, name)
	// A missing path is an observed dependency, not a failed producer. Declared
	// inputs still fail before execution. The cache records an explicit missing
	// marker and invalidates when the path later appears.
	if err == os.ErrNotExist { c.Publish(core.Value{Kind: core.Nil}); return core.ProducerCompleted }
	if err != nil { c.Fail(failure(state.Program.Alloc, "TGT_NO_RULE", "required input does not exist: "+state.Name)); return core.ProducerFailed }
	c.Publish(core.NewString(c.Allocator(), state.Name))
	return core.ProducerCompleted
}
func produceExternalValue(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*externalValueState)
	if state.Kind == core.ResourceEnvironment { value, ok := state.Program.configuredEnvironment(state.Name); if ok { c.Publish(core.NewString(c.Allocator(), value)) } else { c.Publish(core.Value{Kind: core.Nil}) }
	} else if state.Kind == core.ResourceGlob { c.Publish(state.Program.wildcard(state.Name))
	} else { c.Fail(failure(state.Program.Alloc, "HOST_FAIL", "invalid external resource")); return core.ProducerFailed }
	return core.ProducerCompleted
}

func (p *Program) Materialize(target string) Result {
	started := p.Start(target)
	handle, d := started.Handle, started.Diagnostic
	if d.Code != "" {
		if d.Code == "TGT_NO_RULE" && isPathTarget(target) {
			name := p.canonicalTarget(target, true)
			_, err := os.Stat(name)
			mem.FreeString(p.Alloc, name)
			if err == nil { d.Free(p.Alloc); return Result{Path: cloneText(p.Alloc, target), Fresh: true} }
		}
		return Result{Diagnostic: d}
	}
	defer handle.Free()
	for {
		p.Tick(10)
		result := handle.Poll()
		if result.Done { return result.Result }
	}
}

func (p *Program) Start(target string) HandleStart {
	resolved := p.instanceFor(target)
	node, plan, d := resolved.Node, resolved.Plan, resolved.Diagnostic
	if d.Code != "" { return HandleStart{Diagnostic: d} }
	definition := node == nil
	if definition { node = p.Eval.Definition(target); if node == nil { plan.Free(p.Alloc); return HandleStart{Diagnostic: failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+target)} }
	}
	plan.Free(p.Alloc)
	index := p.instanceIndex(node)
	if index >= 0 {
		p.epoch++
		p.Instances[index].runEpoch = p.epoch
		p.invalidateStaleBareTasks(index, p.epoch)
	}
	if index >= 0 && taskTerminal(node.State) {
		entry := &p.Instances[index]
		if entry.Rule.Kind == rule.FileRule && node.State == core.NodeComplete { entry.Plan.Freshness = p.freshness(&entry.Plan, node); if p.Options.Force || entry.Plan.Freshness == Stale { p.Engine.Invalidate(node) }
		} else if entry.Rule.Kind == rule.TaskRule || entry.Rule.Kind == rule.CachedTaskRule { p.Engine.Invalidate(node) }
	}
	handle := mem.Alloc[Handle](p.Alloc)
	handle.Program, handle.Root, handle.Node, handle.Target, handle.Definition = p, p.Engine.RequestRoot(node), node, cloneText(p.Alloc, target), definition
	return HandleStart{Handle: handle}
}

// invalidateStaleBareTasks reruns stale terminal bare tasks once for this root epoch.
// It walks instance indexes and stored plan inputs only. Calling instanceFor here
// would allocate and transfer Plans during the walk.
func (p *Program) invalidateStaleBareTasks(root int, epoch int64) {
	closure := p.taskClosure(root)
	for i := len(closure) - 1; i >= 0; i-- {
		p.claimStaleTask(p.Instances[closure[i]].Node, epoch)
	}
	slices.Free(p.Alloc, closure)
}

func taskTerminal(state core.NodeState) bool {
	return state == core.NodeComplete || state == core.NodeFailed || state == core.NodeCancelled
}

// claimStaleTask invalidates one terminal task whose last claim is older than epoch.
// The stamp is set before Invalidate so a later parent in this epoch, or a resumed
// producer, does not invalidate the same generation again.
func (p *Program) claimStaleTask(node *core.Node, epoch int64) {
	if node == nil || epoch == 0 { return }
	index := p.instanceIndex(node)
	if index < 0 || p.Instances[index].Rule == nil { return }
	kind := p.Instances[index].Rule.Kind
	state := p.Instances[index].Node.State
	if !taskTerminal(state) || p.Instances[index].Node.Interest > 0 || p.Instances[index].satisfiedEpoch >= epoch { return }
	if kind == rule.CachedTaskRule {
		if state == core.NodeComplete && !p.cacheBlockedByBareTask(&p.Instances[index]) { return }
	} else if kind != rule.TaskRule {
		return
	}
	p.Instances[index].satisfiedEpoch = epoch
	p.Instances[index].runEpoch = epoch
	p.Engine.Invalidate(p.Instances[index].Node)
}

func (p *Program) taskClosure(root int) []int {
	var seen []int
	seen = slices.Append(p.Alloc, seen, root)
	i := 0
	for i < len(seen) {
		p.appendTaskClosure(seen[i], &seen)
		i++
	}
	return seen
}

func (p *Program) appendTaskClosure(index int, seen *[]int) {
	if index < 0 || index >= len(p.Instances) { return }
	inputs := p.Instances[index].Plan.Inputs
	if p.Instances[index].Plan.Resolved { inputs = p.Instances[index].Plan.ResolvedInputs }
	for n := range inputs {
		dep := p.instanceByTarget(inputs[n])
		if dep >= 0 && !slices.Contains(*seen, dep) { *seen = slices.Append(p.Alloc, *seen, dep) }
	}
	node := p.Instances[index].Node
	if node == nil { return }
	for n := range node.Dynamic {
		dep := p.instanceIndex(node.Dynamic[n])
		if dep >= 0 && !slices.Contains(*seen, dep) { *seen = slices.Append(p.Alloc, *seen, dep) }
	}
	for n := range node.Static {
		dep := p.instanceIndex(node.Static[n])
		if dep >= 0 && !slices.Contains(*seen, dep) { *seen = slices.Append(p.Alloc, *seen, dep) }
	}
}

func (p *Program) instanceByTarget(name string) int {
	for i := range p.Instances {
		if p.Instances[i].Plan.Target == name { return i }
	}
	return -1
}

func (p *Program) Tick(wait int) {
	p.pump(wait)
	p.drainRequests()
	p.Engine.DrainCompletions()
	jobs := p.Options.Jobs
	if jobs <= 0 { jobs = 1 }
	ready := p.Engine.Ready(jobs)
	for i := range ready { p.Engine.Dispatch(ready[i]) }
	if len(ready) != 0 { slices.Free(p.Alloc, ready) }
	p.observeInstances()
}

func (h *Handle) Cancel() { if h == nil || h.Program == nil || h.Root == nil { return }; h.Program.Engine.Release(h.Root); h.Root = nil; h.Program.drainCancellations() }

func (h *Handle) Poll() HandleResult {
	if h == nil || h.Program == nil || h.Node == nil { return HandleResult{Done: true, Result: Result{Diagnostic: failure(mem.System, "TGT_NO_RULE", "invalid handle")}} }
	if h.Node.State != core.NodeComplete && h.Node.State != core.NodeFailed && h.Node.State != core.NodeCancelled { return HandleResult{} }
	if h.Root != nil { h.Program.Engine.Release(h.Root); h.Root = nil }
	if h.Node.State != core.NodeComplete { return HandleResult{Done: true, Result: Result{Diagnostic: h.Node.Diagnostic.Clone(h.Program.Alloc)}} }
	if h.Definition { return HandleResult{Done: true, Result: Result{Value: h.Node.Latest.Clone(h.Program.Alloc)}} }
	result := Result{Fresh: h.Program.Instances[h.Program.instanceIndex(h.Node)].Plan.Freshness == Fresh}
	if h.Program.Instances[h.Program.instanceIndex(h.Node)].Rule.Kind == rule.FileRule { result.Path = cloneText(h.Program.Alloc, h.Target) }
	return HandleResult{Done: true, Result: result}
}

func (p *Program) instanceIndex(node *core.Node) int { for i := range p.Instances { if p.Instances[i].Node == node { return i } }; return -1 }
func isPathTarget(target string) bool { return isPath(target) || hasSlash(target) }

func (p *Program) instanceFor(target string) instanceResult {
	planned := p.Plan(target)
	plan, d := planned.Plan, planned.Diagnostic
	if d.Code != "" { return instanceResult{Diagnostic: d} }
	if plan.Rule == nil { return instanceResult{Plan: plan} }
	for i := range p.Instances {
		entry := &p.Instances[i]
		if entry.Rule == plan.Rule && sameCaptures(entry.Captures, plan.Captures) { return instanceResult{Node: entry.Node, Plan: plan} }
	}
	state := mem.Alloc[instanceState](p.Alloc)
	state.Program, state.Index = p, len(p.Instances)
	node := p.Engine.AddOwned(plan.Key, produce, state, freeInstanceState)
	if node == nil { plan.Free(p.Alloc); return instanceResult{Diagnostic: failure(p.Alloc, "TGT_AMBIG", "duplicate rule instance")} }
	p.Instances = slices.Append(p.Alloc, p.Instances, instance{Rule: plan.Rule, Captures: cloneCaptures(p.Alloc, plan.Captures), Node: node, Plan: plan})
	return instanceResult{Node: node}
}

func sameCaptures(left, right []template.CaptureValue) bool {
	if len(left) != len(right) { return false }
	for i := range left { if left[i].Name != right[i].Name || left[i].Text != right[i].Text { return false } }
	return true
}

func produce(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*instanceState)
	p := state.Program
	entry := &p.Instances[state.Index]
	if !entry.started || entry.startedGeneration != c.Generation() {
		if len(entry.CacheStdout) != 0 { slices.Free(p.Alloc, entry.CacheStdout) }; if len(entry.CacheStderr) != 0 { slices.Free(p.Alloc, entry.CacheStderr) }
		entry.CacheStdout, entry.CacheStderr, entry.CacheStdoutTruncated, entry.CacheStderrTruncated, entry.CacheReady = nil, nil, false, false, false
		if entry.Script != "" { mem.FreeString(p.Alloc, entry.Script); entry.Script = "" }
		freeStrings(p.Alloc, entry.Operations); entry.Operations = nil
		if entry.runEpoch != 0 && entry.satisfiedEpoch < entry.runEpoch { entry.satisfiedEpoch = entry.runEpoch }
		p.emitNode(entry.Node, entry.Plan.Target, TargetStarted, diagnostic.Span{}, nil)
		entry.started, entry.startedGeneration, entry.terminalEmitted = true, c.Generation(), false
	}
	if entry.Rule.Kind == rule.ServiceRule { c.Fail(failure(p.Alloc, "FEATURE_UNSUP", "service execution is not supported")); return core.ProducerFailed }
	if c.Completion().RequestID != 0 && entry.Script != "" {
		if entry.Script != "" { mem.FreeString(p.Alloc, entry.Script) }; entry.Script = ""
		if c.Completion().Diagnostic.Code != "" { c.Fail(c.Completion().Diagnostic); return core.ProducerFailed }
		if entry.Rule.Kind == rule.FileRule {
			for i := range entry.Plan.Outputs {
				name := p.canonicalTarget(entry.Plan.Outputs[i], true)
				_, err := os.Stat(name)
				mem.FreeString(p.Alloc, name)
				if err != nil { c.Fail(failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i])); return core.ProducerFailed }
			}
			c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0]))
		} else { c.Publish(core.Value{Kind: core.Nil}) }
		return core.ProducerCompleted
	}
	resolvedInputs := p.resolveInputs(c, entry)
	inputs, resourceInputs := resolvedInputs.Inputs, resolvedInputs.ResourceInputs
	defer freeOwnedStrings(p.Alloc, inputs, resolvedInputs.Owned)
	defer freePlanInputs(p.Alloc, resourceInputs, resolvedInputs.Owned)
	if resolvedInputs.Waiting { return core.ProducerWaiting }
	if resolvedInputs.Diagnostic.Code != "" { c.Fail(resolvedInputs.Diagnostic); return core.ProducerFailed }
	for i := range inputs {
		input := inputs[i]
		kind := core.ResourceTarget
		if i < len(resourceInputs) { kind = resourceInputs[i].Key.Kind }
		if kind == core.ResourceFile || (kind == core.ResourceTarget && isFileName(input)) {
			resolved := p.instanceFor(input)
			entry = &p.Instances[state.Index]
			resolved.Diagnostic.Free(p.Alloc)
			if resolved.Node != nil {
				resolved.Plan.Free(p.Alloc)
				if !p.prepareDependency(c, state.Index, resolved.Node) { return core.ProducerWaiting }
				continue
			}
			resolved.Plan.Free(p.Alloc)
			name := p.canonicalTarget(input, true)
			_, err := os.Stat(name)
			mem.FreeString(p.Alloc, name)
			if err != nil { c.Fail(failure(p.Alloc, "TGT_NO_RULE", "required input does not exist: "+input)); return core.ProducerFailed }
			continue
		}
		resolved := p.instanceFor(input)
		entry = &p.Instances[state.Index]
		dep, d := resolved.Node, resolved.Diagnostic
		resolved.Plan.Free(p.Alloc)
		if d.Code == "" && dep != nil {
			if !p.prepareDependency(c, state.Index, dep) { return core.ProducerWaiting }
			continue
		}
		d.Free(p.Alloc)
		definition := p.Eval.Definition(input)
		if definition == nil { c.Fail(failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+input)); return core.ProducerFailed }
		definitionNode := p.definitionNode(definition.Key.Name)
		if definitionNode == nil || !p.addDependency(c, entry, definitionNode) { return core.ProducerWaiting }
	}
	entry = &p.Instances[state.Index]
	rendered := p.render(c, entry, inputs)
	commands, effects, writePaths, d := rendered.Commands, rendered.Effects, rendered.WritePaths, rendered.Diagnostic
	if len(entry.LineSpans) != 0 { slices.Free(p.Alloc, entry.LineSpans) }
	entry.LineSpans = rendered.LineSpans
	defer eval.FreeEffects(p.Alloc, effects)
	defer freeStrings(p.Alloc, writePaths)
	if rendered.Waiting { return core.ProducerWaiting }
	if d.Code != "" { c.Fail(d); return core.ProducerFailed }
	if entry.Rule.Kind == rule.FileRule { entry.Plan.Freshness = p.freshness(&entry.Plan, entry.Node) } else { entry.Plan.Freshness = Stale }
	if entry.Rule.Kind == rule.CachedTaskRule && !p.Options.CacheDisabled && !p.Options.Force && !p.Options.DryRun {
		entry.CacheReady = p.cacheFingerprint(entry, commands)
		if !p.cacheBlockedByBareTask(entry) {
			record := p.cacheLoad(entry, entry.CacheFingerprint[:])
			if record.Identity != "" {
				entry.Plan.Freshness = Fresh
				p.emitCachedLog(entry, Stdout, record.Stdout, record.StdoutTruncated)
				p.emitCachedLog(entry, Stderr, record.Stderr, record.StderrTruncated)
				record.Free(p.Alloc)
				if commands != "" { mem.FreeString(p.Alloc, commands) }
				c.Publish(core.Value{Kind: core.Nil})
				return core.ProducerCompleted
			}
		}
	}
	if entry.Plan.Freshness == Fresh && !p.Options.Force && !p.Options.DryRun {
		if commands != "" { mem.FreeString(p.Alloc, commands) }
		if entry.Rule.Kind == rule.FileRule { c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0])) } else { c.Publish(core.Value{Kind: core.Nil}) }
		return core.ProducerCompleted
	}
	if hasYield(effects) && commands != "" { if commands != "" { mem.FreeString(p.Alloc, commands) }; c.Fail(failureAt(p.Alloc, "OUTPUT_CONFLICT", yieldSpan(effects), "yield cannot be combined with shell commands")); return core.ProducerFailed }
	if effectDiagnostic := validateEffects(p.Alloc, entry, effects); effectDiagnostic.Code != "" { if commands != "" { mem.FreeString(p.Alloc, commands) }; c.Fail(effectDiagnostic); return core.ProducerFailed }
	if p.Options.DryRun { p.commitEffects(entry, effects, writePaths, true); if commands != "" { mem.FreeString(p.Alloc, commands) }; c.Publish(core.Value{Kind: core.Nil}); return core.ProducerCompleted }
	if effectDiagnostic := p.commitEffects(entry, effects, writePaths, false); effectDiagnostic.Code != "" { if commands != "" { mem.FreeString(p.Alloc, commands) }; c.Fail(effectDiagnostic); return core.ProducerFailed }
	if commands == "" {
		if entry.Rule.Kind == rule.FileRule && !hasYield(effects) {
			for i := range entry.Plan.Outputs {
				name := p.canonicalTarget(entry.Plan.Outputs[i], true)
				_, err := os.Stat(name)
				mem.FreeString(p.Alloc, name)
				if err != nil { c.Fail(failure(p.Alloc, "OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i])); return core.ProducerFailed }
			}
		}
		if entry.Rule.Kind == rule.CachedTaskRule && entry.CacheReady && !p.Options.CacheDisabled { p.cacheCommit(entry, nil, nil, false, false) }
		if entry.Rule.Kind == rule.FileRule && hasYield(effects) { c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0])) } else { c.Publish(core.Value{Kind: core.Nil}) }
		return core.ProducerCompleted
	}
	if entry.Rule.Kind == rule.FileRule {
		for i := range entry.Plan.Outputs {
			name := p.canonicalTarget(entry.Plan.Outputs[i], true)
			ok := mkdirParent(name)
			mem.FreeString(p.Alloc, name)
			if !ok { c.Fail(failure(p.Alloc, "FS_ERR", "cannot create output directory")); return core.ProducerFailed }
		}
	}
	p.nextRequest++
	entry.Script = commands
	retain := p.Options.RetainBytes
	if entry.Rule.Kind == rule.CachedTaskRule && p.Options.CacheRetainBytes > retain { retain = p.Options.CacheRetainBytes }
	entry.cacheStartedAt = time.Now().UnixNano()
	entry.retryCount = 0
	request := posix.Request{ID: p.nextRequest, Shell: p.Options.Shell, Script: []byte(entry.Script), Directory: p.Options.Directory, Environment: p.Options.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}
	if !p.Host.Start(request) { c.Fail(failure(p.Alloc, "HOST_FAIL", "cannot start recipe")); return core.ProducerFailed }
	c.Submit(request.ID)
	return core.ProducerSubmitted
}


func (p *Program) render(c *core.EngineContext, entry *instance, names []string) renderResult {
	inputs := makeValues(p.Alloc, names)
	outputs := makeValues(p.Alloc, entry.Plan.Outputs)
	defer freeValues(p.Alloc, inputs); defer freeValues(p.Alloc, outputs)
	dependencyState := renderDependencyState{Program: p, Index: p.instanceIndex(entry.Node)}
	context := mem.Alloc[eval.Context](p.Alloc)
	*context = eval.Context{Program: p.Eval, Engine: c, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Phase: eval.RenderingPhase, ResolverState: &dependencyState, DependencyObserver: observeRenderDependency, OperationObserver: observeRenderOperation, RuleFrames: []eval.RuleFrame{{Inputs: inputs, Outputs: outputs}}}
	b := strings.NewBuilder(p.Alloc)
	defer b.Free()
	var spans []diagnostic.Span
	for i := range entry.Rule.Body {
		result := p.Eval.Render(p.Alloc, entry.Rule.Body[i].Template, p.Eval.Scope, context)
		if result.Waiting { if len(spans) != 0 { slices.Free(p.Alloc, spans) }; eval.FreeEffects(p.Alloc, context.Effects); freeStrings(p.Alloc, context.WritePaths); mem.Free(p.Alloc, context); return renderResult{Waiting: true} }
		if result.Diagnostic.Code != "" { if len(spans) != 0 { slices.Free(p.Alloc, spans) }; eval.FreeEffects(p.Alloc, context.Effects); freeStrings(p.Alloc, context.WritePaths); mem.Free(p.Alloc, context); return renderResult{Diagnostic: result.Diagnostic} }
		if result.Value.Kind != core.String { result.Value.Free(p.Alloc); if len(spans) != 0 { slices.Free(p.Alloc, spans) }; eval.FreeEffects(p.Alloc, context.Effects); freeStrings(p.Alloc, context.WritePaths); mem.Free(p.Alloc, context); return renderResult{Diagnostic: failure(p.Alloc, "EXPR_INVALID", "recipe line is not text")} }
		if result.Value.Text != "" { if b.Len() != 0 { b.WriteByte('\n') }; b.WriteString(result.Value.Text); spans = slices.Append(p.Alloc, spans, diagnostic.Span{Start: entry.Rule.Body[i].Span.Start, End: entry.Rule.Body[i].Span.End}) }
		result.Value.Free(p.Alloc)
	}
	var effects []eval.Effect
	for i := range context.Effects { effects = slices.Append(p.Alloc, effects, eval.Effect{Kind: context.Effects[i].Kind, Data: slices.Clone(p.Alloc, context.Effects[i].Data), Span: context.Effects[i].Span}) }
	eval.FreeEffects(p.Alloc, context.Effects)
	var writePaths []string
	for i := range context.WritePaths { writePaths = slices.Append(p.Alloc, writePaths, cloneText(p.Alloc, context.WritePaths[i])); mem.FreeString(p.Alloc, context.WritePaths[i]) }
	slices.Free(p.Alloc, context.WritePaths)
	result := renderResult{Commands: cloneText(p.Alloc, b.String()), Effects: effects, WritePaths: writePaths, LineSpans: spans}
	mem.Free(p.Alloc, context)
	return result
}

func observeRenderDependency(value any, key core.ResourceKey) {
	state := value.(*renderDependencyState)
	if key.Name == "" { return }
	p := state.Program
	if key.Kind == core.ResourceEnvironment || key.Kind == core.ResourceGlob {
		state := mem.Alloc[externalValueState](p.Alloc)
		state.Program, state.Name, state.Kind = p, cloneText(p.Alloc, key.Name), key.Kind
		if p.Engine.AddOwned(key, produceExternalValue, state, freeExternalValueState) == nil { freeExternalValueState(p.Alloc, state) }
		return
	}
	resolved := p.instanceFor(key.Name)
	if resolved.Diagnostic.Code == "" || (resolved.Diagnostic.Code == "TGT_NO_RULE" && key.Kind == core.ResourceFile) {
		if resolved.Node == nil && key.Kind == core.ResourceFile {
			external := mem.Alloc[externalFileState](p.Alloc)
			external.Program, external.Name = p, cloneText(p.Alloc, key.Name)
			if p.Engine.AddOwned(key, produceExternalFile, external, freeExternalFileState) == nil { freeExternalFileState(p.Alloc, external) }
		}
	}
	resolved.Plan.Free(p.Alloc)
	resolved.Diagnostic.Free(p.Alloc)
	if state.Index < 0 || state.Index >= len(p.Instances) { return }
	if resolved.Node != nil { p.adoptTask(resolved.Node, p.Instances[state.Index].runEpoch) }
	entry := &p.Instances[state.Index]
	p.emit(Event{Kind: DependencyDiscovered, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, DependencyKey: key})
}

// observeDefinitionDependency supplies external resources to lazy definitions.
// Definitions have no rule-instance event identity, so this intentionally only
// registers resources and never emits a target dependency event.
func observeDefinitionDependency(value any, key core.ResourceKey) {
	p := value.(*Program)
	if key.Name == "" { return }
	if key.Kind == core.ResourceEnvironment || key.Kind == core.ResourceGlob {
		state := mem.Alloc[externalValueState](p.Alloc)
		state.Program, state.Name, state.Kind = p, cloneText(p.Alloc, key.Name), key.Kind
		if p.Engine.AddOwned(key, produceExternalValue, state, freeExternalValueState) == nil { freeExternalValueState(p.Alloc, state) }
		return
	}
	resolved := p.instanceFor(key.Name)
	if resolved.Diagnostic.Code == "" || (resolved.Diagnostic.Code == "TGT_NO_RULE" && key.Kind == core.ResourceFile) {
		if resolved.Node == nil && key.Kind == core.ResourceFile {
			state := mem.Alloc[externalFileState](p.Alloc)
			state.Program, state.Name = p, cloneText(p.Alloc, key.Name)
			if p.Engine.AddOwned(key, produceExternalFile, state, freeExternalFileState) == nil { freeExternalFileState(p.Alloc, state) }
		}
	}
	resolved.Plan.Free(p.Alloc)
	resolved.Diagnostic.Free(p.Alloc)
}

func observeRenderOperation(value any, name string, version string) {
	state := value.(*renderDependencyState)
	if state.Index < 0 || state.Index >= len(state.Program.Instances) { return }
	entry := &state.Program.Instances[state.Index]
	identity := name + "\x00" + version
	for i := range entry.Operations { if entry.Operations[i] == identity { return } }
	entry.Operations = slices.Append(state.Program.Alloc, entry.Operations, cloneText(state.Program.Alloc, identity))
}

func (p *Program) cacheBlockedByBareTask(entry *instance) bool {
	inputs, resources := entry.Plan.Inputs, entry.Plan.ResourceInputs
	if entry.Plan.Resolved { inputs, resources = entry.Plan.ResolvedInputs, entry.Plan.ResolvedResourceInputs }
	for i := range inputs {
		if i < len(resources) && resources[i].Key.Kind == core.ResourceFile { continue }
		if isFileName(inputs[i]) { continue }
		selected := p.selectRule(inputs[i])
		if selected.Rule != nil && selected.Rule.Kind == rule.TaskRule { freeCaptures(p.Alloc, selected.Captures); return true }
		freeCaptures(p.Alloc, selected.Captures)
	}
	for i := range entry.Node.Dynamic {
		dependency := entry.Node.Dynamic[i]
		index := p.instanceIndex(dependency)
		if index >= 0 && p.Instances[index].Rule.Kind == rule.TaskRule { return true }
		if index >= 0 && p.Instances[index].Rule.Kind == rule.CachedTaskRule && p.cacheBlockedByBareTask(&p.Instances[index]) { return true }
	}
	return false
}

func (p *Program) emitCachedLog(entry *instance, kind EventKind, data []byte, truncated bool) {
	if len(data) == 0 && !truncated { return }
	event := Event{Kind: kind, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, Cached: true, Truncated: truncated}
	if len(data) != 0 { event.Data = slices.Clone(p.Alloc, data) }
	p.emit(event)
}

func hasYield(effects []eval.Effect) bool { for i := range effects { if effects[i].Kind == eval.EffectYield { return true } }; return false }
func yieldSpan(effects []eval.Effect) diagnostic.Span { for i := range effects { if effects[i].Kind == eval.EffectYield { return diagnostic.Span{Start: effects[i].Span.Start, End: effects[i].Span.End} } }; return diagnostic.Span{} }
func validateEffects(a mem.Allocator, entry *instance, effects []eval.Effect) diagnostic.Diagnostic { if hasYield(effects) && (entry.Rule.Kind != rule.FileRule || len(entry.Plan.Outputs) != 1) { return failureAt(a, "YIELD_INVALID", yieldSpan(effects), "yield requires one file output") }; return diagnostic.Diagnostic{} }

func (p *Program) commitEffects(entry *instance, effects []eval.Effect, writePaths []string, dryRun bool) diagnostic.Diagnostic {
	var yielded []byte
	writeIndex := 0
	hasYielded := false
	for i := range effects {
		effect := effects[i]
		p.emitNode(entry.Node, entry.Plan.Target, Effect, diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, effect.Data)
		if effect.Kind == eval.EffectOut { p.emitNode(entry.Node, entry.Plan.Target, Stdout, diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, effect.Data)
			if entry.Rule.Kind == rule.CachedTaskRule { p.cacheAppend(&entry.CacheStdout, &entry.CacheStdoutTruncated, effect.Data, p.Options.CacheRetainBytes) }
		} else if effect.Kind == eval.EffectErr { p.emitNode(entry.Node, entry.Plan.Target, Stderr, diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, effect.Data)
			if entry.Rule.Kind == rule.CachedTaskRule { p.cacheAppend(&entry.CacheStderr, &entry.CacheStderrTruncated, effect.Data, p.Options.CacheRetainBytes) }
		} else if effect.Kind == eval.EffectYield { hasYielded = true; for j := range effect.Data { yielded = slices.Append(p.Alloc, yielded, effect.Data[j]) } }
		if effect.Kind == eval.EffectWrite { if writeIndex >= len(writePaths) { slices.Free(p.Alloc, yielded); return failure(p.Alloc, "FS_ERR", "missing write path") }; if !dryRun { name := p.canonicalTarget(writePaths[writeIndex], true); temporary := name + ".littlemake-write.tmp"; ok := mkdirParent(name) && os.WriteFile(temporary, effect.Data, 0o644) == nil && os.Rename(temporary, name) == nil; if !ok { os.Remove(temporary); mem.FreeString(p.Alloc, name); slices.Free(p.Alloc, yielded); return failure(p.Alloc, "FS_ERR", "cannot write file") }; mem.FreeString(p.Alloc, name) }; writeIndex++ }
	}
	if !hasYielded { return diagnostic.Diagnostic{} }
	if dryRun { if len(yielded) != 0 { slices.Free(p.Alloc, yielded) }; return diagnostic.Diagnostic{} }
	if entry.Rule.Kind != rule.FileRule || len(entry.Plan.Outputs) != 1 { slices.Free(p.Alloc, yielded); return failure(p.Alloc, "YIELD_INVALID", "yield requires one file output") }
	name := p.canonicalTarget(entry.Plan.Outputs[0], true)
	ok := mkdirParent(name)
	temporary := name + ".littlemake-yield.tmp"
	wrote := ok && os.WriteFile(temporary, yielded, 0o644) == nil
	if wrote { ok = os.Rename(temporary, name) == nil }
	if wrote && !ok { os.Remove(temporary) }
	mem.FreeString(p.Alloc, name); slices.Free(p.Alloc, yielded)
	if !ok { return failure(p.Alloc, "FS_ERR", "cannot write yielded output") }
	return diagnostic.Diagnostic{}
}

func (p *Program) resolveInputs(c *core.EngineContext, entry *instance) inputsResult {
	hasExpression := false
	for i := range entry.Rule.Inputs { if entry.Rule.Inputs[i].Kind == rule.InputExpression { hasExpression = true; break } }
	if !hasExpression { return inputsResult{Inputs: entry.Plan.Inputs, ResourceInputs: entry.Plan.ResourceInputs} }
	var inputs []string
	var resourceInputs []PlanInput
	for i := range entry.Rule.Inputs {
		input := entry.Rule.Inputs[i]
		if input.Kind != rule.InputExpression {
			if input.Kind == rule.InputTemplate { inputs = slices.Append(p.Alloc, inputs, renderInput(p.Alloc, input, entry.Captures))
			} else { inputs = slices.Append(p.Alloc, inputs, cloneText(p.Alloc, input.Text)) }
			text := inputs[len(inputs)-1]
			kind := core.ResourceTarget
			if input.Kind == rule.InputPath { kind = core.ResourceFile }
			resourceInputs = slices.Append(p.Alloc, resourceInputs, PlanInput{Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, kind, text)})
			continue
		}
		if input.Template == nil || len(input.Template.Parts) != 1 || input.Template.Parts[0].Kind != template.Expression || input.Template.Parts[0].Expr == nil { freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Diagnostic: failure(p.Alloc, "EXPR_INVALID", "invalid rule input expression")} }
		values, outputs := makeValues(p.Alloc, inputs), makeValues(p.Alloc, entry.Plan.Outputs)
		dependencyState := renderDependencyState{Program: p, Index: p.instanceIndex(entry.Node)}
		context := &eval.Context{Program: p.Eval, Engine: c, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Phase: eval.PlanningPhase, ResolverState: &dependencyState, OperationObserver: observeRenderOperation, RuleFrames: []eval.RuleFrame{{Inputs: values, Outputs: outputs}}}
		result := p.Eval.EvaluateWith(input.Template.Parts[0].Expr, context)
		freeValues(p.Alloc, values); freeValues(p.Alloc, outputs)
		if context.PhaseInvalid() || len(context.Effects) != 0 { eval.FreeEffects(p.Alloc, context.Effects); result.Free(p.Alloc); freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Diagnostic: failure(p.Alloc, "PHASE_INVALID", "build effects are invalid while planning")} }
		if result.Waiting { freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Waiting: true} }
		if result.Diagnostic.Code != "" { freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Diagnostic: result.Diagnostic} }
		if !appendInputValue(p.Alloc, &inputs, &resourceInputs, result.Value) { result.Value.Free(p.Alloc); freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Diagnostic: failure(p.Alloc, "INPUT_INVALID", "rule input expression must produce strings, resources, lists, or nil")} }
		result.Value.Free(p.Alloc)
	}
	freeStrings(p.Alloc, entry.Plan.ResolvedInputs)
	freePlanInputs(p.Alloc, entry.Plan.ResolvedResourceInputs, true)
	entry.Plan.ResolvedInputs = cloneStrings(p.Alloc, inputs)
	entry.Plan.ResolvedResourceInputs = clonePlanInputs(p.Alloc, resourceInputs)
	entry.Plan.Resolved = true
	return inputsResult{Inputs: inputs, ResourceInputs: resourceInputs, Owned: true}
}

func appendInputValue(a mem.Allocator, inputs *[]string, resourceInputs *[]PlanInput, value core.Value) bool {
	if value.Kind == core.Nil { return true }
	if value.Kind == core.String { text := cloneText(a, value.Text); *inputs = slices.Append(a, *inputs, text); *resourceInputs = slices.Append(a, *resourceInputs, PlanInput{Display: cloneText(a, text), Key: core.NewResourceKey(a, core.ResourceTarget, text)}); return true }
	if value.Kind == core.Resource { text := cloneText(a, value.Resource.Name); *inputs = slices.Append(a, *inputs, text); *resourceInputs = slices.Append(a, *resourceInputs, PlanInput{Display: cloneText(a, text), Key: value.Resource.Clone(a)}); return true }
	if value.Kind != core.List { return false }
	for i := range value.List { if !appendInputValue(a, inputs, resourceInputs, value.List[i]) { return false } }
	return true
}

func cloneStrings(a mem.Allocator, values []string) []string { var out []string; for i := range values { out = slices.Append(a, out, cloneText(a, values[i])) }; return out }
func clonePlanInputs(a mem.Allocator, values []PlanInput) []PlanInput { var out []PlanInput; for i := range values { out = slices.Append(a, out, PlanInput{Display: cloneText(a, values[i].Display), Key: values[i].Key.Clone(a)}) }; return out }

func makeValues(a mem.Allocator, names []string) []core.Value { var out []core.Value; for i := range names { out = slices.Append(a, out, core.NewString(a, names[i])) }; return out }
func freeValues(a mem.Allocator, values []core.Value) { for i := range values { values[i].Free(a) }; slices.Free(a, values) }
func freeStrings(a mem.Allocator, values []string) { for i := range values { if values[i] != "" { mem.FreeString(a, values[i]) } }; if len(values) != 0 { slices.Free(a, values) } }
func freeOwnedStrings(a mem.Allocator, values []string, owned bool) { if owned { freeStrings(a, values) } }
func freePlanInputs(a mem.Allocator, values []PlanInput, owned bool) { if !owned { return }; for i := range values { if values[i].Display != "" { mem.FreeString(a, values[i].Display) }; values[i].Key.Free(a) }; if len(values) != 0 { slices.Free(a, values) } }

func mkdirParent(name string) bool {
	parent := path.Dir(mem.System, name)
	defer mem.FreeString(mem.System, parent)
	if parent == "." || parent == "/" { return true }
	if _, err := os.Stat(parent); err == nil { return true }
	// The recursive walk is deliberately lexical; output paths have already been normalized.
	if !mkdirParent(parent) { return false }
	if os.Mkdir(parent, 0o755) == nil { return true }
	_, err := os.Stat(parent)
	return err == nil
}

func (p *Program) pump(wait int) {
	if p.Host == nil { return }
	p.Host.Pump(wait)
	for {
		next := p.Host.Next(); if !next.OK { break }
		event := next.Event
		entry := p.instanceForRequest(event.ID)
		if entry != nil && event.Kind == posix.Stdout { p.emitNode(entry.Node, entry.Plan.Target, Stdout, diagnostic.Span{}, event.Data)
		} else if entry != nil && event.Kind == posix.Stderr { p.emitNode(entry.Node, entry.Plan.Target, Stderr, diagnostic.Span{}, event.Data)
		} else if entry != nil && event.Kind == posix.Started { p.emitNode(entry.Node, entry.Plan.Target, ProcessStarted, diagnostic.Span{}, nil)
		} else if event.Kind == posix.Terminal { if entry != nil { p.emitNode(entry.Node, entry.Plan.Target, ProcessExited, diagnostic.Span{}, nil) }; p.complete(event) }
		event.Free(p.Alloc)
	}
	p.drainCancellations()
	p.observeInstances()
}

// drainRequests services evaluator operations before scheduling resumed nodes.
func (p *Program) drainRequests() {
	for {
		next := p.Eval.Requests.Next()
		if !next.OK { return }
		request := next.Request
		if request.Kind == host.RequestProcess {
			script := host.PayloadText(request.Payload, host.FieldScript)
			// The shell operation returns captured output, so it needs a
			// retained-byte budget even when no --log-limit was given.
			retain := p.Options.RetainBytes
			if retain < cacheLogDefault { retain = cacheLogDefault }
			if script == "" || p.Host == nil || !p.Host.Start(posix.Request{ID: request.ID, Shell: p.Options.Shell, Script: []byte(script), Directory: p.Options.Directory, Environment: p.Options.Environment, TimeoutMS: p.Options.TimeoutMS, RetainBytes: retain}) {
				p.Engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Diagnostic: failure(p.Alloc, "HOST_FAIL", "cannot start shell request")})
			} else { p.Pending = slices.Append(p.Alloc, p.Pending, pendingRequest{ID: request.ID, NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Retries: 0}) }
		} else { p.completeRequest(request) }
		request.Free(p.Alloc)
	}
}

func (p *Program) completeRequest(request host.Request) {
	completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID}
	name := host.PayloadPath(request.Payload)
	if request.Kind == host.RequestEnvironment {
		value, ok := p.configuredEnvironment(name)
		if ok { completion.Value, completion.HasValue = core.NewString(p.Alloc, value), true } else { completion.Value, completion.HasValue = core.Value{Kind: core.Nil}, true }
	} else if request.Kind == host.RequestReadFile {
		op := host.PayloadText(request.Payload, host.FieldOp)
		if op == "" { op = host.OpRead }
		completion = p.fileCompletion(request, op, name)
	} else if request.Kind == host.RequestWriteFile {
		filename := p.canonicalTarget(name, true)
		data := host.PayloadBytes(request.Payload, host.FieldData)
		temporary := filename + ".littlemake-write.tmp"
		ok := mkdirParent(filename) && os.WriteFile(temporary, data, 0o644) == nil && os.Rename(temporary, filename) == nil
		if !ok { os.Remove(temporary); completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot write file") } else { completion.Value, completion.HasValue = core.Value{Kind: core.Nil}, true }
		mem.FreeString(p.Alloc, filename)
	} else { completion.Diagnostic = failure(p.Alloc, "HOST_FAIL", "unsupported host request") }
	p.Engine.Complete(completion)
}

func (p *Program) fileCompletion(request host.Request, op string, name string) core.Completion {
	completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID}
	filename := p.canonicalTarget(name, true)
	defer mem.FreeString(p.Alloc, filename)
	if op == host.OpRead {
		data, err := os.ReadFile(p.Alloc, filename)
		if err != nil { completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot read file"); return completion }
		completion.Value, completion.HasValue = core.NewBytes(p.Alloc, data), true; mem.FreeSlice(p.Alloc, data); return completion
	}
	if op == host.OpExists {
		_, err := os.Stat(filename); completion.Value, completion.HasValue = core.Value{Kind: core.Bool, Bool: err == nil}, true; return completion
	}
	if op == host.OpStat {
		info, err := os.Stat(filename)
		if err != nil { completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot stat file"); return completion }
		fields := []core.RecordField{{Key: "name", Value: core.NewString(p.Alloc, name)}, {Key: "size", Value: core.Value{Kind: core.Int, Int: info.Size()}}, {Key: "mode", Value: core.Value{Kind: core.Int, Int: int64(info.Mode())}}, {Key: "dir", Value: core.Value{Kind: core.Bool, Bool: info.IsDir()}}}
		completion.Value, completion.HasValue = core.NewRecord(p.Alloc, fields), true
		for i := range fields { fields[i].Value.Free(p.Alloc) }
		return completion
	}
	if op == host.OpWildcard { completion.Value, completion.HasValue = p.wildcard(name), true; return completion }
	completion.Diagnostic = failure(p.Alloc, "HOST_FAIL", "unknown filesystem request")
	return completion
}

func (p *Program) wildcard(pattern string) core.Value {
	pattern = p.canonicalTarget(pattern, true)
	defer mem.FreeString(p.Alloc, pattern)
	root := globRoot(p.Alloc, pattern)
	defer mem.FreeString(p.Alloc, root)
	var names []string
	collectPaths(p.Alloc, root, &names)
	var values []core.Value
	for i := range names {
		matched, err := globMatches(pattern, names[i])
		if err == nil && matched {
			relative := p.relativePath(names[i])
			values = slices.Append(p.Alloc, values, core.NewString(p.Alloc, relative))
			mem.FreeString(p.Alloc, relative)
		}
		mem.FreeString(p.Alloc, names[i])
	}
	slices.Free(p.Alloc, names)
	for i := 1; i < len(values); i++ { for j := i; j > 0 && values[j].Text < values[j-1].Text; j-- { values[j], values[j-1] = values[j-1], values[j] } }
	result := core.NewList(p.Alloc, values)
	for i := range values { values[i].Free(p.Alloc) }
	slices.Free(p.Alloc, values)
	return result
}

func (p *Program) relativePath(name string) string {
	cwd := p.Options.Directory
	if cwd == "." && !path.IsAbs(name) { return cloneText(p.Alloc, "./"+name) }
	if cwd != "" && len(name) > len(cwd) && name[:len(cwd)] == cwd && name[len(cwd)] == '/' { return cloneText(p.Alloc, "./"+name[len(cwd)+1:]) }
	return cloneText(p.Alloc, name)
}

func globRoot(a mem.Allocator, pattern string) string {
	cut := -1
	for i := range pattern { if pattern[i] == '*' || pattern[i] == '?' || pattern[i] == '[' { cut = i; break } }
	if cut < 0 { return cloneText(a, pattern) }
	for cut > 0 && pattern[cut-1] != '/' { cut-- }
	if cut == 0 { return cloneText(a, ".") }
	if cut == 1 { return cloneText(a, "/") }
	return cloneText(a, pattern[:cut-1])
}

func collectPaths(a mem.Allocator, directory string, names *[]string) {
	entries, err := os.ReadDir(a, directory)
	if err != nil { return }
	defer os.FreeDirEntry(a, entries)
	for i := range entries {
		name := path.Join(a, directory, entries[i].Name)
		*names = slices.Append(a, *names, name)
		if entries[i].IsDir { collectPaths(a, name, names) }
	}
}

func globMatches(pattern string, name string) (bool, error) { return matchSegments(pattern, 0, name, 0) }

func matchSegments(pattern string, pi int, name string, ni int) (bool, error) {
	if pi == len(pattern) { return ni == len(name), nil }
	pend := pi; for pend < len(pattern) && pattern[pend] != '/' { pend++ }
	nend := ni; for nend < len(name) && name[nend] != '/' { nend++ }
	segment := pattern[pi:pend]
	if segment == "**" {
		if ok, err := matchSegments(pattern, nextSegment(pattern, pend), name, ni); ok || err != nil { return ok, err }
		for cursor := ni; cursor < len(name); cursor++ { if name[cursor] == '/' { if ok, err := matchSegments(pattern, nextSegment(pattern, pend), name, cursor+1); ok || err != nil { return ok, err } } }
		return false, nil
	}
	if ni == len(name) { return false, nil }
	ok, err := path.Match(segment, name[ni:nend])
	if err != nil || !ok { return false, err }
	if pend == len(pattern) || nend == len(name) { return pend == len(pattern) && nend == len(name), nil }
	return matchSegments(pattern, pend+1, name, nend+1)
}

func nextSegment(value string, end int) int { if end < len(value) { return end + 1 }; return end }

func (p *Program) drainCancellations() {
	for {
		cancellation := p.Engine.NextCancellation()
		if cancellation.RequestID == 0 { return }
		if p.Host != nil { p.Host.Cancel(cancellation.RequestID) }
	}
}

func (p *Program) complete(event posix.Event) {
	var d diagnostic.Diagnostic
	if event.Outcome == posix.Failed { d = failure(p.Alloc, "HOST_FAIL", event.Diagnostic.Message)
	} else if event.Outcome == posix.TimedOut { d = failure(p.Alloc, "RECIPE_TIMEOUT", "recipe timed out")
	} else if event.Outcome == posix.Cancelled { d = failure(p.Alloc, "EXEC_CANCELLED", "recipe cancelled")
	} else if event.Status != 0 { d = failure(p.Alloc, "RECIPE_FAIL", "recipe exited unsuccessfully"); if entry := p.instanceForRequest(event.ID); entry != nil && len(entry.LineSpans) != 0 { d.Span = entry.LineSpans[0] } }
	for i := range p.Pending {
		pending := p.Pending[i]
		if pending.ID != event.ID { continue }
		copy(p.Pending[i:], p.Pending[i+1:]); p.Pending = p.Pending[:len(p.Pending)-1]
		if d.Code == "RECIPE_FAIL" { d.Free(p.Alloc); d = diagnostic.Diagnostic{} }
		completion := core.Completion{NodeID: pending.NodeID, Generation: pending.Generation, Attempt: pending.Attempt, RequestID: event.ID, Diagnostic: d}
		if d.Code == "" { completion.Value, completion.HasValue = shellValue(p.Alloc, event), true }
		p.Engine.Complete(completion)
		return
	}
	node := p.nodeForRequest(event.ID)
	entry := p.instanceForRequest(event.ID)
	if node!=nil && entry!=nil && node.HostRequestID==event.ID && node.State==core.NodeWaiting && d.Code!="" && d.Code!="EXEC_CANCELLED" && entry.retryCount<p.Options.RetryCount {
		p.nextRequest++
		retryID:=p.nextRequest
		retain:=p.Options.RetainBytes
		if p.Options.CacheRetainBytes>retain { retain=p.Options.CacheRetainBytes }
		request:=posix.Request{ID:retryID,Shell:p.Options.Shell,Script:[]byte(entry.Script),Directory:p.Options.Directory,Environment:p.Options.Environment,TimeoutMS:p.Options.TimeoutMS,RetainBytes:retain}
		if p.Host!=nil && p.Host.Start(request) {
			entry.retryCount++
			node.HostRequestID=retryID
			d.Free(p.Alloc)
			return
		}
	}
	if node != nil && entry != nil && node.HostRequestID == event.ID && node.State == core.NodeWaiting && entry.Rule.Kind == rule.CachedTaskRule && entry.CacheReady && d.Code == "" && event.Status == 0 && !p.Options.CacheDisabled {
		p.cacheCommit(entry, event.Stdout, event.Stderr, event.StdoutTruncated, event.StderrTruncated)
	}
	if node != nil { p.Engine.Complete(core.Completion{NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: event.ID, Diagnostic: d})
	} else { d.Free(p.Alloc) }
}

func (p *Program) cacheAppend(dst *[]byte, truncated *bool, data []byte, limit int) {
	if limit <= 0 { limit = cacheLogDefault }
	for i := range data { if len(*dst) >= limit { *truncated = true; return }; *dst = slices.Append(p.Alloc, *dst, data[i]) }
}

func (p *Program) cacheCommit(entry *instance, stdout []byte, stderr []byte, stdoutTruncated bool, stderrTruncated bool) {
	if p.cacheBlockedByBareTask(entry) { return }
	p.cacheAppend(&entry.CacheStdout, &entry.CacheStdoutTruncated, stdout, p.Options.CacheRetainBytes)
	p.cacheAppend(&entry.CacheStderr, &entry.CacheStderrTruncated, stderr, p.Options.CacheRetainBytes)
	if stdoutTruncated { entry.CacheStdoutTruncated = true }; if stderrTruncated { entry.CacheStderrTruncated = true }
	identity := p.cacheIdentity(entry)
	completed := time.Now().UnixNano()
	started := entry.cacheStartedAt
	if started<=0 || started>completed { started=completed }
	record := cacheRecord{Identity: identity, Schema: 1, StartedAt: started, CompletedAt: completed, Duration: completed-started, ExitStatus: 0, Manifest: slices.Clone(p.Alloc, entry.CacheManifest), Stdout: slices.Clone(p.Alloc, entry.CacheStdout), Stderr: slices.Clone(p.Alloc, entry.CacheStderr), StdoutTruncated: entry.CacheStdoutTruncated, StderrTruncated: entry.CacheStderrTruncated}
	for i := range record.Fingerprint { record.Fingerprint[i] = entry.CacheFingerprint[i] }
	p.cacheSave(entry, &record)
	record.Free(p.Alloc)
}

func shellValue(a mem.Allocator, event posix.Event) core.Value {
	fields := []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: int64(event.Status)}}, {Key: "stdout", Value: core.NewBytes(a, event.Stdout)}, {Key: "stderr", Value: core.NewBytes(a, event.Stderr)}}
	value := core.NewRecord(a, fields)
	for i := range fields { fields[i].Value.Free(a) }
	return value
}

func (p *Program) instanceForRequest(id int64) *instance {
	for i := range p.Instances { if p.Instances[i].Node.HostRequestID == id { return &p.Instances[i] } }
	return nil
}

func (p *Program) definitionNode(name string) *core.Node { return p.Eval.Definition(name) }

func (p *Program) prepareDependency(c *core.EngineContext, index int, dependency *core.Node) bool {
	p.adoptTask(dependency, p.Instances[index].runEpoch)
	return p.addDependency(c, &p.Instances[index], dependency)
}

func (p *Program) adoptTask(node *core.Node, epoch int64) {
	p.claimStaleTask(node, epoch)
	index := p.instanceIndex(node)
	if index < 0 || epoch == 0 { return }
	if p.Instances[index].Node.State == core.NodeWaiting || p.Instances[index].Node.State == core.NodeReady { return }
	if p.Instances[index].runEpoch < epoch { p.Instances[index].runEpoch = epoch }
}

func (p *Program) addDependency(c *core.EngineContext, entry *instance, dependency *core.Node) bool {
	existed := slices.Contains(entry.Node.Dynamic, dependency)
	current := c.Dependency(dependency.Key)
	if !existed && slices.Contains(entry.Node.Dynamic, dependency) {
		event := Event{Kind: DependencyDiscovered, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, DependencyID: dependency.ID, DependencyKey: dependency.Key}
		p.emit(event)
	}
	return current
}

func (p *Program) nodeForRequest(id int64) *core.Node { for i := range p.Instances { if p.Instances[i].Node.HostRequestID == id { return p.Instances[i].Node } }; return nil }

func (p *Program) emit(event Event) {
	event.Target = cloneText(p.Alloc, event.Target)
	if event.Key.Name != "" { event.Key = event.Key.Clone(p.Alloc) }
	if event.DependencyKey.Name != "" { event.DependencyKey = event.DependencyKey.Clone(p.Alloc) }
	p.Events = slices.Append(p.Alloc, p.Events, event)
}

func (p *Program) emitNode(node *core.Node, target string, kind EventKind, span diagnostic.Span, data []byte) {
	if node == nil { return }
	event := Event{Kind: kind, Target: target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: node.HostRequestID, Span: span}
	if len(data) != 0 { event.Data = slices.Clone(p.Alloc, data) }
	p.emit(event)
}

func (p *Program) observeInstances() {
	for i := range p.Instances {
		entry := &p.Instances[i]
		node := entry.Node
		if node.Current && node.Revision != entry.valueRevision {
			event := Event{Kind: TargetValue, Target: entry.Plan.Target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: node.HostRequestID, Value: node.Latest.Clone(p.Alloc)}
			p.emit(event)
			entry.valueRevision = node.Revision
		}
		if entry.terminalEmitted && entry.terminalGeneration == node.Generation { continue }
		kind := EventKind(-1)
		if node.State == core.NodeComplete { kind = TargetCompleted
		} else if node.State == core.NodeFailed { kind = TargetFailed
		} else if node.State == core.NodeCancelled { kind = TargetCancelled
		}
		if kind < 0 { continue }
		event := Event{Kind: kind, Target: entry.Plan.Target, Key: node.Key, NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: node.HostRequestID}
		if node.Diagnostic.Code != "" { event.Diagnostic = node.Diagnostic.Clone(p.Alloc) }
		p.emit(event)
		entry.terminalEmitted, entry.terminalGeneration = true, node.Generation
	}
}

func (p *Program) NextEvent() EventResult {
	if len(p.Events) == 0 { return EventResult{} }
	event := p.Events[0]; copy(p.Events, p.Events[1:]); p.Events = p.Events[:len(p.Events)-1]; return EventResult{Event: event, OK: true}
}
