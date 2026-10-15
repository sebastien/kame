package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestRemediation3NumericBounds(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "bounds.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		"(nth 4294967296 [7])", "(nth 4294967296 \"a\")",
		"(nth -4294967296 [7])", "(nth 9223372036854775807 [([x] x)])",
	}
	for i := range texts {
		r := evaluate(t, p, texts[i])
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Nil { t.Error("oversized nth index wrapped") }
		r.Free(a)
	}
	texts = []string{
		"(slice 4294967296 [7])", "(slice 0 4294967296 \"a\")",
		"(slice -4294967296 [7])", "(slice 0 -9223372036854775808 [([x] x)])",
	}
	for i := range texts {
		r := evaluate(t, p, texts[i])
		if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("oversized slice bound wrapped") }
		r.Free(a)
	}
	p.Free()
	parsed.Free()
	registry.Free()
	engine.Free()
}
