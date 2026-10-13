package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestCompositionPublicEvaluationPreflightsAllChildren(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	defer engine.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	parsed := script.Parse(a, "empty", "")
	defer parsed.Free()
	p := eval.Compile(a, engine, parsed, registry)
	defer p.Free()
	cases := []string{"(if :false (include ./a.km) :nil)", "([] (include? ./a.km))", "(let [unused ([] (include ./a.km))] (out! 42))", "(if :true (out! 42) (include ./a.km))"}
	for i := range cases {
		expression := expr.Parse(a, "public", cases[i])
		plain := p.Evaluate(a, expression.Expr, p.Scope)
		if plain.Diagnostic.Code != "EXPR_INVALID" || !plain.Diagnostic.Owned {
			t.Error("Evaluate missed hidden source head or returned borrowed failure")
		}
		plain.Free(a)
		context := &eval.Context{Program: p, Scope: p.Scope, Run: a, Source: "public"}
		result := p.EvaluateWith(expression.Expr, context)
		if result.Diagnostic.Code != "EXPR_INVALID" || len(context.Effects) != 0 || result.Diagnostic.Source != "public" {
			t.Error("EvaluateWith ran effects or lost placement diagnostic source")
		}
		result.Free(a)
		eval.FreeEffects(a, context.Effects)
		expression.Free()
	}
	normal := expr.Parse(a, "normal", "(if :false 1 :true 42 0)")
	defer normal.Free()
	plain := p.Evaluate(a, normal.Expr, p.Scope)
	defer plain.Free(a)
	context := &eval.Context{Program: p, Scope: p.Scope, Run: a}
	result := p.EvaluateWith(normal.Expr, context)
	defer result.Free(a)
	if plain.Diagnostic.Code != "" || plain.Value.Kind != core.Int || plain.Value.Int != 42 || result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 {
		t.Error("public validation changed ordinary runtime if")
	}
}
