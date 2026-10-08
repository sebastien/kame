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
	if selected.Diagnostic.Code != "" {
		freeCaptures(p.Alloc, selected.Captures)
		freeArguments(p.Alloc, selected.Arguments)
		mem.FreeString(p.Alloc, selected.Target)
		return PlanResult{Diagnostic: selected.Diagnostic}
	}
	if selected.Ambiguous {
		mem.FreeString(p.Alloc, selected.Target)
		return PlanResult{Diagnostic: failure(p.Alloc, "TGT_AMBIG", "multiple rules match target")}
	}
	if selected.Rule == nil {
		mem.FreeString(p.Alloc, selected.Target)
		if p.Eval.Definition(target) != nil {
			return PlanResult{Plan: Plan{Tools: p.plannedTools(), Configuration: cloneStrings(p.Alloc, p.Configuration), Target: cloneText(p.Alloc, target), Key: core.NewResourceKey(p.Alloc, core.ResourceDefinition, target), Freshness: Unknown}}
		}
		return PlanResult{Diagnostic: failure(p.Alloc, "TGT_NO_RULE", "no rule for target: "+target)}
	}
	plan := Plan{Tools: p.plannedTools(), Configuration: cloneStrings(p.Alloc, p.Configuration), Target: cloneText(p.Alloc, target), Rule: selected.Rule, RuleSpan: diagnostic.Span{Start: selected.Rule.Span.Start, End: selected.Rule.Span.End}, Body: selected.Rule.Body, Captures: cloneCaptures(p.Alloc, selected.Captures), Arguments: cloneArguments(p.Alloc, selected.Arguments), Freshness: Unknown}
	for i := range p.GeneratedRuleMeta {
		if p.GeneratedRuleMeta[i].Rule == selected.Rule {
			plan.Generator = cloneText(p.Alloc, p.GeneratedRuleMeta[i].Name)
			plan.GeneratorDependencies = cloneStrings(p.Alloc, p.GeneratedRuleMeta[i].Dependencies)
			break
		}
	}
	defer freeCaptures(p.Alloc, selected.Captures)
	defer freeArguments(p.Alloc, selected.Arguments)
	defer mem.FreeString(p.Alloc, selected.Target)
	if selected.Rule.Kind == rule.FileRule {
		canonical := p.canonicalTarget(target, true)
		plan.Key = core.NewResourceKey(p.Alloc, resourceKind(selected.Rule), canonical)
		mem.FreeString(p.Alloc, canonical)
	} else {
		keyName := targetArgumentKey(p.Alloc, selected.Target, selected.Arguments)
		plan.Key = core.NewResourceKey(p.Alloc, resourceKind(selected.Rule), keyName)
		mem.FreeString(p.Alloc, keyName)
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
		if input.Kind == rule.InputExpression || input.Kind == rule.InputString || input.Kind == rule.InputWildcard {
			plan.Freshness = Unknown
			before := len(plan.Inputs)
			if d := p.planInputExpression(input, &plan); d.Code != "" {
				plan.Free(p.Alloc)
				return PlanResult{Diagnostic: d}
			}
			for j := before; j < len(plan.ResourceInputs); j++ {
				plan.ResourceInputs[j].OrderOnly = input.OrderOnly
				plan.ResourceInputs[j].Computed = true
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
			plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{OrderOnly: input.OrderOnly, SequenceEnd: input.SequenceEnd, Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, core.ResourceTarget, text)})
			continue
		}
		text := cloneText(p.Alloc, input.Text)
		plan.Inputs = slices.Append(p.Alloc, plan.Inputs, text)
		plan.StaticInputs = slices.Append(p.Alloc, plan.StaticInputs, cloneText(p.Alloc, text))
		kind := core.ResourceTarget
		if input.Kind == rule.InputPath {
			kind = core.ResourceFile
		}
		plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{OrderOnly: input.OrderOnly, SequenceEnd: input.SequenceEnd, Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, kind, text)})
	}
	// Planning does not validate accepted records or resource bytes.
	plan.Freshness = Unknown
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
	return p.expandReadOnlyPlan(target, yield, false)
}

func (p *Program) expandReadOnlyPlan(target string, yield bool, definitions bool) PlanResult {
	result := p.Plan(target)
	if result.Diagnostic.Code != "" || (result.Plan.Rule == nil && !definitions) || (result.Plan.Rule != nil && !hasExpressionInput(result.Plan.Rule)) {
		return result
	}
	index := len(p.Instances)
	var node *core.Node
	for i := range p.Instances {
		if p.Instances[i].Inspection && p.Instances[i].InspectionStatus == nil && sameInspectionPlan(p.Instances[i].Plan, result.Plan) {
			index, node = i, p.Instances[i].Node
			break
		}
	}
	if node == nil {
		state := mem.Alloc[instanceState](p.Alloc)
		state.Program, state.Index = p, index
		keyName := "\x00inspection-input:" + target
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
	if p.Instances[index].inspectionRoot == nil && node.State != core.NodeComplete && node.State != core.NodeFailed && node.State != core.NodeCancelled {
		p.Instances[index].inspectionRoot = p.Engine.RequestRoot(node)
	}
	for node.State != core.NodeComplete && node.State != core.NodeFailed && node.State != core.NodeCancelled {
		p.Tick(0)
		if yield && len(p.Outbound) != 0 {
			result.Plan.Free(p.Alloc)
			return PlanResult{Waiting: true}
		}
	}
	if p.Instances[index].inspectionRoot != nil {
		p.Engine.Release(p.Instances[index].inspectionRoot)
		p.Instances[index].inspectionRoot = nil
	}
	if node.State != core.NodeComplete {
		result.Plan.Free(p.Alloc)
		return PlanResult{Diagnostic: node.Diagnostic.Clone(p.Alloc)}
	}
	freeStrings(p.Alloc, result.Plan.DynamicInputs)
	result.Plan.DynamicInputs = cloneStrings(p.Alloc, p.Instances[index].Plan.DynamicInputs)
	if result.Plan.Rule != nil {
		freeStrings(p.Alloc, result.Plan.Inputs)
		freePlanInputs(p.Alloc, result.Plan.ResourceInputs, true)
		result.Plan.Inputs = cloneStrings(p.Alloc, p.Instances[index].Plan.ResolvedInputs)
		result.Plan.ResourceInputs = clonePlanInputs(p.Alloc, p.Instances[index].Plan.ResolvedResourceInputs)
	}
	result.Plan.Freshness = Unknown
	freeTools(p.Alloc, result.Plan.Tools)
	result.Plan.Tools = p.plannedTools()
	return result
}

func hasExpressionInput(r *rule.Rule) bool {
	for i := range r.Inputs {
		if r.Inputs[i].Kind == rule.InputExpression || r.Inputs[i].Kind == rule.InputString || r.Inputs[i].Kind == rule.InputWildcard {
			return true
		}
	}
	return false
}

func sameInspectionPlan(left, right Plan) bool {
	if left.Rule != right.Rule { return false }
	if left.Rule == nil { return left.Key.Name == right.Key.Name }
	return sameCaptures(left.Captures, right.Captures) && sameArguments(left.Arguments, right.Arguments)
}

func expandPlanProduce(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*instanceState)
	p := state.Program
	if state.Index < 0 || state.Index >= len(p.Instances) {
		c.Fail(failure(p.Alloc, "HOST_FAIL", "input resolver instance disappeared"))
		return core.ProducerFailed
	}
	if p.Instances[state.Index].Rule == nil {
		context := eval.Context{Phase: eval.ResolvingPhase, Cwd: p.Options.Directory, Environment: p.Options.Environment, HasEnvironment: p.Options.Environment != nil}
		p.bindDefinitionEnvironment(&context)
		definition := p.Eval.DefinitionWith(p.Instances[state.Index].Plan.Target, &context)
		if definition == nil { c.Fail(failure(p.Alloc, "TGT_NO_RULE", "no definition")); return core.ProducerFailed }
		if !c.Dependency(definition.Key) { return core.ProducerWaiting }
		return core.ProducerCompleted
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

func restoreInspectionContextObserver(p *Program, observer func(any, core.ResourceKey, *eval.Context)) {
	p.Eval.DefinitionDependencyContextObserver = observer
}

func (p *Program) tickInspection(wait int) {
	previousObserver, previousState := p.Eval.DefinitionDependencyObserver, p.Eval.DefinitionDependencyState
	previousContextObserver := p.Eval.DefinitionDependencyContextObserver
	p.Eval.SetDefinitionDependencyObserver(observeInspectionDependency, p)
	p.Eval.DefinitionDependencyContextObserver = nil
	defer p.Eval.SetDefinitionDependencyObserver(previousObserver, previousState)
	defer restoreInspectionContextObserver(p, previousContextObserver)
	p.tick(wait)
}

func clonePlan(a mem.Allocator, plan Plan) Plan {
	clone := Plan{Tools: cloneTools(a, plan.Tools), Configuration: cloneStrings(a, plan.Configuration), Target: cloneText(a, plan.Target), Generator: cloneText(a, plan.Generator), GeneratorDependencies: cloneStrings(a, plan.GeneratorDependencies), Key: plan.Key.Clone(a), Rule: plan.Rule, RuleSpan: plan.RuleSpan, Body: plan.Body, Captures: cloneCaptures(a, plan.Captures), Arguments: cloneArguments(a, plan.Arguments), Inputs: cloneStrings(a, plan.Inputs), StaticInputs: cloneStrings(a, plan.StaticInputs), DynamicInputs: cloneStrings(a, plan.DynamicInputs), ResourceInputs: clonePlanInputs(a, plan.ResourceInputs), ResolvedInputs: cloneStrings(a, plan.ResolvedInputs), ResolvedResourceInputs: clonePlanInputs(a, plan.ResolvedResourceInputs), Resolved: plan.Resolved, Outputs: cloneStrings(a, plan.Outputs), Freshness: plan.Freshness}
	return clone
}

func (p *Program) planInputExpression(input rule.Input, plan *Plan) diagnostic.Diagnostic {
	if input.Template == nil || (input.Kind == rule.InputExpression && (len(input.Template.Parts) != 1 || input.Template.Parts[0].Kind != template.Expression || input.Template.Parts[0].Expr == nil)) {
		return failure(p.Alloc, "EXPR_INVALID", "invalid rule input expression")
	}
	inputs, outputs := makeRuleInputValues(p.Alloc, plan.Inputs, plan.ResourceInputs), makeValues(p.Alloc, plan.Outputs)
	defer freeValues(p.Alloc, inputs)
	defer freeValues(p.Alloc, outputs)
	state := planResolverState{Program: p}
	context := &eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: p.Alloc, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Args: p.Eval.DefinitionArgs, HasArgs: p.Eval.DefinitionArgsSet, Phase: eval.PlanningPhase, ResolveDefinition: resolvePlanDefinition, ResolverState: &state, RuleFrames: []eval.RuleFrame{{Inputs: inputs, Outputs: outputs}}}
	scope := p.ruleScope(context, plan.Captures, plan.Arguments)
	context.Scope = scope
	result := p.evaluateInput(input, context)
	scope.Free()
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
			d := failure(state.Program.Alloc, "DEP_CYCLE", "definition cycle")
			for j := i; j < len(state.Resolving); j++ { d.TargetStack = slices.Append(state.Program.Alloc, d.TargetStack, cloneText(state.Program.Alloc, state.Resolving[j])) }
			d.TargetStack = slices.Append(state.Program.Alloc, d.TargetStack, cloneText(state.Program.Alloc, key.Name))
			return eval.Result{Diagnostic: d}
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

// Complete expression inputs retain their value shape for list flattening;
// interpolated path tokens render one string through the shared template engine.
func (p *Program) evaluateInput(input rule.Input, context *eval.Context) eval.Result {
	if input.Kind == rule.InputString {
		return p.Eval.Render(p.Alloc, input.Template, context.Scope, context)
	}
	return p.Eval.EvaluateWith(input.Template.Parts[0].Expr, context)
}
