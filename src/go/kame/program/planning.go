package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func (p *Program) Plan(target string) PlanResult {
	selected := p.selectRule(target)
	if selected.Ambiguous {
		return PlanResult{Diagnostic: failure(p.Alloc, "TGT_AMBIG", "multiple rules match target")}
	}
	if selected.Rule == nil {
		if p.Eval.Definition(target) != nil {
			return PlanResult{Plan: Plan{Target: cloneText(p.Alloc, target), Key: core.NewResourceKey(p.Alloc, core.ResourceDefinition, target), Freshness: Unknown}}
		}
		return PlanResult{Diagnostic: failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+target)}
	}
	plan := Plan{Target: cloneText(p.Alloc, target), Rule: selected.Rule, RuleSpan: diagnostic.Span{Start: selected.Rule.Span.Start, End: selected.Rule.Span.End}, Body: selected.Rule.Body, Captures: cloneCaptures(p.Alloc, selected.Captures), Freshness: Unknown}
	defer freeCaptures(p.Alloc, selected.Captures)
	if selected.Rule.Kind == rule.FileRule {
		canonical := p.canonicalTarget(target, true)
		plan.Key = core.NewResourceKey(p.Alloc, resourceKind(selected.Rule), canonical)
		mem.FreeString(p.Alloc, canonical)
	} else {
		plan.Key = core.NewResourceKey(p.Alloc, resourceKind(selected.Rule), target)
	}
	for i := range selected.Rule.Outputs {
		output := renderTarget(p.Alloc, selected.Rule.Outputs[i], selected.Captures)
		plan.Outputs = slices.Append(p.Alloc, plan.Outputs, output)
	}
	for i := range plan.Outputs {
		for j := 0; j < i; j++ {
			if plan.Outputs[i] == plan.Outputs[j] {
				plan.Free(p.Alloc)
				return PlanResult{Diagnostic: failureAt(p.Alloc, "PARSE_ERR", diagnostic.Span{Start: selected.Rule.Span.Start, End: selected.Rule.Span.End}, "rule outputs resolve to the same path")}
			}
		}
	}
	for i := range selected.Rule.Inputs {
		input := selected.Rule.Inputs[i]
		if input.Kind == rule.InputExpression {
			plan.Freshness = Unknown
			before := len(plan.Inputs)
			if d := p.planInputExpression(input, &plan); d.Code != "" {
				plan.Free(p.Alloc)
				return PlanResult{Diagnostic: d}
			}
			for j := before; j < len(plan.Inputs); j++ {
				plan.DynamicInputs = slices.Append(p.Alloc, plan.DynamicInputs, cloneText(p.Alloc, plan.Inputs[j]))
			}
			continue
		}
		if input.Kind == rule.InputTemplate {
			text := renderInput(p.Alloc, input, selected.Captures)
			plan.Inputs = slices.Append(p.Alloc, plan.Inputs, text)
			plan.StaticInputs = slices.Append(p.Alloc, plan.StaticInputs, cloneText(p.Alloc, text))
			plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, core.ResourceTarget, text)})
			continue
		}
		text := cloneText(p.Alloc, input.Text)
		plan.Inputs = slices.Append(p.Alloc, plan.Inputs, text)
		plan.StaticInputs = slices.Append(p.Alloc, plan.StaticInputs, cloneText(p.Alloc, text))
		kind := core.ResourceTarget
		if input.Kind == rule.InputPath {
			kind = core.ResourceFile
		}
		plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, kind, text)})
	}
	if selected.Rule.Kind == rule.FileRule && len(selected.Rule.Body) == 0 {
		plan.Freshness = p.freshness(&plan, nil)
	} else {
		plan.Freshness = Unknown
	}
	return PlanResult{Plan: plan}
}

// ExpandPlan resolves expression-form inputs through the engine without
// rendering or running a recipe. It is intended for graph inspection: unlike
// Plan, it may perform granted read-only host operations such as wildcard.
func (p *Program) ExpandPlan(target string) PlanResult {
	return p.expandPlan(target, false)
}

// expandPlan may yield to an embedding host while retaining inspection interest.
// Retrying the query resumes the same node; no recipe producer is instantiated.
func (p *Program) expandPlan(target string, yield bool) PlanResult {
	result := p.Plan(target)
	if result.Diagnostic.Code != "" || result.Plan.Rule == nil || !hasExpressionInput(result.Plan.Rule) {
		return result
	}
	// Definitions reached while expanding an input must resolve filesystem
	// resources as external values, never by instantiating rule producers.
	previousObserver, previousState := p.Eval.DefinitionDependencyObserver, p.Eval.DefinitionDependencyState
	p.Eval.SetDefinitionDependencyObserver(observeInspectionDependency, p)
	defer p.Eval.SetDefinitionDependencyObserver(previousObserver, previousState)
	index := len(p.Instances)
	var node *core.Node
	for i := range p.Instances {
		if p.Instances[i].Inspection && p.Instances[i].Plan.Target == target {
			index, node = i, p.Instances[i].Node
			break
		}
	}
	if node == nil {
		state := mem.Alloc[instanceState](p.Alloc)
		state.Program, state.Index = p, index
		keyName := "\x00span:" + target
		key := core.NewResourceKey(p.Alloc, core.ResourceTarget, keyName)
		node = p.Engine.AddOwned(key, expandPlanProduce, state, freeInstanceState)
		key.Free(p.Alloc)
		if node == nil {
			mem.Free(p.Alloc, state)
			result.Plan.Free(p.Alloc)
			return PlanResult{Diagnostic: failure(p.Alloc, "HOST_FAIL", "cannot create input resolver")}
		}
		p.Instances = slices.Append(p.Alloc, p.Instances, instance{Rule: result.Plan.Rule, Captures: cloneCaptures(p.Alloc, result.Plan.Captures), Node: node, Plan: clonePlan(p.Alloc, result.Plan), Inspection: true})
	}
	if p.Instances[index].inspectionRoot == nil {
		p.Instances[index].inspectionRoot = p.Engine.RequestRoot(node)
	}
	for node.State != core.NodeComplete && node.State != core.NodeFailed && node.State != core.NodeCancelled {
		p.Tick(0)
		if yield && len(p.Outbound) != 0 {
			result.Plan.Free(p.Alloc)
			return PlanResult{Waiting: true}
		}
	}
	p.Engine.Release(p.Instances[index].inspectionRoot)
	p.Instances[index].inspectionRoot = nil
	if node.State != core.NodeComplete {
		result.Plan.Free(p.Alloc)
		return PlanResult{Diagnostic: node.Diagnostic.Clone(p.Alloc)}
	}
	freeStrings(p.Alloc, result.Plan.DynamicInputs)
	result.Plan.DynamicInputs = cloneStrings(p.Alloc, p.Instances[index].Plan.DynamicInputs)
	result.Plan.Freshness = Unknown
	return result
}

func hasExpressionInput(r *rule.Rule) bool {
	for i := range r.Inputs {
		if r.Inputs[i].Kind == rule.InputExpression {
			return true
		}
	}
	return false
}

func expandPlanProduce(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*instanceState)
	p := state.Program
	if state.Index < 0 || state.Index >= len(p.Instances) {
		c.Fail(failure(p.Alloc, "HOST_FAIL", "input resolver instance disappeared"))
		return core.ProducerFailed
	}
	resolved := p.resolveInputs(c, &p.Instances[state.Index])
	if resolved.Waiting {
		return core.ProducerWaiting
	}
	if resolved.Diagnostic.Code != "" {
		c.Fail(resolved.Diagnostic)
		return core.ProducerFailed
	}
	entry := &p.Instances[state.Index]
	freeStrings(p.Alloc, entry.Plan.DynamicInputs)
	entry.Plan.DynamicInputs = cloneStrings(p.Alloc, resolved.DynamicInputs)
	freeOwnedStrings(p.Alloc, resolved.Inputs, resolved.Owned)
	freeStrings(p.Alloc, resolved.DynamicInputs)
	freePlanInputs(p.Alloc, resolved.ResourceInputs, resolved.Owned)
	return core.ProducerCompleted
}

func clonePlan(a mem.Allocator, plan Plan) Plan {
	clone := Plan{Target: cloneText(a, plan.Target), Key: plan.Key.Clone(a), Rule: plan.Rule, RuleSpan: plan.RuleSpan, Body: plan.Body, Captures: cloneCaptures(a, plan.Captures), Inputs: cloneStrings(a, plan.Inputs), StaticInputs: cloneStrings(a, plan.StaticInputs), DynamicInputs: cloneStrings(a, plan.DynamicInputs), ResourceInputs: clonePlanInputs(a, plan.ResourceInputs), ResolvedInputs: cloneStrings(a, plan.ResolvedInputs), ResolvedResourceInputs: clonePlanInputs(a, plan.ResolvedResourceInputs), Resolved: plan.Resolved, Outputs: cloneStrings(a, plan.Outputs), Freshness: plan.Freshness}
	return clone
}

func (p *Program) planInputExpression(input rule.Input, plan *Plan) diagnostic.Diagnostic {
	if input.Template == nil || len(input.Template.Parts) != 1 || input.Template.Parts[0].Kind != template.Expression || input.Template.Parts[0].Expr == nil {
		return failure(p.Alloc, "EXPR_INVALID", "invalid rule input expression")
	}
	inputs, outputs := makeValues(p.Alloc, plan.Inputs), makeValues(p.Alloc, plan.Outputs)
	defer freeValues(p.Alloc, inputs)
	defer freeValues(p.Alloc, outputs)
	state := planResolverState{Program: p}
	context := &eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Phase: eval.PlanningPhase, ResolveDefinition: resolvePlanDefinition, ResolverState: &state, RuleFrames: []eval.RuleFrame{{Inputs: inputs, Outputs: outputs}}}
	result := p.Eval.EvaluateWith(input.Template.Parts[0].Expr, context)
	if state.Resolving != nil {
		slices.Free(p.Alloc, state.Resolving)
	}
	if context.PhaseInvalid() || len(context.Effects) != 0 {
		eval.FreeEffects(p.Alloc, context.Effects)
		if result.Diagnostic.Code != "" {
			d := result.Diagnostic
			result.Diagnostic = diagnostic.Diagnostic{}
			result.Free(p.Alloc)
			return d
		}
		result.Free(p.Alloc)
		return failure(p.Alloc, "PHASE_INVALID", "build effects are invalid while planning")
	}
	if result.Waiting {
		result.Free(p.Alloc)
		// Inspection remains side-effect free. A host-backed input is resolved by
		// the execution producer, so planning reports unknown freshness instead of
		// misclassifying a valid deferred dependency as a phase error.
		return diagnostic.Diagnostic{}
	}
	if result.Diagnostic.Code != "" {
		return result.Diagnostic
	}
	if !appendPlanInputValue(p.Alloc, plan, result.Value) {
		result.Value.Free(p.Alloc)
		return failure(p.Alloc, "EXPR_INVALID", "rule input expression must produce strings, resources, lists, or nil")
	}
	result.Value.Free(p.Alloc)
	return diagnostic.Diagnostic{}
}

func resolvePlanDefinition(value any, key core.ResourceKey, context *eval.Context) eval.Result {
	state := value.(*planResolverState)
	for i := range state.Resolving {
		if state.Resolving[i] == key.Name {
			return eval.Result{Diagnostic: failure(state.Program.Alloc, "DEP_CYCLE", "definition cycle")}
		}
	}
	state.Resolving = slices.Append(state.Program.Alloc, state.Resolving, key.Name)
	result := state.Program.Eval.EvaluateDefinition(key, context)
	state.Resolving = state.Resolving[:len(state.Resolving)-1]
	return result
}

func appendPlanInputValue(a mem.Allocator, plan *Plan, value core.Value) bool {
	if value.Kind == core.Nil {
		return true
	}
	if value.Kind == core.String || value.Kind == core.Pattern {
		text := cloneText(a, value.Text)
		plan.Inputs = slices.Append(a, plan.Inputs, text)
		plan.ResourceInputs = slices.Append(a, plan.ResourceInputs, PlanInput{Display: cloneText(a, text), Key: core.NewResourceKey(a, core.ResourceTarget, text)})
		return true
	}
	if value.Kind == core.Resource {
		text := cloneText(a, value.Resource.Name)
		plan.Inputs = slices.Append(a, plan.Inputs, text)
		plan.ResourceInputs = slices.Append(a, plan.ResourceInputs, PlanInput{Display: cloneText(a, text), Key: value.Resource.Clone(a)})
		return true
	}
	if value.Kind != core.List {
		return false
	}
	for i := range value.List {
		if !appendPlanInputValue(a, plan, value.List[i]) {
			return false
		}
	}
	return true
}

func resourceKind(r *rule.Rule) core.ResourceKind {
	if r.Kind == rule.FileRule {
		return core.ResourceFile
	}
	if r.Kind == rule.CachedTaskRule {
		return core.ResourceTask
	}
	if r.Kind == rule.ServiceRule {
		return core.ResourceService
	}
	return core.ResourceTarget
}
