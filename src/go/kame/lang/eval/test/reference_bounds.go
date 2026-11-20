package evalcheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
)

func TestRemediation3ReferenceBounds(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.ParseKameFile(a, "bounds.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		"((xs => xs.18446744073709551616) [7])",
		"((xs => xs.-18446744073709551616) [7])",
		"((xs => xs.9223372036854775807) \"a\")",
	}
	for i := range texts {
		r := evaluate(t, p, texts[i])
		if r.Diagnostic.Code != "SEL_INDEX_INVALID" {
			t.Error("invalid reference bound accepted")
		}
		r.Free(a)
	}
	selectors := []string{"outputs.18446744073709551616", "outputs.-18446744073709551616", "outputs.9223372036854775807", "outputs.-2147483648"}
	context := eval.Context{Program: p, Scope: p.Scope, Run: a, Outputs: []core.Value{{Kind: core.Int, Int: 7}}}
	for i := range selectors {
		expression := expr.Parse(a, "selector.km", selectors[i])
		if len(expression.Diagnostics) != 0 {
			t.Error("numeric selector fixture did not parse")
		}
		r := p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "SEL_INDEX_INVALID" {
			t.Error("invalid selector bound accepted")
		}
		r.Free(a)
		expression.Free()
	}
	p.Free()
	parsed.Free()
	registry.Free()
	engine.Free()
}
