package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestCallableOwnershipRemediation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "ownership.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		"(with [f: ([x] x)] (f 42))",
		"(with [f: ([x] x) f: ([x] x)] (f 42))",
		"(let [xs [([x] x)]] ((nth 0 xs) 42) ((nth 0 xs) 42))",
		"(let [r [f: ([x] x) unused: ([x] x)]] (r.f 42) (r.f 42))",
		"((let [f (match? {*})] f) \"a\")",
		"((let [xs [(match? {*})]] (first xs)) \"a\")",
		"((eval \"([x] x)\") 42)",
		"(((eval \"([x] ([y] x))\") 42) 0)",
		"(bool ([x] x))",
		"(count [([x] x)])",
		"(first [42 ([x] x)])",
		"(nth 1 [([x] x) 42 ([x] x)])",
		"(nth 2 [([x] x)])",
		"(slice 1 2 [([x] x) 42 ([x] x)])",
		"(nop ([x] x) 42)",
		"(filter ([x] [([y] y)]) [1])",
		"(map ([x] ([y] y)) [1 2])",
		"(flatmap ([x] [([y] y)]) [1 2])",
		"(reduce ([acc x] acc) ([y] y) [1 2])",
		"(let [xs [[f: ([x] x)]]] 42)",
		"(let [r [f: [([x] x)]]] 42)",
		"(let [xs [([x] x)]] (apply (first xs) [42]))",
		"(let [xs [([x] x)]] ((first (filter ([x] :true) xs)) 42))",
	}
	for i := range texts {
		input := expr.Parse(a, "ownership", texts[i])
		if len(input.Diagnostics) != 0 {
			t.Error("parse failed: " + texts[i])
			input.Free()
			continue
		}
		result := p.Evaluate(a, input.Expr, p.Scope)
		if result.Waiting || result.Diagnostic.Code != "" {
			t.Error("evaluation failed: " + texts[i])
		}
		result.Free(a)
		input.Free()
	}
	result := evaluate(t, p, "(([x] x) ([y] y) 1)")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("arity rejection missing")
	}
	result.Free(a)
	result = evaluate(t, p, "((match? {*}) ([y] y) 1)")
	if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("native arity rejection missing") }
	result.Free(a)
	result = evaluate(t, p, "(map ([x] (if (eq x 1) ([y] y) (uppercase 42))) [1 2])")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("callback rejection missing")
	}
	result.Free(a)
	withInput := expr.Parse(a, "reserved-with", "(with [f: ([x] x) env: 42] 0)")
	withInput.Expr.Kash = true
	result = p.Evaluate(a, withInput.Expr, p.Scope)
	if result.Diagnostic.Code != "DEF_INVALID" {
		t.Error("reserved with field was not rejected")
	}
	result.Free(a)
	withInput.Free()
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestContextCallBorrowsCallableOperands(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "call.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	identity := evaluate(t, p, "(eval \"([x] x)\")")
	operand := evaluate(t, p, "(eval \"([x] x)\")")
	c := &eval.Context{Program: p, Scope: p.Scope, Run: a}
	for i := 0; i < 2; i++ {
		result := c.Call(identity.Value, []core.Value{operand.Value})
		if result.Diagnostic.Code != "" || result.Value.Kind != core.Callable { t.Error("callable operand was not returned") }
		result.Free(a)
	}
	result := c.Call(operand.Value, []core.Value{{Kind: core.Int, Int: 42}})
	if result.Diagnostic.Code != "" || result.Value.Int != 42 { t.Error("Call consumed its borrowed operand") }
	result.Free(a)
	operand.Free(a)
	identity.Free(a)
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestDefinitionArgsReplacement(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "args.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	first := core.NewString(a, "first")
	second := core.NewString(a, "second")
	p.SetDefinitionArgs([]core.Value{first})
	p.SetDefinitionArgs([]core.Value{second, first})
	if len(p.DefinitionArgs) != 2 || p.DefinitionArgs[0].Text != "second" {
		t.Error("replacement retained old arguments")
	}
	p.SetDefinitionArgs(nil)
	if len(p.DefinitionArgs) != 0 || !p.DefinitionArgsSet {
		t.Error("empty arguments not installed")
	}
	first.Free(a)
	second.Free(a)
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestDuplicateRecordTextCoercion(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "duplicate.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	input := expr.Parse(a, "duplicate", "(out! [b: 3 a: 1 a: 2])")
	c := &eval.Context{Program: p, Scope: p.Scope, Run: a}
	result := p.EvaluateWith(input.Expr, c)
	if result.Diagnostic.Code != "" || len(c.Effects) != 1 {
		t.Error("duplicate record coercion failed")
	} else if string(c.Effects[0].Data) != "{\"a\":1,\"a\":2,\"b\":3}" {
		t.Error("duplicate record ordering changed")
	}
	result.Free(a)
	eval.FreeEffects(a, c.Effects)
	input.Free()
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
