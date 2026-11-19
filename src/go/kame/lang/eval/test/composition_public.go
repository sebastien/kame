package evalcheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
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
	cases := []string{"(if false then (include ./a.km) else nil)", "(() => (include? ./a.km))", "((unused => (out! 42)) (() => (include ./a.km)))", "(if true then (out! 42) else (include ./a.km))"}
	for i := range cases {
		expression := expr.Parse(a, "public", cases[i])
		if len(expression.Diagnostics) == 0 || expression.Diagnostics[0].Code != "PARSE_ERR" {
			t.Error("value parser accepted a source declaration in a nested expression")
		}
		expression.Free()
	}
	normal := expr.Parse(a, "normal", "(if false then 1 elif true then 42 else 0)")
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
