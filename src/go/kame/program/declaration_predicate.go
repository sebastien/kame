package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type PredicateResult struct {
	Selected   bool
	Diagnostic diagnostic.Diagnostic
}

// ponytail: prefix/predicate offsets are synthetic; keep unqualified frame labels
// until the query accepts an authored origin map instead of inventing locations.
func configurationDiagnostic(a mem.Allocator, value diagnostic.Diagnostic) diagnostic.Diagnostic {
	view := value
	if value.Code == "CAP_DENIED" {
		view.Message = "configuration conditions cannot use host capabilities or execute processes"
		view.Tips = []string{"select source files using literal configuration and pure expressions; runtime grants do not apply to conditions"}
	} else if value.Code == "PHASE_INVALID" {
		view.Message = "configuration conditions must be pure; runtime effects are not allowed"
		view.Tips = []string{"move effects into runtime statements or rule recipes"}
	}
	copy := view.Clone(a)
	for i := range copy.Frames {
		mem.FreeString(a, copy.Frames[i].Source)
		copy.Frames[i].Source = ""
		copy.Frames[i].Span = diagnostic.Span{}
	}
	return copy
}

// DeclarationPredicate evaluates a pure configuration predicate against already
// selected declarations. Includes are supplied by the source-composition host;
// this function never reads files, submits requests or launches processes.
// Future explicit overrides are validated by final registration, rather than
// rejected merely because their declaration has not been loaded yet.
func DeclarationPredicate(a mem.Allocator, name string, prefix string, predicate string, defines []string, environment []string) PredicateResult {
	return configurationCondition(a, name, prefix, predicate, defines, environment, true)
}

// CompositionCondition uses ordinary expression truth without granting host
// capabilities or loading sources: only nil and false are false.
func CompositionCondition(a mem.Allocator, name string, prefix string, predicate string, defines []string, environment []string) PredicateResult {
	return configurationCondition(a, name, prefix, predicate, defines, environment, false)
}

func configurationCondition(a mem.Allocator, name string, prefix string, predicate string, defines []string, environment []string, requireBoolean bool) PredicateResult {
	parsed := script.Parse(a, name, prefix)
	defer parsed.Free()
	var known []string
	for i := range defines {
		equal := strings.IndexByte(defines[i], '=')
		if equal <= 0 {
			continue
		}
		for j := range parsed.Items {
			d := parsed.Items[j].Definition
			if d != nil && !d.Function && d.Name == defines[i][:equal] {
				known = slices.Append(a, known, defines[i])
				break
			}
		}
	}
	defer slices.Free(a, known)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	defer registry.Free()
	compiled := Compile(a, parsed, registry, Options{Defines: known, Environment: environment})
	defer compiled.Free(a)
	if compiled.Program == nil {
		if len(compiled.Diagnostics) != 0 {
			return PredicateResult{Diagnostic: configurationDiagnostic(a, compiled.Diagnostics[0])}
		}
		return PredicateResult{Diagnostic: failure(a, "PARSE_ERR", "conditional configuration could not be compiled")}
	}
	p := compiled.Program
	defer p.Free()
	expression := expr.Parse(a, name, predicate)
	defer expression.Free()
	if len(expression.Diagnostics) != 0 {
		d := expression.Diagnostics[0]
		portable := diagnostic.Diagnostic{Source: name, Code: d.Code, Severity: diagnostic.Error, Span: diagnostic.Span{Start: d.Span.Start, End: d.Span.End}, Message: d.Message}
		return PredicateResult{Diagnostic: portable.Clone(a)}
	}
	state := planResolverState{Program: p}
	defer slices.Free(a, state.Resolving)
	context := &eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: a, Source: name, Phase: eval.PlanningPhase, ResolveDefinition: resolvePlanDefinition, ResolverState: &state}
	result := p.Eval.EvaluateWith(expression.Expr, context)
	defer result.Free(a)
	defer eval.FreeEffects(a, context.Effects)
	defer freeStrings(a, context.WritePaths)
	if result.Diagnostic.Code != "" {
		return PredicateResult{Diagnostic: configurationDiagnostic(a, result.Diagnostic)}
	}
	if result.Waiting || result.Stream != nil || context.PhaseInvalid() || len(context.Effects) != 0 {
		return PredicateResult{Diagnostic: configurationDiagnostic(a, diagnostic.Diagnostic{Source: name, Code: "PHASE_INVALID", Severity: diagnostic.Error, Span: diagnostic.Span{Start: expression.Expr.Span.Start, End: expression.Expr.Span.End}})}
	}
	if requireBoolean && result.Value.Kind != core.Bool {
		d := diagnostic.Diagnostic{Source: name, Code: "EXPR_INVALID", Severity: diagnostic.Error, Span: diagnostic.Span{Start: expression.Expr.Span.Start, End: expression.Expr.Span.End}, Message: "configuration predicate must return bool; got " + core.KindName(result.Value.Kind), Tips: []string{"Use an explicit comparison that returns :true or :false."}}
		return PredicateResult{Diagnostic: d.Clone(a)}
	}
	return PredicateResult{Selected: result.Value.Kind != core.Nil && !(result.Value.Kind == core.Bool && !result.Value.Bool)}
}
