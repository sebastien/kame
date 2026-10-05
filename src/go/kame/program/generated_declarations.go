package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

const (
	generatedBatchLimit    = 4096
	generatedDataLimit     = 1 << 20
	generatedFieldLimit    = 64 << 10
	generatedRecipeLimit   = 256
	generatedListItemLimit = 4096
)

type generatedEvaluation struct {
	Program      *Program
	Stack        []string
	Dependencies []string
}

type generatedRuleResult struct {
	Rule       *rule.Rule
	Diagnostic diagnostic.Diagnostic
}

func resolveGeneratedDefinition(value any, key core.ResourceKey, context *eval.Context) eval.Result {
	state := value.(*generatedEvaluation)
	for i := range state.Stack {
		if state.Stack[i] == key.Name {
			return eval.Result{Diagnostic: diagnostic.Diagnostic{Code: cloneText(state.Program.Alloc, "DEP_CYCLE"), Severity: diagnostic.Error, Message: cloneText(state.Program.Alloc, "generated declaration depends on a cyclic definition: "+key.Name), Owned: true}}
		}
	}
	found := false
	for i := range state.Dependencies {
		if state.Dependencies[i] == key.Name {
			found = true
			break
		}
	}
	if !found {
		state.Dependencies = slices.Append(state.Program.Alloc, state.Dependencies, cloneText(state.Program.Alloc, key.Name))
	}
	state.Stack = slices.Append(state.Program.Alloc, state.Stack, key.Name)
	result := state.Program.Eval.EvaluateDefinition(key, context)
	state.Stack = state.Stack[:len(state.Stack)-1]
	return result
}

func (p *Program) compileGeneratedDeclarations() diagnostic.Diagnostic {
	var rules []*rule.Rule
	var metadata []generatedRuleMeta
	var names []string
	for i := range p.Parsed.Items {
		item := p.Parsed.Items[i]
		if item.Kind != script.Generate {
			continue
		}
		for j := range names {
			if names[j] == item.GenerateName {
				d := generatedDiagnostic(p, item, "DEF_INVALID", "duplicate generator name: "+item.GenerateName, "")
				freeGeneratedBatch(p.Alloc, rules, metadata, names)
				return d
			}
		}
		names = slices.Append(p.Alloc, names, cloneText(p.Alloc, item.GenerateName))
		state := generatedEvaluation{Program: p}
		context := &eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: p.Alloc, Requests: p.Eval.Requests, Cwd: p.Options.Directory, Source: p.Parsed.Source.Name, Grants: p.Options.Grants, Phase: eval.PlanningPhase, ResolverState: &state, ResolveDefinition: resolveGeneratedDefinition}
		value := p.Eval.EvaluateWith(item.Expression, context)
		if value.Waiting || value.Completed || value.Stream != nil || context.PhaseInvalid() || len(context.Effects) != 0 {
			value.Free(p.Alloc)
			eval.FreeEffects(p.Alloc, context.Effects)
			freeStrings(p.Alloc, state.Stack)
			freeStrings(p.Alloc, state.Dependencies)
			d := generatedDiagnostic(p, item, "PHASE_INVALID", "generated declarations must be a complete pure value", "")
			freeGeneratedBatch(p.Alloc, rules, metadata, names)
			return d
		}
		eval.FreeEffects(p.Alloc, context.Effects)
		if value.Diagnostic.Code != "" {
			d := diagnosticFailureClone(p.Alloc, value.Diagnostic)
			value.Free(p.Alloc)
			freeStrings(p.Alloc, state.Stack)
			freeStrings(p.Alloc, state.Dependencies)
			freeGeneratedBatch(p.Alloc, rules, metadata, names)
			return d
		}
		if value.Value.Kind != core.List || len(value.Value.List) > generatedBatchLimit {
			value.Free(p.Alloc)
			freeStrings(p.Alloc, state.Stack)
			freeStrings(p.Alloc, state.Dependencies)
			d := generatedDiagnostic(p, item, "EXPR_INVALID", "generated declaration expression must return a bounded list of records", "")
			freeGeneratedBatch(p.Alloc, rules, metadata, names)
			return d
		}
		batchBytes := 0
		batchItems := 0
		for j := range value.Value.List {
			lowered := p.lowerGeneratedRule(item, value.Value.List[j], &batchBytes, &batchItems)
			if lowered.Diagnostic.Code != "" {
				d := lowered.Diagnostic
				lowered.Diagnostic = diagnostic.Diagnostic{}
				value.Free(p.Alloc)
				freeStrings(p.Alloc, state.Stack)
				freeStrings(p.Alloc, state.Dependencies)
				freeGeneratedBatch(p.Alloc, rules, metadata, names)
				return d
			}
			if p.generatedTargetExists(lowered.Rule, rules) {
				d := generatedDiagnostic(p, item, "TGT_AMBIG", "generated target duplicates an existing rule", lowered.Rule.Outputs[0].Text)
				freeGeneratedRule(p.Alloc, lowered.Rule)
				value.Free(p.Alloc)
				freeStrings(p.Alloc, state.Stack)
				freeStrings(p.Alloc, state.Dependencies)
				freeGeneratedBatch(p.Alloc, rules, metadata, names)
				return d
			}
			rules = slices.Append(p.Alloc, rules, lowered.Rule)
			dependencies := cloneStrings(p.Alloc, state.Dependencies)
			metadata = slices.Append(p.Alloc, metadata, generatedRuleMeta{Rule: lowered.Rule, Name: cloneText(p.Alloc, item.GenerateName), Dependencies: dependencies})
		}
		value.Free(p.Alloc)
		freeStrings(p.Alloc, state.Stack)
		freeStrings(p.Alloc, state.Dependencies)
	}
	for i := range rules {
		p.GeneratedRules = slices.Append(p.Alloc, p.GeneratedRules, rules[i])
		p.GeneratedRuleMeta = slices.Append(p.Alloc, p.GeneratedRuleMeta, metadata[i])
		p.Rules = slices.Append(p.Alloc, p.Rules, registeredRule{Rule: rules[i]})
	}
	slices.Free(p.Alloc, rules)
	slices.Free(p.Alloc, metadata)
	freeStrings(p.Alloc, names)
	return diagnostic.Diagnostic{}
}

func freeGeneratedBatch(a mem.Allocator, rules []*rule.Rule, metadata []generatedRuleMeta, names []string) {
	for i := range rules {
		freeGeneratedRule(a, rules[i])
	}
	slices.Free(a, rules)
	for i := range metadata {
		mem.FreeString(a, metadata[i].Name)
		freeStrings(a, metadata[i].Dependencies)
	}
	slices.Free(a, metadata)
	freeStrings(a, names)
}

func freeGeneratedRule(a mem.Allocator, generated *rule.Rule) {
	if generated == nil {
		return
	}
	for i := range generated.Outputs {
		mem.FreeString(a, generated.Outputs[i].Text)
	}
	for i := range generated.Inputs {
		mem.FreeString(a, generated.Inputs[i].Text)
	}
	for i := range generated.Body {
		mem.FreeString(a, generated.Body[i].Text)
	}
	rule.FreeRule(a, generated)
}

func (p *Program) lowerGeneratedRule(item script.ScriptItem, value core.Value, total *int, itemCount *int) generatedRuleResult {
	if value.Kind != core.Record {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated rule must be a record", "")}
	}
	var kind, target string
	var inputs, orderOnly, recipes []string
	seen := 0
	for i := range value.Record {
		field := value.Record[i]
		index := -1
		switch field.Key {
		case "kind":
			index = 0
		case "target":
			index = 1
		case "inputs":
			index = 2
		case "order-only":
			index = 3
		case "recipe":
			index = 4
		default:
			return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "unknown generated rule field: "+field.Key, "")}
		}
		bit := int64(1) << index
		if seen&int(bit) != 0 {
			return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "duplicate generated rule field: "+field.Key, "")}
		}
		seen |= int(bit)
		if index == 0 || index == 1 {
			if field.Value.Kind != core.String {
				return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated rule field must be a string: "+field.Key, "")}
			}
			if index == 0 {
				kind = field.Value.Text
			} else {
				target = field.Value.Text
			}
		} else {
			if field.Value.Kind != core.List {
				return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated rule field must be a list: "+field.Key, target)}
			}
			*itemCount += len(field.Value.List)
			if *itemCount > generatedListItemLimit {
				return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated batch exceeds its list item limit", target)}
			}
			values := make([]string, 0, len(field.Value.List))
			for j := range field.Value.List {
				text := field.Value.List[j]
				if text.Kind != core.String {
					return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated rule list fields must contain strings: "+field.Key, target)}
				}
				if len(text.Text) > generatedFieldLimit || containsNUL(text.Text) {
					return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated rule field exceeds its limit or contains NUL: "+field.Key, target)}
				}
				*total += len(text.Text)
				if *total > generatedDataLimit {
					return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated batch exceeds its retained data limit", target)}
				}
				values = append(values, text.Text)
			}
			if index == 2 {
				inputs = values
			} else if index == 3 {
				orderOnly = values
			} else {
				recipes = values
			}
		}
	}
	if seen != 31 {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated rule is missing a required field", target)}
	}
	if len(target) == 0 || len(target) > generatedFieldLimit || containsNUL(target) || !validGeneratedTarget(target) {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "invalid generated target", target)}
	}
	if kind != "file" && kind != "task" && kind != "cached-task" {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "unknown generated rule kind", target)}
	}
	if kind == "file" && !isFileName(target) {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated file rule target must be a path", target)}
	}
	if kind != "file" && isFileName(target) {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated task target must be a name", target)}
	}
	if len(recipes) > generatedRecipeLimit {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated rule exceeds the recipe line limit", target)}
	}
	*total += len(target) + len(kind)
	if *total > generatedDataLimit {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated batch exceeds its retained data limit", target)}
	}
	for i := range inputs {
		if !validGeneratedInput(inputs[i]) {
			return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "invalid generated input", target)}
		}
	}
	for i := range orderOnly {
		if !validGeneratedInput(orderOnly[i]) {
			return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "invalid generated order-only input", target)}
		}
	}
	if kind == "file" && len(recipes) == 0 {
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated file rule requires a recipe", target)}
	}
	generated := mem.Alloc[rule.Rule](p.Alloc)
	generated.Kind = rule.TaskRule
	if kind == "file" {
		generated.Kind = rule.FileRule
	}
	if kind == "cached-task" {
		generated.Kind = rule.CachedTaskRule
	}
	generated.Span, generated.Header = source.Span{Start: item.Span.Start, End: item.Span.End}, source.Span{Start: item.Span.Start, End: item.Span.End}
	targetPath := isFileName(target)
	targetKind := rule.TargetName
	if targetPath {
		targetKind = rule.TargetPath
	}
	generated.Outputs = slices.Append(p.Alloc, generated.Outputs, rule.Target{Kind: targetKind, Text: cloneText(p.Alloc, target), Span: generated.Header, Path: targetPath})
	for i := range inputs {
		kind := rule.InputName
		if isFileName(inputs[i]) {
			kind = rule.InputPath
		}
		generated.Inputs = slices.Append(p.Alloc, generated.Inputs, rule.Input{Kind: kind, Text: cloneText(p.Alloc, inputs[i]), Span: generated.Header})
	}
	for i := range orderOnly {
		kind := rule.InputName
		if isFileName(orderOnly[i]) {
			kind = rule.InputPath
		}
		generated.Inputs = slices.Append(p.Alloc, generated.Inputs, rule.Input{OrderOnly: true, Kind: kind, Text: cloneText(p.Alloc, orderOnly[i]), Span: generated.Header})
	}
	for i := range recipes {
		if len(recipes[i]) > generatedFieldLimit || containsNUL(recipes[i]) {
			freeGeneratedRule(p.Alloc, generated)
			return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated recipe field exceeds its limit or contains NUL", target)}
		}
		*total += len(recipes[i])
		if *total > generatedDataLimit {
			freeGeneratedRule(p.Alloc, generated)
			return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated batch exceeds its retained data limit", target)}
		}
		templateValue := template.ParseString(p.Alloc, p.Parsed.Source.Name, recipes[i])
		if len(templateValue.Diagnostics) != 0 {
			templateValue.Free()
			freeGeneratedRule(p.Alloc, generated)
			return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "PARSE_ERR", "invalid generated recipe template", target)}
		}
		generated.Body = slices.Append(p.Alloc, generated.Body, rule.RecipeLine{Text: cloneText(p.Alloc, recipes[i]), Span: generated.Header, Template: templateValue})
		p.declareTemplateTools(templateValue)
	}
	if *total > generatedDataLimit {
		freeGeneratedRule(p.Alloc, generated)
		return generatedRuleResult{Diagnostic: generatedDiagnostic(p, item, "EXPR_INVALID", "generated batch exceeds its retained data limit", target)}
	}
	return generatedRuleResult{Rule: generated}
}

func (p *Program) generatedTargetExists(candidate *rule.Rule, pending []*rule.Rule) bool {
	target := candidate.Outputs[0]
	selected := p.selectRule(target.Text)
	exists := selected.Rule != nil || selected.Ambiguous
	mem.FreeString(p.Alloc, selected.Target)
	freeCaptures(p.Alloc, selected.Captures)
	freeArguments(p.Alloc, selected.Arguments)
	selected.Diagnostic.Free(p.Alloc)
	if exists {
		return true
	}
	key := cloneText(p.Alloc, target.Text)
	file := target.Path
	if file {
		mem.FreeString(p.Alloc, key)
		key = p.canonicalTarget(target.Text, true)
	}
	defer mem.FreeString(p.Alloc, key)
	for i := range p.Rules {
		if p.Rules[i].Rule == nil {
			continue
		}
		for j := range p.Rules[i].Rule.Outputs {
			output := p.Rules[i].Rule.Outputs[j]
			if output.Template {
				continue
			}
			other := output.Text
			if output.Path {
				other = p.canonicalTarget(other, true)
			}
			equal := key == other
			if output.Path {
				mem.FreeString(p.Alloc, other)
			}
			if equal {
				return true
			}
		}
	}
	for i := range pending {
		for j := range pending[i].Outputs {
			output := pending[i].Outputs[j]
			other := output.Text
			if output.Path {
				other = p.canonicalTarget(other, true)
			}
			equal := key == other
			if output.Path {
				mem.FreeString(p.Alloc, other)
			}
			if equal {
				return true
			}
		}
	}
	return false
}

func generatedDiagnostic(p *Program, item script.ScriptItem, code string, message string, target string) diagnostic.Diagnostic {
	d := failureAt(p.Alloc, code, diagnostic.Span{Start: item.Span.Start, End: item.Span.End}, message)
	d.Source = cloneText(p.Alloc, p.Parsed.Source.Name)
	if target != "" {
		d.Notes = slices.Append(p.Alloc, d.Notes, cloneText(p.Alloc, "generated target: "+target))
	}
	return d
}

func validGeneratedTarget(target string) bool {
	if isFileName(target) {
		return true
	}
	if len(target) == 0 {
		return false
	}
	for i := range target {
		b := target[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || (i > 0 && ((b >= '0' && b <= '9') || b == '-' || b == '!' || b == '?')) {
			continue
		}
		return false
	}
	return true
}

func validGeneratedInput(input string) bool {
	return input != "" && len(input) <= generatedFieldLimit && !containsNUL(input)
}

func containsNUL(value string) bool {
	for i := range value {
		if value[i] == 0 {
			return true
		}
	}
	return false
}

func diagnosticFailureClone(a mem.Allocator, value diagnostic.Diagnostic) diagnostic.Diagnostic {
	return value.Clone(a)
}
