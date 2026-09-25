package program

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lang/eval"
	"littlemake/lang/rule"
	"littlemake/lang/template"
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
			if d := p.planInputExpression(input, &plan); d.Code != "" {
				plan.Free(p.Alloc)
				return PlanResult{Diagnostic: d}
			}
			continue
		}
		if input.Kind == rule.InputTemplate {
			text := renderInput(p.Alloc, input, selected.Captures)
			plan.Inputs = slices.Append(p.Alloc, plan.Inputs, text)
			plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, core.ResourceTarget, text)})
			continue
		}
		text := cloneText(p.Alloc, input.Text)
		plan.Inputs = slices.Append(p.Alloc, plan.Inputs, text)
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
		result.Free(p.Alloc)
		return failure(p.Alloc, "PHASE_INVALID", "build effects are invalid while planning")
	}
	if result.Waiting {
		result.Free(p.Alloc)
		return failure(p.Alloc, "PHASE_INVALID", "operation is invalid while planning")
	}
	if result.Diagnostic.Code != "" {
		return result.Diagnostic
	}
	if !appendPlanInputValue(p.Alloc, plan, result.Value) {
		result.Value.Free(p.Alloc)
		return failure(p.Alloc, "INPUT_INVALID", "rule input expression must produce strings, resources, lists, or nil")
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
