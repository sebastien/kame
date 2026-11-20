package evalcheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
)

func TestSelectorMissingContextDiagnostics(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.ParseKameFile(a, "selector.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	names := []string{"inputs", "outputs", "args"}
	context := eval.Context{Program: p, Scope: p.Scope, Run: a}
	for _, name := range names {
		text := " " + name
		expression := expr.Parse(a, "selector.km", text)
		if len(expression.Diagnostics) != 0 {
			t.Error("context binding fixture did not parse")
		}
		r := p.EvaluateWith(expression.Expr, &context)
		expression.Free()
		if r.Diagnostic.Code != "SEL_NO_CONTEXT" || r.Diagnostic.Message != name+" requires an active rule or invocation context" ||
			!r.Diagnostic.Owned || r.Diagnostic.Span.Start != 1 || r.Diagnostic.Span.End != len(text) {
			t.Error("missing context binding lost kind, ownership, or authored span")
		}
		if len(r.Diagnostic.Tips) != 1 || r.Diagnostic.Tips[0] != "Use "+name+" where it is bound, or pass the list explicitly." {
			t.Error("missing context binding lost its owned remediation tip")
		}
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
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "selector.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	values := []core.Value{{Kind: core.Int, Int: 7}}
	context := eval.Context{Program: p, Scope: p.Scope, Run: a, Inputs: values, Outputs: values, Args: values, HasArgs: true}
	texts := []string{"inputs.0", "outputs.0", "args.0"}
	for _, text := range texts {
		expression := expr.Parse(a, "selector.km", text)
		r := p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Int || r.Value.Int != 7 {
			t.Error("explicit non-rule selector context was rejected")
		}
		r.Free(a)
		expression.Free()
	}
	context.Inputs, context.Outputs, context.Args = nil, nil, nil
	context.RuleFrames = []eval.RuleFrame{{}}
	empty := []string{"inputs.0", "outputs.0", "args.0"}
	for _, text := range empty {
		expression := expr.Parse(a, "selector.km", text)
		r := p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "SEL_INDEX_INVALID" {
			t.Error("present empty context was mislabeled as missing")
		}
		r.Free(a)
		expression.Free()
	}
	emptyLists := []string{"inputs", "outputs", "args"}
	for _, text := range emptyLists {
		expression := expr.Parse(a, "selector.km", text)
		r := p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.List || len(r.Value.List) != 0 {
			t.Error("present empty context stopped supporting all-item selection")
		}
		r.Free(a)
		expression.Free()
	}
	emptyCounts := []string{"count inputs", "count outputs", "count args"}
	for _, text := range emptyCounts {
		expression := expr.Parse(a, "selector.km", text)
		r := p.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Int || r.Value.Int != 0 {
			t.Error("present empty context stopped supporting count selection")
		}
		r.Free(a)
		expression.Free()
	}
	p.Free()
	parsed.Free()
	registry.Free()
	engine.Free()
}
