package evalcheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
)

func TestKashInterpreterRejectsTextCompilation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "test.km", "")
	program := eval.Compile(a, engine, parsed, registry)
	textCalls := []string{
		`kash "printf forbidden"`,
		`kash "./module.kash"`,
		`kash ./module.kash`,
		`kash (out! "forbidden")`,
	}
	for i := range textCalls {
		expression := expr.Parse(a, "text.km", textCalls[i])
		context := eval.Context{Program: program, Scope: program.Scope, Run: a}
		r := program.EvaluateWith(expression.Expr, &context)
		if r.Diagnostic.Code != "EXPR_INVALID" || len(context.Effects) != 0 {
			t.Error("Kash text compilation was accepted or performed effects")
		}
		r.Free(a)
		eval.FreeEffects(a, context.Effects)
		expression.Free()
	}
	r := evaluate(t, program, `(kash => kash "authored") (value => value)`)
	if r.Diagnostic.Code != "" || r.Value.Text != "authored" {
		t.Error("built-in interpreter selection changed lexical lookup")
	}
	r.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestKashInterpreterDefinitionsPublish(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "test.km", "SHELL = kash\nselected = SHELL\n")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("SHELL")
	engine.Request(node)
	for i := 0; i < 10 && !node.Current; i++ {
		engine.Step()
	}
	if !node.Current || !program.IsKashSelector(node.Latest) {
		t.Error("Kash interpreter could not cross the definition boundary")
	}
	node = program.Definition("selected")
	engine.Request(node)
	for i := 0; i < 10 && !node.Current; i++ {
		engine.Step()
	}
	if !node.Current || node.Latest.Kind != core.Callable || node.Latest.HasTransientCallable() {
		t.Error("aliased interpreter definition could not publish")
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
	text := "def result = (list (probe 75 1 2) (probe 0))\ndef probe status rest...\n\tdef nested code\n\t\treturn code\n\tif (count rest == 2)\n\t\treturn status\n\t(nested 3)\n\treturn (count rest)\n\t(fail! \"unreachable\")\n"
	parsed := script.ParseKash(a, "functions.kash", text)
	p := eval.Compile(a, engine, parsed, registry)
	node := p.Definition("result")
	engine.Request(node)
	stepUntilCurrent(engine, node)
	if !node.Current || node.Diagnostic.Code != "" || len(node.Latest.List) != 2 || node.Latest.List[0].Int != 75 || node.Latest.List[1].Int != 0 {
		t.Error("native function failed to bind parameters, return typed integers, or unwind nested calls")
	}
	if request := p.Requests.Next(); request.OK {
		request.Request.Free(a)
		t.Error("native value return launched a host process")
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
	parsed := script.ParseKash(a, "fresh.kash", "def probe ()\n\tdef local = (tick ())\n\treturn local\ndef result = (map (unused => (probe ())) [1, 2, 3])\n")
	p := eval.Compile(a, engine, parsed, registry)
	node := p.Definition("result")
	engine.Request(node)
	stepUntilCurrent(engine, node)
	if !node.Current || node.Diagnostic.Code != "" || len(node.Latest.List) != 3 || state.Calls != 3 {
		t.Error("separate native calls shared local effects")
	} else {
		for i := range node.Latest.List {
			if node.Latest.List[i].Int != int64(i+1) {
				t.Error("native call returned a stale local definition")
			}
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
