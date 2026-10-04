package eval_test

import (
 "kame/core"
 "kame/lang/eval"
 "kame/lang/expr"
 "kame/lang/script"
 "kame/operations"
 "solod.dev/so/testing"
)

func resolveInlineDefinition(value any, key core.ResourceKey, context *eval.Context) eval.Result {
 return value.(*eval.Program).EvaluateDefinition(key, context)
}

func TestInlineGlobalDefinitionPreservesFunctionOperandScope(t *testing.T) {
 a := t.Allocator()
 parsed := script.Parse(a, "inline.km", "mode = \"release\"\n(matches wanted) = (eq mode wanted)\n")
 defer parsed.Free()
 registry := eval.NewRegistry(a)
 operations.Register(registry)
 defer registry.Free()
 engine := core.NewEngine(a)
 defer engine.Free()
 compiled := eval.CompileChecked(a, engine, parsed, registry)
 defer compiled.Free(a)
 if compiled.Program == nil { t.Fatal("inline test compile failed"); return }
 p := compiled.Program
 defer p.Free()
 expression := expr.Parse(a, "predicate.km", "(matches \"release\")")
 defer expression.Free()
 context := &eval.Context{Program: p, Scope: p.Scope, Run: a, Source: "predicate.km", Phase: eval.PlanningPhase, ResolveDefinition: resolveInlineDefinition, ResolverState: p}
 result := p.EvaluateWith(expression.Expr, context)
 defer result.Free(a)
 if result.Diagnostic.Code != "" || result.Value.Kind != core.Bool || !result.Value.Bool { t.Error("global definition resolution lost the following function parameter") }
 if context.Scope != p.Scope { t.Error("inline definition did not restore the caller scope") }
}
