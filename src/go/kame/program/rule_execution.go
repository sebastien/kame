package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

func (p *Program) cacheBlockedByBareTask(entry *instance) bool {
	inputs, resources := entry.Plan.Inputs, entry.Plan.ResourceInputs
	if entry.Plan.Resolved {
		inputs, resources = entry.Plan.ResolvedInputs, entry.Plan.ResolvedResourceInputs
	}
	for i := range inputs {
		if i < len(resources) && resources[i].Key.Kind == core.ResourceFile {
			continue
		}
		if isFileName(inputs[i]) {
			continue
		}
		selected := p.selectRule(inputs[i])
		if selected.Rule != nil && selected.Rule.Kind == rule.TaskRule {
			freeCaptures(p.Alloc, selected.Captures)
			return true
		}
		freeCaptures(p.Alloc, selected.Captures)
	}
	for i := range entry.Node.Dynamic {
		dependency := entry.Node.Dynamic[i]
		index := p.instanceIndex(dependency)
		if index >= 0 && p.Instances[index].Rule.Kind == rule.TaskRule {
			return true
		}
		if index >= 0 && p.Instances[index].Rule.Kind == rule.CachedTaskRule && p.cacheBlockedByBareTask(&p.Instances[index]) {
			return true
		}
	}
	return false
}

func (p *Program) emitCachedLog(entry *instance, kind EventKind, data []byte, truncated bool) {
	if len(data) == 0 && !truncated {
		return
	}
	event := Event{Kind: kind, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, Cached: true, Truncated: truncated}
	if len(data) != 0 {
		event.Data = slices.Clone(p.Alloc, data)
	}
	p.emit(event)
}

func hasYield(effects []eval.Effect) bool {
	for i := range effects {
		if effects[i].Kind == eval.EffectYield {
			return true
		}
	}
	return false
}
func yieldSpan(effects []eval.Effect) diagnostic.Span {
	for i := range effects {
		if effects[i].Kind == eval.EffectYield {
			return diagnostic.Span{Start: effects[i].Span.Start, End: effects[i].Span.End}
		}
	}
	return diagnostic.Span{}
}
func validateEffects(a mem.Allocator, entry *instance, effects []eval.Effect) diagnostic.Diagnostic {
	if hasYield(effects) && (entry.Rule.Kind != rule.FileRule || len(entry.Plan.Outputs) != 1) {
		return failureAt(a, "EXPR_INVALID", yieldSpan(effects), "yield requires one file output")
	}
	return diagnostic.Diagnostic{}
}

func (p *Program) commitEffects(entry *instance, effects []eval.Effect, writePaths []string, dryRun bool) diagnostic.Diagnostic {
	var yielded []byte
	writeIndex := 0
	hasYielded := false
	for i := range effects {
		effect := effects[i]
		p.emit(Event{Kind: Effect, Target: entry.Plan.Target, Key: entry.Node.Key, NodeID: entry.Node.ID, Generation: entry.Node.Generation, Attempt: entry.Node.Attempt, RequestID: entry.Node.HostRequestID, Span: diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, Data: slices.Clone(p.Alloc, effect.Data), Effect: effectName(effect.Kind)})
		if effect.Kind == eval.EffectOut {
			p.emitNode(entry.Node, entry.Plan.Target, Stdout, diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, effect.Data)
			if entry.Rule.Kind == rule.CachedTaskRule {
				p.cacheAppend(&entry.CacheStdout, &entry.CacheStdoutTruncated, effect.Data, p.Options.CacheRetainBytes)
			}
		} else if effect.Kind == eval.EffectErr {
			p.emitNode(entry.Node, entry.Plan.Target, Stderr, diagnostic.Span{Start: effect.Span.Start, End: effect.Span.End}, effect.Data)
			if entry.Rule.Kind == rule.CachedTaskRule {
				p.cacheAppend(&entry.CacheStderr, &entry.CacheStderrTruncated, effect.Data, p.Options.CacheRetainBytes)
			}
		} else if effect.Kind == eval.EffectYield {
			hasYielded = true
			for j := range effect.Data {
				yielded = slices.Append(p.Alloc, yielded, effect.Data[j])
			}
		}
		if effect.Kind == eval.EffectWrite {
			if writeIndex >= len(writePaths) {
				slices.Free(p.Alloc, yielded)
				return failure(p.Alloc, "FS_ERR", "missing write path")
			}
			if !dryRun {
				name := p.canonicalTarget(writePaths[writeIndex], true)
				temporary := name + ".kame-write.tmp"
				ok := mkdirParent(name) && os.WriteFile(temporary, effect.Data, 0o644) == nil && os.Rename(temporary, name) == nil
				if !ok {
					os.Remove(temporary)
					mem.FreeString(p.Alloc, name)
					slices.Free(p.Alloc, yielded)
					return failure(p.Alloc, "FS_ERR", "cannot write file")
				}
				mem.FreeString(p.Alloc, name)
			}
			writeIndex++
		}
	}
	if !hasYielded {
		return diagnostic.Diagnostic{}
	}
	if dryRun {
		if len(yielded) != 0 {
			slices.Free(p.Alloc, yielded)
		}
		return diagnostic.Diagnostic{}
	}
	if entry.Rule.Kind != rule.FileRule || len(entry.Plan.Outputs) != 1 {
		slices.Free(p.Alloc, yielded)
		return failure(p.Alloc, "EXPR_INVALID", "yield requires one file output")
	}
	name := p.canonicalTarget(entry.Plan.Outputs[0], true)
	ok := mkdirParent(name)
	temporary := name + ".kame-yield.tmp"
	wrote := ok && os.WriteFile(temporary, yielded, 0o644) == nil
	if wrote {
		ok = os.Rename(temporary, name) == nil
	}
	if wrote && !ok {
		os.Remove(temporary)
	}
	mem.FreeString(p.Alloc, name)
	slices.Free(p.Alloc, yielded)
	if !ok {
		return failure(p.Alloc, "FS_ERR", "cannot write yielded output")
	}
	return diagnostic.Diagnostic{}
}

func effectName(kind eval.EffectKind) string {
	if kind == eval.EffectOut { return "out" }
	if kind == eval.EffectErr { return "err" }
	if kind == eval.EffectYield { return "yield" }
	return "write"
}

func (p *Program) resolveInputs(c *core.EngineContext, entry *instance) inputsResult {
	index := p.instanceIndex(entry.Node)
	hasExpression := false
	for i := range entry.Rule.Inputs {
		if entry.Rule.Inputs[i].Kind == rule.InputExpression {
			hasExpression = true
			break
		}
	}
	if !hasExpression {
		return inputsResult{Inputs: entry.Plan.Inputs, ResourceInputs: entry.Plan.ResourceInputs}
	}
	var inputs []string
	var dynamicInputs []string
	var resourceInputs []PlanInput
	for i := range entry.Rule.Inputs {
		input := entry.Rule.Inputs[i]
		if input.Kind != rule.InputExpression {
			if input.Kind == rule.InputTemplate {
				inputs = slices.Append(p.Alloc, inputs, renderInput(p.Alloc, input, entry.Captures))
			} else {
				inputs = slices.Append(p.Alloc, inputs, cloneText(p.Alloc, input.Text))
			}
			text := inputs[len(inputs)-1]
			kind := core.ResourceTarget
			if input.Kind == rule.InputPath {
				kind = core.ResourceFile
			}
			resourceInputs = slices.Append(p.Alloc, resourceInputs, PlanInput{Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, kind, text)})
			continue
		}
		if input.Template == nil || len(input.Template.Parts) != 1 || input.Template.Parts[0].Kind != template.Expression || input.Template.Parts[0].Expr == nil {
			freeStrings(p.Alloc, inputs)
			freeStrings(p.Alloc, dynamicInputs)
			freePlanInputs(p.Alloc, resourceInputs, true)
			return inputsResult{Diagnostic: failure(p.Alloc, "EXPR_INVALID", "invalid rule input expression")}
		}
		values, outputs := makeValues(p.Alloc, inputs), makeValues(p.Alloc, entry.Plan.Outputs)
		dependencyState := renderDependencyState{Program: p, Index: index, Inspection: entry.Inspection}
		context := &eval.Context{Program: p.Eval, Engine: c, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Phase: eval.ResolvingPhase, ResolverState: &dependencyState, DependencyObserver: observeRenderDependency, OperationObserver: observeRenderOperation, ToolResolver: resolveRenderTool, RuleFrames: []eval.RuleFrame{{Inputs: values, Outputs: outputs}}}
		result := p.Eval.EvaluateWith(input.Template.Parts[0].Expr, context)
		freeValues(p.Alloc, values)
		freeValues(p.Alloc, outputs)
		if context.PhaseInvalid() || len(context.Effects) != 0 {
			eval.FreeEffects(p.Alloc, context.Effects)
			result.Free(p.Alloc)
			freeStrings(p.Alloc, inputs)
			freeStrings(p.Alloc, dynamicInputs)
			freePlanInputs(p.Alloc, resourceInputs, true)
			return inputsResult{Diagnostic: failure(p.Alloc, "PHASE_INVALID", "build effects are invalid while planning")}
		}
		if result.Waiting {
			freeStrings(p.Alloc, inputs)
			freeStrings(p.Alloc, dynamicInputs)
			freePlanInputs(p.Alloc, resourceInputs, true)
			return inputsResult{Waiting: true}
		}
		if result.Diagnostic.Code != "" {
			freeStrings(p.Alloc, inputs)
			freeStrings(p.Alloc, dynamicInputs)
			freePlanInputs(p.Alloc, resourceInputs, true)
			return inputsResult{Diagnostic: result.Diagnostic}
		}
		before := len(inputs)
		if !appendInputValue(p.Alloc, &inputs, &resourceInputs, result.Value) {
			result.Value.Free(p.Alloc)
			freeStrings(p.Alloc, inputs)
			freeStrings(p.Alloc, dynamicInputs)
			freePlanInputs(p.Alloc, resourceInputs, true)
			return inputsResult{Diagnostic: failure(p.Alloc, "EXPR_INVALID", "rule input expression must produce strings, resources, lists, or nil")}
		}
		result.Value.Free(p.Alloc)
		for j := before; j < len(inputs); j++ {
			dynamicInputs = slices.Append(p.Alloc, dynamicInputs, cloneText(p.Alloc, inputs[j]))
		}
	}
	// Dependency discovery can append rule instances, invalidating the entry
	// pointer passed by the producer. Reacquire it by stable node identity.
	if index < 0 || index >= len(p.Instances) {
		freeStrings(p.Alloc, inputs)
		freeStrings(p.Alloc, dynamicInputs)
		freePlanInputs(p.Alloc, resourceInputs, true)
		return inputsResult{Diagnostic: failure(p.Alloc, "HOST_FAIL", "input resolver instance disappeared")}
	}
	entry = &p.Instances[index]
	freeStrings(p.Alloc, entry.Plan.ResolvedInputs)
	freePlanInputs(p.Alloc, entry.Plan.ResolvedResourceInputs, true)
	entry.Plan.ResolvedInputs = cloneStrings(p.Alloc, inputs)
	entry.Plan.ResolvedResourceInputs = clonePlanInputs(p.Alloc, resourceInputs)
	entry.Plan.Resolved = true
	return inputsResult{Inputs: inputs, DynamicInputs: dynamicInputs, ResourceInputs: resourceInputs, Owned: true}
}

func appendInputValue(a mem.Allocator, inputs *[]string, resourceInputs *[]PlanInput, value core.Value) bool {
	if value.Kind == core.Nil {
		return true
	}
	if value.Kind == core.String || value.Kind == core.Pattern {
		text := cloneText(a, value.Text)
		*inputs = slices.Append(a, *inputs, text)
		*resourceInputs = slices.Append(a, *resourceInputs, PlanInput{Display: cloneText(a, text), Key: core.NewResourceKey(a, core.ResourceTarget, text)})
		return true
	}
	if value.Kind == core.Resource {
		text := cloneText(a, value.Resource.Name)
		*inputs = slices.Append(a, *inputs, text)
		*resourceInputs = slices.Append(a, *resourceInputs, PlanInput{Display: cloneText(a, text), Key: value.Resource.Clone(a)})
		return true
	}
	if value.Kind != core.List {
		return false
	}
	for i := range value.List {
		if !appendInputValue(a, inputs, resourceInputs, value.List[i]) {
			return false
		}
	}
	return true
}

func cloneStrings(a mem.Allocator, values []string) []string {
	var out []string
	for i := range values {
		out = slices.Append(a, out, cloneText(a, values[i]))
	}
	return out
}
func clonePlanInputs(a mem.Allocator, values []PlanInput) []PlanInput {
	var out []PlanInput
	for i := range values {
		out = slices.Append(a, out, PlanInput{Display: cloneText(a, values[i].Display), Key: values[i].Key.Clone(a)})
	}
	return out
}

func makeValues(a mem.Allocator, names []string) []core.Value {
	var out []core.Value
	for i := range names {
		out = slices.Append(a, out, core.NewString(a, names[i]))
	}
	return out
}
func freeValues(a mem.Allocator, values []core.Value) {
	for i := range values {
		values[i].Free(a)
	}
	slices.Free(a, values)
}
func freeStrings(a mem.Allocator, values []string) {
	for i := range values {
		if values[i] != "" {
			mem.FreeString(a, values[i])
		}
	}
	if len(values) != 0 {
		slices.Free(a, values)
	}
}
func freeOwnedStrings(a mem.Allocator, values []string, owned bool) {
	if owned {
		freeStrings(a, values)
	}
}
func freePlanInputs(a mem.Allocator, values []PlanInput, owned bool) {
	if !owned {
		return
	}
	for i := range values {
		if values[i].Display != "" {
			mem.FreeString(a, values[i].Display)
		}
		values[i].Key.Free(a)
	}
	if len(values) != 0 {
		slices.Free(a, values)
	}
}

func mkdirParent(name string) bool {
	parent := path.Dir(mem.System, name)
	defer mem.FreeString(mem.System, parent)
	if parent == "." || parent == "/" {
		return true
	}
	if _, err := os.Stat(parent); err == nil {
		return true
	}
	// The recursive walk is deliberately lexical; output paths have already been normalized.
	if !mkdirParent(parent) {
		return false
	}
	if os.Mkdir(parent, 0o755) == nil {
		return true
	}
	_, err := os.Stat(parent)
	return err == nil
}
