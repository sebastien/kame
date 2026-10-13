package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestCompositionRegistrationGuards(t *testing.T) {
	a := t.Allocator()
	cases := []string{"(include ./a.km)\nx = 42\n", "(if :false (include? ./a.km) :nil)\nx = 42\n"}
	for i := range cases {
		engine := core.NewEngine(a)
		parsed := script.Parse(a, "composition.km", cases[i])
		checked := eval.CompileChecked(a, engine, parsed, nil)
		if checked.Program != nil || len(checked.Diagnostics) == 0 || checked.Diagnostics[0].Code != "FEATURE_UNSUP" {
			t.Error("checked compile accepted unresolved composition")
		}
		checked.Free(a)
		compiled := eval.Compile(a, engine, parsed, nil)
		if compiled.Valid || compiled.Definition("x") != nil || len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Code != "FEATURE_UNSUP" {
			t.Error("unchecked compile accepted unresolved composition")
		}
		engine.Free()
		compiled.Free()
		parsed.Free()
	}
}

func TestCompositionRuntimeReservationAndConditionalArity(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	defer engine.Free()
	parsed := script.Parse(a, "empty.km", "")
	defer parsed.Free()
	p := eval.Compile(a, engine, parsed, nil)
	defer p.Free()
	cases := []string{"(include ./a.km)", "(include? ./a.km)", "(if)", "(if :true)"}
	for i := range cases {
		expression := expr.Parse(a, "runtime.km", cases[i])
		context := &eval.Context{Program: p, Scope: p.Scope, Run: a, Source: "runtime.km"}
		result := p.EvaluateWith(expression.Expr, context)
		if result.Diagnostic.Code != "EXPR_INVALID" {
			t.Error("invalid source form or if arity accepted")
		}
		if i >= 2 && !strings.Contains(result.Diagnostic.Message, "expects at least 2 arguments; got") {
			t.Error("if arity lacks expected and actual counts")
		}
		result.Free(a)
		expression.Free()
	}
	if !eval.IsSpecialForm("include") || !eval.IsSpecialForm("include?") {
		t.Error("source call heads are not reserved")
	}
	expression := expr.Parse(a, "pairs.km", "(if :false 1 :true 2 3)")
	defer expression.Free()
	context := &eval.Context{Program: p, Scope: p.Scope, Run: a}
	result := p.EvaluateWith(expression.Expr, context)
	defer result.Free(a)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 2 {
		t.Error("runtime if lost multiple test/body pairs")
	}
}
