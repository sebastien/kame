package lib_test

import (
	"littlemake/core"
	"littlemake/host"
	"littlemake/lang/eval"
	"littlemake/lang/expr"
	"littlemake/lang/script"
	"littlemake/operations"
	"solod.dev/so/testing"
)

func waitEach(context *eval.Context, state any, values []core.Value) eval.Result {
	_, _ = state, values
	if context.TakeCompletion().RequestID != 0 { return eval.Result{Value: values[0].Clone(context.Run)} }
	if context.Submit(host.RequestCustom, core.Value{Kind: core.Nil}) == 0 { return eval.Result{Diagnostic: core.Diagnostic{Code: "HOST_FAIL"}} }
	return eval.Result{Waiting: true}
}

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
	if !operations.Register(registry) { t.Error("library registration failed") }
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
	result = evaluate(t, program, "(map ([x] (list x x)) (list \"first\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 1 || len(result.Value.List[0].List) != 2 { t.Error("map container result failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestMapResumesWithoutRepeatingCallbacks(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	registry.Add(eval.Operation{Name: "wait-each", Call: waitEach, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "test", "result = (map ([item] (wait-each item)) (list \"one\" \"two\"))")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	for i := 0; i < 2; i++ { engine.Step() }
	first := program.Requests.Next()
	if !first.OK { t.Fatal("first callback did not submit"); return }
	engine.Complete(core.Completion{NodeID: first.Request.NodeID, Generation: first.Request.Generation, Attempt: first.Request.Attempt, RequestID: first.Request.ID})
	first.Request.Free(a)
	for i := 0; i < 2; i++ { engine.Step() }
	second := program.Requests.Next()
	if !second.OK || second.Request.ID == first.Request.ID { t.Fatal("second callback did not submit separately"); return }
	engine.Complete(core.Completion{NodeID: second.Request.NodeID, Generation: second.Request.Generation, Attempt: second.Request.Attempt, RequestID: second.Request.ID})
	second.Request.Free(a)
	for i := 0; i < 3; i++ { engine.Step() }
	if !node.Current || len(node.Latest.List) != 2 || node.Latest.List[0].Text != "one" || node.Latest.List[1].Text != "two" { t.Error("map did not resume in input order") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}
