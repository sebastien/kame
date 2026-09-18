package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host/posix"
	"littlemake/lang/eval"
	"littlemake/lang/rule"
	"littlemake/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
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
type renderResult struct { Commands string; Effects []eval.Effect; LineSpans []diagnostic.Span; Waiting bool; Diagnostic diagnostic.Diagnostic }
type inputsResult struct { Inputs []string; ResourceInputs []PlanInput; Owned bool; Waiting bool; Diagnostic diagnostic.Diagnostic }
type renderDependencyState struct { Program *Program; Index int }
type externalFileState struct { Program *Program; Name string }

func freeInstanceState(a mem.Allocator, value any) { mem.Free(a, value.(*instanceState)) }
func freeExternalFileState(a mem.Allocator, value any) { state := value.(*externalFileState); if state.Name != "" { mem.FreeString(a, state.Name) }; mem.Free(a, state) }

func produceExternalFile(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*externalFileState)
	name := state.Program.canonicalTarget(state.Name, true)
	_, err := os.Stat(name)
	mem.FreeString(state.Program.Alloc, name)
	if err != nil { c.Fail(failure("TGT_NO_RULE", "required input does not exist: "+state.Name)); return core.ProducerFailed }
	c.Publish(core.NewString(c.Allocator(), state.Name))
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
			if err == nil { return Result{Path: cloneText(p.Alloc, target), Fresh: true} }
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
	if definition { node = p.Eval.Definition(target); if node == nil { plan.Free(p.Alloc); return HandleStart{Diagnostic: failure("TGT_NO_RULE", "no rule for target: "+target)} }
	}
	plan.Free(p.Alloc)
	index := p.instanceIndex(node)
	if index >= 0 && node.State == core.NodeComplete {
		entry := &p.Instances[index]
		if entry.Rule.Kind == rule.FileRule { entry.Plan.Freshness = p.freshness(&entry.Plan, node); if entry.Plan.Freshness == Stale { p.Engine.Invalidate(node) }
		} else if entry.Rule.Kind == rule.TaskRule || entry.Rule.Kind == rule.CachedTaskRule { p.Engine.Invalidate(node) }
	}
	handle := mem.Alloc[Handle](p.Alloc)
	handle.Program, handle.Root, handle.Node, handle.Target, handle.Definition = p, p.Engine.RequestRoot(node), node, cloneText(p.Alloc, target), definition
	return HandleStart{Handle: handle}
}

func (p *Program) Tick(wait int) {
	p.pump(wait)
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
	if h == nil || h.Program == nil || h.Node == nil { return HandleResult{Done: true, Result: Result{Diagnostic: failure("TGT_NO_RULE", "invalid handle")}} }
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
	if node == nil { plan.Free(p.Alloc); return instanceResult{Diagnostic: failure("TGT_AMBIG", "duplicate rule instance")} }
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
		p.emitNode(entry.Node, entry.Plan.Target, TargetStarted, diagnostic.Span{}, nil)
		entry.started, entry.startedGeneration, entry.terminalEmitted = true, c.Generation(), false
	}
	if entry.Rule.Kind == rule.ServiceRule { c.Fail(failure("FEATURE_UNSUP", "service execution is not supported")); return core.ProducerFailed }
	if c.Completion().RequestID != 0 {
		if entry.Script != "" { mem.FreeString(p.Alloc, entry.Script) }; entry.Script = ""
		if c.Completion().Diagnostic.Code != "" { c.Fail(c.Completion().Diagnostic); return core.ProducerFailed }
		if entry.Rule.Kind == rule.FileRule {
			for i := range entry.Plan.Outputs {
				name := p.canonicalTarget(entry.Plan.Outputs[i], true)
				_, err := os.Stat(name)
				mem.FreeString(p.Alloc, name)
				if err != nil { c.Fail(failure("OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i])); return core.ProducerFailed }
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
			if resolved.Node != nil {
				resolved.Plan.Free(p.Alloc)
				if !p.addDependency(c, entry, resolved.Node) { return core.ProducerWaiting }
				continue
			}
			resolved.Plan.Free(p.Alloc)
			name := p.canonicalTarget(input, true)
			_, err := os.Stat(name)
			mem.FreeString(p.Alloc, name)
			if err != nil { c.Fail(failure("TGT_NO_RULE", "required input does not exist: "+input)); return core.ProducerFailed }
			continue
		}
		resolved := p.instanceFor(input)
		entry = &p.Instances[state.Index]
		dep, d := resolved.Node, resolved.Diagnostic
		resolved.Plan.Free(p.Alloc)
		if d.Code == "" && dep != nil {
			if !p.addDependency(c, entry, dep) { return core.ProducerWaiting }
			continue
		}
		definition := p.Eval.Definition(input)
		if definition == nil { c.Fail(failure("TGT_NO_RULE", "no rule for target: "+input)); return core.ProducerFailed }
		definitionNode := p.definitionNode(definition.Key.Name)
		if definitionNode == nil || !p.addDependency(c, entry, definitionNode) { return core.ProducerWaiting }
	}
	entry = &p.Instances[state.Index]
	rendered := p.render(c, entry, inputs)
	commands, effects, d := rendered.Commands, rendered.Effects, rendered.Diagnostic
	if len(entry.LineSpans) != 0 { slices.Free(p.Alloc, entry.LineSpans) }
	entry.LineSpans = rendered.LineSpans
	defer eval.FreeEffects(p.Alloc, effects)
	if rendered.Waiting { return core.ProducerWaiting }
	if d.Code != "" { c.Fail(d); return core.ProducerFailed }
	if entry.Rule.Kind == rule.FileRule { entry.Plan.Freshness = p.freshness(&entry.Plan, entry.Node) } else { entry.Plan.Freshness = Stale }
	if entry.Plan.Freshness == Fresh && !p.Options.DryRun {
		if commands != "" { mem.FreeString(p.Alloc, commands) }
		if entry.Rule.Kind == rule.FileRule { c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0])) } else { c.Publish(core.Value{Kind: core.Nil}) }
		return core.ProducerCompleted
	}
	if hasYield(effects) && commands != "" { if commands != "" { mem.FreeString(p.Alloc, commands) }; c.Fail(failureAt("OUTPUT_CONFLICT", yieldSpan(effects), "yield cannot be combined with shell commands")); return core.ProducerFailed }
	if effectDiagnostic := validateEffects(entry, effects); effectDiagnostic.Code != "" { if commands != "" { mem.FreeString(p.Alloc, commands) }; c.Fail(effectDiagnostic); return core.ProducerFailed }
	if p.Options.DryRun { p.commitEffects(entry, effects, true); if commands != "" { mem.FreeString(p.Alloc, commands) }; c.Publish(core.Value{Kind: core.Nil}); return core.ProducerCompleted }
	if effectDiagnostic := p.commitEffects(entry, effects, false); effectDiagnostic.Code != "" { if commands != "" { mem.FreeString(p.Alloc, commands) }; c.Fail(effectDiagnostic); return core.ProducerFailed }
	if commands == "" {
		if entry.Rule.Kind == rule.FileRule && !hasYield(effects) {
			for i := range entry.Plan.Outputs {
				name := p.canonicalTarget(entry.Plan.Outputs[i], true)
				_, err := os.Stat(name)
				mem.FreeString(p.Alloc, name)
				if err != nil { c.Fail(failure("OUTPUT_MISSING", "recipe omitted declared output: "+entry.Plan.Outputs[i])); return core.ProducerFailed }
			}
		}
		if entry.Rule.Kind == rule.FileRule && hasYield(effects) { c.Publish(core.NewString(c.Allocator(), entry.Plan.Outputs[0])) } else { c.Publish(core.Value{Kind: core.Nil}) }
		return core.ProducerCompleted
	}
	if entry.Rule.Kind == rule.FileRule {
		for i := range entry.Plan.Outputs {
			name := p.canonicalTarget(entry.Plan.Outputs[i], true)
			ok := mkdirParent(name)
			mem.FreeString(p.Alloc, name)
			if !ok { c.Fail(failure("FS_ERR", "cannot create output directory")); return core.ProducerFailed }
		}
	}
	p.nextRequest++
	entry.Script = commands
	request := posix.Request{ID: p.nextRequest, Shell: p.Options.Shell, Script: []byte(entry.Script), Directory: p.Options.Directory, Environment: p.Options.Environment, RetainBytes: p.Options.RetainBytes}
	if !p.Host.Start(request) { c.Fail(failure("HOST_FAIL", "cannot start recipe")); return core.ProducerFailed }
	c.Submit(request.ID)
	return core.ProducerSubmitted
}

func (p *Program) render(c *core.EngineContext, entry *instance, names []string) renderResult {
	inputs := makeValues(p.Alloc, names)
	outputs := makeValues(p.Alloc, entry.Plan.Outputs)
	defer freeValues(p.Alloc, inputs); defer freeValues(p.Alloc, outputs)
	dependencyState := renderDependencyState{Program: p, Index: p.instanceIndex(entry.Node)}
	context := &eval.Context{Program: p.Eval, Engine: c, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Phase: eval.RenderingPhase, ResolverState: &dependencyState, DependencyObserver: observeRenderDependency, RuleFrames: []eval.RuleFrame{{Inputs: inputs, Outputs: outputs}}}
	b := strings.NewBuilder(p.Alloc)
	defer b.Free()
	var spans []diagnostic.Span
	for i := range entry.Rule.Body {
		result := p.Eval.Render(p.Alloc, entry.Rule.Body[i].Template, p.Eval.Scope, context)
		if result.Waiting { eval.FreeEffects(p.Alloc, context.Effects); return renderResult{Waiting: true} }
		if result.Diagnostic.Code != "" { eval.FreeEffects(p.Alloc, context.Effects); return renderResult{Diagnostic: result.Diagnostic} }
		if result.Value.Kind != core.String { result.Value.Free(p.Alloc); eval.FreeEffects(p.Alloc, context.Effects); return renderResult{Diagnostic: failure("EXPR_INVALID", "recipe line is not text")} }
		if result.Value.Text != "" { if b.Len() != 0 { b.WriteByte('\n') }; b.WriteString(result.Value.Text); spans = slices.Append(p.Alloc, spans, diagnostic.Span{Start: entry.Rule.Body[i].Span.Start, End: entry.Rule.Body[i].Span.End}) }
		result.Value.Free(p.Alloc)
	}
	var effects []eval.Effect
	for i := range context.Effects { effects = slices.Append(p.Alloc, effects, eval.Effect{Kind: context.Effects[i].Kind, Data: slices.Clone(p.Alloc, context.Effects[i].Data), Span: context.Effects[i].Span}) }
	eval.FreeEffects(p.Alloc, context.Effects)
	return renderResult{Commands: cloneText(p.Alloc, b.String()), Effects: effects, LineSpans: spans}
}

func observeRenderDependency(value any, key core.ResourceKey) {
	state := value.(*renderDependencyState)
	if key.Name == "" { return }
	p := state.Program
	resolved := p.instanceFor(key.Name)
	if resolved.Diagnostic.Code == "" || (resolved.Diagnostic.Code == "TGT_NO_RULE" && key.Kind == core.ResourceFile) {
		if resolved.Node == nil && key.Kind == core.ResourceFile {
			external := mem.Alloc[externalFileState](p.Alloc)
			external.Program, external.Name = p, cloneText(p.Alloc, key.Name)
			if p.Engine.AddOwned(key, produceExternalFile, external, freeExternalFileState) == nil { freeExternalFileState(p.Alloc, external) }
		}
		resolved.Plan.Free(p.Alloc)
	}
	if state.Index < 0 || state.Index >= len(p.Instances) { return }
	entry := &p.Instances[state.Index]
	p.emit(Event{Kind: DependencyDiscovered, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, DependencyKey: key})
}

func hasYield(effects []eval.Effect) bool { for i := range effects { if effects[i].Kind == eval.EffectYield { return true } }; return false }
func yieldSpan(effects []eval.Effect) diagnostic.Span { for i := range effects { if effects[i].Kind == eval.EffectYield { return diagnostic.Span{Start: effects[i].Span.Start, End: effects[i].Span.End} } }; return diagnostic.Span{} }
func validateEffects(entry *instance, effects []eval.Effect) diagnostic.Diagnostic { if hasYield(effects) && (entry.Rule.Kind != rule.FileRule || len(entry.Plan.Outputs) != 1) { return failureAt("YIELD_INVALID", yieldSpan(effects), "yield requires one file output") }; return diagnostic.Diagnostic{} }

func (p *Program) commitEffects(entry *instance, effects []eval.Effect, dryRun bool) diagnostic.Diagnostic {
	var yielded []byte
	hasYielded := false
	for i := range effects {
		effect := effects[i]
		p.emitNode(entry.Node, entry.Plan.Target, Effect, diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, effect.Data)
		if effect.Kind == eval.EffectOut { p.emitNode(entry.Node, entry.Plan.Target, Stdout, diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, effect.Data)
		} else if effect.Kind == eval.EffectErr { p.emitNode(entry.Node, entry.Plan.Target, Stderr, diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, effect.Data)
		} else if effect.Kind == eval.EffectYield { hasYielded = true; for j := range effect.Data { yielded = slices.Append(p.Alloc, yielded, effect.Data[j]) } }
	}
	if !hasYielded { return diagnostic.Diagnostic{} }
	if dryRun { if len(yielded) != 0 { slices.Free(p.Alloc, yielded) }; return diagnostic.Diagnostic{} }
	if entry.Rule.Kind != rule.FileRule || len(entry.Plan.Outputs) != 1 { slices.Free(p.Alloc, yielded); return failure("YIELD_INVALID", "yield requires one file output") }
	name := p.canonicalTarget(entry.Plan.Outputs[0], true)
	ok := mkdirParent(name)
	temporary := name + ".littlemake-yield.tmp"
	wrote := ok && os.WriteFile(temporary, yielded, 0o644) == nil
	if wrote { ok = os.Rename(temporary, name) == nil }
	if wrote && !ok { os.Remove(temporary) }
	mem.FreeString(p.Alloc, name); slices.Free(p.Alloc, yielded)
	if !ok { return failure("FS_ERR", "cannot write yielded output") }
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
		if input.Template == nil || len(input.Template.Parts) != 1 || input.Template.Parts[0].Kind != template.Expression || input.Template.Parts[0].Expr == nil { freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Diagnostic: failure("EXPR_INVALID", "invalid rule input expression")} }
		values, outputs := makeValues(p.Alloc, inputs), makeValues(p.Alloc, entry.Plan.Outputs)
		context := &eval.Context{Program: p.Eval, Engine: c, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Phase: eval.PlanningPhase, RuleFrames: []eval.RuleFrame{{Inputs: values, Outputs: outputs}}}
		result := p.Eval.EvaluateWith(input.Template.Parts[0].Expr, context)
		freeValues(p.Alloc, values); freeValues(p.Alloc, outputs)
		if context.PhaseInvalid() || len(context.Effects) != 0 { eval.FreeEffects(p.Alloc, context.Effects); result.Free(p.Alloc); freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Diagnostic: failure("PHASE_INVALID", "build effects are invalid while planning")} }
		if result.Waiting { freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Waiting: true} }
		if result.Diagnostic.Code != "" { freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Diagnostic: result.Diagnostic} }
		if !appendInputValue(p.Alloc, &inputs, &resourceInputs, result.Value) { result.Value.Free(p.Alloc); freeStrings(p.Alloc, inputs); freePlanInputs(p.Alloc, resourceInputs, true); return inputsResult{Diagnostic: failure("INPUT_INVALID", "rule input expression must produce strings, resources, lists, or nil")} }
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

func (p *Program) drainCancellations() {
	for {
		cancellation := p.Engine.NextCancellation()
		if cancellation.RequestID == 0 { return }
		if p.Host != nil { p.Host.Cancel(cancellation.RequestID) }
	}
}

func (p *Program) complete(event posix.Event) {
	var d diagnostic.Diagnostic
	if event.Outcome == posix.Failed { d = failure("HOST_FAIL", event.Diagnostic.Message)
	} else if event.Outcome == posix.TimedOut { d = failure("RECIPE_TIMEOUT", "recipe timed out")
	} else if event.Outcome == posix.Cancelled { d = failure("EXEC_CANCELLED", "recipe cancelled")
	} else if event.Status != 0 { d = failure("RECIPE_FAIL", "recipe exited unsuccessfully"); if entry := p.instanceForRequest(event.ID); entry != nil && len(entry.LineSpans) != 0 { d.Span = entry.LineSpans[0] } }
	node := p.nodeForRequest(event.ID)
	if node == nil { return }
	p.Engine.Complete(core.Completion{NodeID: node.ID, Generation: node.Generation, Attempt: node.Attempt, RequestID: event.ID, Diagnostic: d})
}

func (p *Program) instanceForRequest(id int64) *instance {
	for i := range p.Instances { if p.Instances[i].Node.HostRequestID == id { return &p.Instances[i] } }
	return nil
}

func (p *Program) definitionNode(name string) *core.Node { return p.Eval.Definition(name) }

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
