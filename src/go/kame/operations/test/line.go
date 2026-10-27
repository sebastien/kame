package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestLineRemovesOnlyOneTerminalLF(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "line.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{`(line "")`, `(line "name")`, `(line " name \n")`, `(line "name\n\n")`, `(line "name\r\n")`, `(line "name\r")`, `(line "one\ntwo")`, `("value\n" | line)`, `(let [frame line] (frame "\n"))`}
	expected := []string{"", "name", " name ", "name\n", "name\r", "name\r", "one\ntwo", "value", ""}
	for i := range texts {
		r := evaluate(t, p, texts[i])
		if r.Diagnostic.Code != "" || r.Value.Kind != core.String || r.Value.Text != expected[i] {
			t.Error("incorrect line framing: " + texts[i])
		}
		r.Free(a)
	}
	invalid := []string{`(line)`, `(line "a" "b")`, `(line :nil)`, `(line 1)`, `(line [])`, `(line ([x] x))`, `(line [value: ([x] x)])`}
	for i := range invalid {
		r := evaluate(t, p, invalid[i])
		if r.Diagnostic.Code != "EXPR_INVALID" {
			t.Error("line accepted invalid argument: " + invalid[i])
		}
		r.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
