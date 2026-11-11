package evalcheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
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
	r = evaluate(t, program, "(kash ./module.kash)")
	if r.Diagnostic.Code != "SEL_NO_CONTEXT" {
		t.Error("literal module path did not require a file-backed source")
	}
	r.Free(a)
	r = evaluate(t, program, "(kash \"./module.kash\")")
	if r.Diagnostic.Code != "" || r.Value.Kind != core.Callable {
		t.Error("quoted module-looking text did not construct a script")
	}
	r.Free(a)
	invalid := []string{"(kash ./module.km)", "(kash ./*.kash)", "(kash ./@{name}.kash)"}
	for i := range invalid {
		r = evaluate(t, program, invalid[i])
		if r.Diagnostic.Code != "EXPR_INVALID" {
			t.Error("invalid module path was accepted")
		}
		r.Free(a)
	}
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

func TestNativeKashFunctionsBindAndReturnWithoutHostProcesses(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	text := "result = (list (probe 75 1 2) (probe 0))\ndef probe status rest...\n\tdef nested code\n\t\treturn code ?\n\tif (= (count rest) 2)\n\t\treturn status ?\n\t(nested 3)\n\treturn (count rest)\n\t(fail! \"unreachable\")\n"
	parsed := script.ParseKash(a, "functions.kash", text)
	p := eval.Compile(a, engine, parsed, registry)
	node := p.Definition("result")
	engine.Request(node)
	stepUntilCurrent(engine, node)
	if !node.Current || node.Diagnostic.Code != "" || len(node.Latest.List) != 2 || node.Latest.List[0].Record[0].Value.Int != 75 || node.Latest.List[1].Record[0].Value.Int != 0 {
		t.Error("native function failed to bind parameters, accept status, or unwind nested calls")
	}
	if request := p.Requests.Next(); request.OK {
		request.Request.Free(a)
		t.Error("native status return launched a host process")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestNativeKashFunctionsHaveFreshLocalDefinitions(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := operationState{}
	registry.Add(eval.Operation{Name: "tick", Call: countOperation, Context: &state, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKash(a, "fresh.kash", "def probe\n\tlocal = (tick)\n\treturn local ?\nresult = (map ([unused] (probe)) [1 2 3])\n")
	p := eval.Compile(a, engine, parsed, registry)
	node := p.Definition("result")
	engine.Request(node)
	stepUntilCurrent(engine, node)
	if !node.Current || node.Diagnostic.Code != "" || len(node.Latest.List) != 3 || state.Calls != 3 {
		t.Error("separate native calls shared local effects")
	} else {
		for i := range node.Latest.List {
			if node.Latest.List[i].Record[0].Value.Int != int64(i+1) {
				t.Error("native call returned a stale local definition")
			}
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
