package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestKashConstructionIsPureAndRetainsLexicalScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test.km", "")
	program := eval.Compile(a, engine, parsed, registry)
	r := evaluate(t, program, "(let [name \"retained\"] (kash \"printf %s $name\"))")
	if r.Diagnostic.Code != "" || r.Value.Kind != core.Callable || r.Value.CallableOwner != program {
		t.Error("Kash script did not retain an invocation-owned callable")
	}
	if request := program.Requests.Next(); request.OK {
		request.Request.Free(a)
		t.Error("script construction performed a host effect")
	}
	r.Free(a)
	r = evaluate(t, program, "(kash \"if true\")")
	if r.Diagnostic.Code != "PARSE_ERR" {
		t.Error("Kash construction accepted an incomplete block")
	}
	r.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestKashCallableDefinitionsPublishWithoutRelaxingClosurePolicy(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test.km", "SHELL = kash\nscript = (let [value 42] (kash \"printf %s $value\"))\n")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("SHELL")
	engine.Request(node)
	for i := 0; i < 10 && !node.Current; i++ {
		engine.Step()
	}
	if !node.Current || !program.IsKashConstructor(node.Latest) {
		t.Error("Kash constructor could not cross the definition boundary")
	}
	node = program.Definition("script")
	engine.Request(node)
	for i := 0; i < 10 && !node.Current; i++ {
		engine.Step()
	}
	if !node.Current || node.Latest.Kind != core.Callable || node.Latest.HasTransientCallable() {
		t.Error("retained script definition could not publish")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}
