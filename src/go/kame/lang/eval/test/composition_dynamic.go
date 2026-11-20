package evalcheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"kame/operations"
)

func TestCompositionDynamicTextRejectsHeadsBeforeEffects(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	defer engine.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "empty.km", "")
	defer parsed.Free()
	p := eval.Compile(a, engine, parsed, registry)
	defer p.Free()
	cases := []string{
		"(eval \"(if false then (include ./a.km) else (out! 42))\")",
		"(eval \"((unused => (out! 42)) (() => (include? ./a.km)))\")",
	}
	for i := range cases {
		expression := expr.Parse(a, "dynamic", cases[i])
		context := &eval.Context{Program: p, Scope: p.Scope, Run: a}
		result := p.EvaluateWith(expression.Expr, context)
		if result.Diagnostic.Code != "PARSE_ERR" || len(context.Effects) != 0 {
			t.Error("dynamic text did not reject source heads before effects")
		}
		result.Free(a)
		eval.FreeEffects(a, context.Effects)
		expression.Free()
	}
	context := &eval.Context{Program: p, Scope: p.Scope, Run: a}
	result := p.RunKash("(out! 42)\nunused = (() => (include ./a.km))\n", context, source.Span{})
	defer result.Free(a)
	defer eval.FreeEffects(a, context.Effects)
	if result.Diagnostic.Code != "PARSE_ERR" || len(context.Effects) != 0 {
		t.Error("Kash recipe ran earlier effects before hidden-head validation")
	}
	if request := p.Requests.Next(); request.OK {
		request.Request.Free(a)
		t.Error("invalid dynamic source submitted host work")
	}
}
