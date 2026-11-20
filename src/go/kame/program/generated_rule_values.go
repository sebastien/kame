package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
)

const (
	generatedBatchLimit    = 4096
	generatedDataLimit     = 1 << 20
	generatedFieldLimit    = 64 << 10
	generatedListItemLimit = 4096
)

type generatedEvaluation struct {
	Program      *Program
	Stack        []string
	Dependencies []string
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
		state.Dependencies = append(state.Dependencies, cloneText(state.Program.Alloc, key.Name))
	}
	state.Stack = append(state.Stack, key.Name)
	result := state.Program.Eval.EvaluateDefinition(key, context)
	state.Stack = state.Stack[:len(state.Stack)-1]
	return result
}

type generatedBatchUsage struct {
	rules  int
	bytes  int
	inputs int
}

type declarationGeneration struct {
	Site diagnostic.Frame
	// The evaluator retains constructing calls for the lifetime of the program.
	Calls []diagnostic.Frame
}

func (p *Program) registerRuleValues(item script.ScriptItem, scope declarationScope) diagnostic.Diagnostic {
	state := generatedEvaluation{Program: p}
	defer func() {
		mem.FreeSlice(nil, state.Stack)
		freeStrings(p.Alloc, state.Dependencies)
	}()
	r := p.evaluateDeclaration(item.Expression, scope.Scope, &state)
	defer r.Free(p.Alloc)
	if r.Diagnostic.Code != "" {
		return r.Diagnostic.Clone(p.Alloc)
	}
	values := r.Value.List
	if r.Value.Kind == core.Rule {
		values = []core.Value{r.Value}
	} else if r.Value.Kind != core.List {
		return p.declarationDiagnostic(item.Span, item.SourceOrigin, "EXPR_INVALID", "generate requires a Rule or a list of Rule values")
	}
	usage := p.GeneratedBatchUsage[item.Expression]
	if len(values) > generatedBatchLimit-usage.rules {
		return p.declarationDiagnostic(item.Span, item.SourceOrigin, "EXPR_INVALID", "generate exceeds its declaration limit")
	}
	location := p.targetLocation(rule.Target{SourceOrigin: item.SourceOrigin, Span: item.Span})
	for _, value := range values {
		constructed, ok := value.Rule.(*eval.ConstructedRule)
		if value.Kind != core.Rule || !ok || constructed == nil || constructed.Owner != p.Eval {
			return p.declarationDiagnostic(item.Span, item.SourceOrigin, "EXPR_INVALID", "generate requires every list item to be a Rule")
		}
		generation := &declarationGeneration{
			Site: diagnostic.Frame{Kind: "generate", Label: "registration", Source: location.Source,
				Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}},
			Calls: constructed.Frames,
		}
		captured := declarationScope{Scope: constructed.Scope, Signature: p.Eval.RuleCaptureSignature(constructed.Scope), Iteration: scope.Iteration, Generation: generation}
		registered, d := p.registerScopedRule(constructed.Declaration, captured)
		if d.Code != "" {
			p.appendDeclarationContext(&d, captured)
			return d
		}
		if !usage.add(registered) {
			return p.declarationDiagnostic(item.Span, item.SourceOrigin, "EXPR_INVALID", "generate exceeds its retained data or input limit")
		}
		p.GeneratedRuleMeta = append(p.GeneratedRuleMeta, generatedRuleMeta{
			Rule: registered, Dependencies: cloneStrings(p.Alloc, state.Dependencies),
		})
	}
	if p.GeneratedBatchUsage == nil {
		p.GeneratedBatchUsage = make(map[*expr.Expr]generatedBatchUsage)
	}
	p.GeneratedBatchUsage[item.Expression] = usage
	if p.firstGenerateSpan.End == 0 {
		p.firstGenerateSpan = item.Span
	}
	return diagnostic.Diagnostic{}
}

func (usage *generatedBatchUsage) add(r *rule.Rule) bool {
	usage.rules++
	usage.inputs += len(r.Inputs)
	if usage.inputs > generatedListItemLimit {
		return false
	}
	for _, output := range r.Outputs {
		if len(output.Text) > generatedFieldLimit {
			return false
		}
		usage.bytes += len(output.Text)
	}
	for _, input := range r.Inputs {
		if len(input.Text) > generatedFieldLimit {
			return false
		}
		usage.bytes += len(input.Text)
	}
	if r.Metadata != nil {
		size := r.Metadata.Span.End - r.Metadata.Span.Start
		if size > generatedFieldLimit {
			return false
		}
		usage.bytes += size
	}
	return usage.bytes <= generatedDataLimit
}
