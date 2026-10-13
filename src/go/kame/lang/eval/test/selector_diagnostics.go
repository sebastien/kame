package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/testing"
)

func TestSelectorMissingContextDiagnostics(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "selector.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{" @<", " @>", " @0"}
	messages := []string{
		"input selector @< requires an input context; none is available",
		"output selector @> requires an output context; none is available",
		"argument selector @0 requires an argument context; none is available",
	}
	tips := []string{
		"use @< in a rule recipe, or pass the input value explicitly",
		"use @> in a rule recipe, or pass the output value explicitly",
		"use @0 in a rule recipe, or pass the argument value explicitly",
	}
	context := eval.Context{Program: p, Scope: p.Scope, Run: a}
	for i := range texts {
		expression := expr.Parse(a, "selector.km", texts[i])
		if len(expression.Diagnostics) != 0 { t.Error("selector fixture did not parse") }
		r := p.EvaluateWith(expression.Expr, &context)
		expression.Free()
		if r.Diagnostic.Code != "SEL_NO_CONTEXT" || r.Diagnostic.Message != messages[i] || !r.Diagnostic.Owned || r.Diagnostic.Span.Start != 1 || r.Diagnostic.Span.End != len(texts[i]) {
			t.Error("missing selector context lost kind, ownership, or authored span")
		}
		if len(r.Diagnostic.Tips) != 1 || r.Diagnostic.Tips[0] != tips[i] { t.Error("missing selector context lost its owned remediation tip") }
		r.Free(a)
	}
	p.Free()
	parsed.Free()
	registry.Free()
	engine.Free()
}

func TestSelectorPresentContextIsNotMissing(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "selector.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	values := []core.Value{{Kind: core.Int, Int: 7}}
	context := eval.Context{Program: p, Scope: p.Scope, Run: a, Inputs: values, Outputs: values, Args: values, HasArgs: true}
	texts := []string{"@<", "@>", "@0"}
	for _, text := range texts {
		expression := expr.Parse(a, "selector.km", text)
		r := p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Int || r.Value.Int != 7 { t.Error("explicit non-rule selector context was rejected") }
		r.Free(a)
		expression.Free()
	}
	context.Inputs, context.Outputs, context.Args = nil, nil, nil
	context.RuleFrames = []eval.RuleFrame{{}}
	empty := []string{"@<", "@>", "@0"}
	for _, text := range empty {
		expression := expr.Parse(a, "selector.km", text)
		r := p.EvaluateWith(expression.Expr, &context)
		message := "selector index is empty"
		if text == "@0" { message = "selector index out of range" }
		if r.Diagnostic.Code != "SEL_INDEX_INVALID" || r.Diagnostic.Message != message { t.Error("present empty context was mislabeled as missing") }
		r.Free(a)
		expression.Free()
	}
	emptyLists := []string{"@<*", "@>*", "@*"}
	for _, text := range emptyLists {
		expression := expr.Parse(a, "selector.km", text)
		r := p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.List || len(r.Value.List) != 0 { t.Error("present empty context stopped supporting all-item selection") }
		r.Free(a)
		expression.Free()
	}
	emptyCounts := []string{"@<#", "@>#", "@#"}
	for _, text := range emptyCounts {
		expression := expr.Parse(a, "selector.km", text)
		r := p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Int || r.Value.Int != 0 { t.Error("present empty context stopped supporting count selection") }
		r.Free(a)
		expression.Free()
	}
	p.Free()
	parsed.Free()
	registry.Free()
	engine.Free()
}
