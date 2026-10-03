package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/testing"
)

func TestNewerSelectorUsesNearestFileFrameAndRejectsPlanning(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "@<?")
	if len(expression.Diagnostics) != 0 {
		t.Error("newer selector did not parse")
	}
	outer, inner := core.NewString(a, "outer"), core.NewString(a, "inner")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Phase: eval.RenderingPhase, RuleFrames: []eval.RuleFrame{{FileRule: true, NewerInputs: []core.Value{outer}}, {FileRule: true, NewerInputs: []core.Value{inner}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.List || len(result.Value.List) != 1 || result.Value.List[0].Text != "inner" {
		t.Error("newer selector did not use nearest file frame")
	}
	result.Free(a)
	context.Phase = eval.PlanningPhase
	result = program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "PHASE_INVALID" {
		t.Error("planning accepted newer selection")
	}
	result.Free(a)
	context.Phase = eval.RenderingPhase
	context.RuleFrames[1].FileRule = false
	result = program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "SEL_NO_CONTEXT" {
		t.Error("task frame inherited enclosing file newer values")
	}
	result.Free(a)
	outer.Free(a)
	inner.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}
