package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestCompositionDynamicTextRejectsHeadsBeforeEffects(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	defer engine.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	parsed := script.Parse(a, "empty.km", "")
	defer parsed.Free()
	p := eval.Compile(a, engine, parsed, registry)
	defer p.Free()
	cases := []string{
		"(eval \"(if :false (include ./a.km) (out! 42))\")",
		"(eval \"(let [unused ([] (include? ./a.km))] (out! 42))\")",
		"(kash \"(out! 42)\\n(if :false (include ./a.km) :nil)\")",
	}
	for i := range cases {
		expression := expr.Parse(a, "dynamic", cases[i])
		context := &eval.Context{Program: p, Scope: p.Scope, Run: a}
		result := p.EvaluateWith(expression.Expr, context)
		if result.Diagnostic.Code != "EXPR_INVALID" || len(context.Effects) != 0 {
			t.Error("dynamic text did not reject source heads before effects")
		}
		result.Free(a)
		eval.FreeEffects(a, context.Effects)
		expression.Free()
	}
	context := &eval.Context{Program: p, Scope: p.Scope, Run: a}
	result := p.RunKash("(out! 42)\nunused = ([] (include ./a.km))\n", context, source.Span{})
	defer result.Free(a)
	defer eval.FreeEffects(a, context.Effects)
	if result.Diagnostic.Code != "EXPR_INVALID" || len(context.Effects) != 0 {
		t.Error("Kash recipe ran earlier effects before hidden-head validation")
	}
	if request := p.Requests.Next(); request.OK {
		request.Request.Free(a)
		t.Error("invalid dynamic source submitted host work")
	}
}
