package lib_test

import (
	"littlemake/core"
	"littlemake/lang/eval"
	"littlemake/lang/expr"
	"littlemake/lang/script"
	"littlemake/lib"
	"solod.dev/so/testing"
)

func evaluate(t *testing.T, program *eval.Program, text string) eval.Result {
	parsed := expr.Parse(t.Allocator(), "test", text)
	if len(parsed.Diagnostics) != 0 { t.Error("parse failed"); parsed.Free(); return eval.Result{} }
	result := program.Evaluate(t.Allocator(), parsed.Expr, program.Scope)
	parsed.Free()
	return result
}

func TestPureOperations(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Error("library registration failed") }
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(count (list \"one\" \"two\"))")
	if result.Diagnostic.Code != "" || result.Value.Int != 2 { t.Error("count/list failed") }
	result.Free(a)
	result = evaluate(t, program, "(first (list \"first\" \"second\"))")
	if result.Diagnostic.Code != "" || result.Value.Text != "first" { t.Error("first failed") }
	result.Free(a)
	result = evaluate(t, program, "(not :nil)")
	if result.Diagnostic.Code != "" || !result.Value.Bool { t.Error("not failed") }
	result.Free(a)
	result = evaluate(t, program, "(map ([x] x) (list \"first\" \"second\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "second" { t.Error("map failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}
