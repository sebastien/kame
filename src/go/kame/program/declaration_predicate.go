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

type PredicateResult struct { Selected bool; Diagnostic diagnostic.Diagnostic }

// DeclarationPredicate evaluates a pure configuration predicate against already
// selected declarations. Includes are supplied by the source-composition host;
// this function never reads files, submits requests or launches processes.
// Future explicit overrides are validated by final registration, rather than
// rejected merely because their declaration has not been loaded yet.
func DeclarationPredicate(a mem.Allocator, name string, prefix string, predicate string, defines []string, environment []string) PredicateResult {
 parsed := script.Parse(a, name, prefix)
 defer parsed.Free()
 var known []string
 for i := range defines {
  equal := strings.IndexByte(defines[i], '=')
  if equal <= 0 { continue }
  for j := range parsed.Items {
   d := parsed.Items[j].Definition
   if d != nil && !d.Function && d.Name == defines[i][:equal] { known = slices.Append(a, known, defines[i]); break }
  }
 }
 defer slices.Free(a, known)
 registry := eval.NewRegistry(a)
 operations.Register(registry)
 defer registry.Free()
 compiled := Compile(a, parsed, registry, Options{Defines: known, Environment: environment})
 defer compiled.Free(a)
 if compiled.Program == nil {
  if len(compiled.Diagnostics) != 0 { return PredicateResult{Diagnostic: compiled.Diagnostics[0].Clone(a)} }
  return PredicateResult{Diagnostic: failure(a, "PARSE_ERR", "conditional configuration could not be compiled")}
 }
 p := compiled.Program
 defer p.Free()
 expression := expr.Parse(a, name, predicate)
 defer expression.Free()
 if len(expression.Diagnostics) != 0 {
  d := expression.Diagnostics[0]
  return PredicateResult{Diagnostic: failureAt(a, d.Code, diagnostic.Span{Start: d.Span.Start, End: d.Span.End}, d.Message)}
 }
 state := planResolverState{Program: p}
 defer slices.Free(a, state.Resolving)
 context := &eval.Context{Program: p.Eval, Scope: p.Eval.Scope, Run: a, Source: name, Phase: eval.PlanningPhase, ResolveDefinition: resolvePlanDefinition, ResolverState: &state}
 result := p.Eval.EvaluateWith(expression.Expr, context)
 defer result.Free(a)
 defer eval.FreeEffects(a, context.Effects)
 defer freeStrings(a, context.WritePaths)
 if result.Diagnostic.Code != "" { return PredicateResult{Diagnostic: result.Diagnostic.Clone(a)} }
 if result.Waiting || result.Stream != nil || context.PhaseInvalid() || len(context.Effects) != 0 { return PredicateResult{Diagnostic: failure(a, "PHASE_INVALID", "declaration predicates must be pure configuration expressions")} }
 if result.Value.Kind != core.Bool { return PredicateResult{Diagnostic: failure(a, "EXPR_INVALID", "declaration predicate must return bool")} }
 return PredicateResult{Selected: result.Value.Bool}
}
