package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/testing"
)

func TestRemediation3ReferenceBounds(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "bounds.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		"(let [xs [7]] xs.18446744073709551616)",
		"(let [xs [7]] xs.-18446744073709551616)",
		"(let [xs [7]] xs.0..18446744073709551616)",
		"(let [xs [7]] xs.0..-2147483648)",
		"(let [xs \"a\"] xs.9223372036854775807)",
	}
	for i := range texts {
		r := evaluate(t, p, texts[i])
		if r.Diagnostic.Code != "SEL_INDEX_INVALID" { t.Error("invalid reference bound accepted") }
		r.Free(a)
	}
	r := evaluate(t, p, "(let [xs [7 8]] xs.1..)")
	if r.Diagnostic.Code != "" || r.Value.Kind != core.List || len(r.Value.List) != 1 || r.Value.List[0].Int != 8 {
		t.Error("omitted slice end changed")
	}
	r.Free(a)
	selectors := []string{"@>18446744073709551616", "@>-18446744073709551616", "@>0..18446744073709551616", "@>0..-2147483648"}
	context := eval.Context{Program: p, Scope: p.Scope, Run: a, Outputs: []core.Value{{Kind: core.Int, Int: 7}}}
	for i := range selectors {
		expression := expr.Parse(a, "selector.km", selectors[i])
		if len(expression.Diagnostics) != 0 { t.Error("numeric selector fixture did not parse") }
		r = p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "SEL_INDEX_INVALID" { t.Error("invalid selector bound accepted") }
		r.Free(a)
		expression.Free()
	}
	p.Free()
	parsed.Free()
	registry.Free()
	engine.Free()
}
