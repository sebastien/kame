package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestScalarTextAndExplicitJSONBoundaries(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "serialization.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{`(str)`, `(str "n=" 42 "/" 1.5 "/" :true "/" :nil)`, `(json "a\n\\\"")`, `(json [b: [1 :nil :false] a: "x"])`, `(str [])`, `(str [a: 1])`, `(str ./a/{*})`, `(str (resource "mem://x/a"))`, `(str ([x] x))`, `(json ([x] x))`, `(json [bad: ([x] x)])`, `(json (resource "mem://x/a"))`, `(json ./a/{*})`}
	wants := []string{"", "n=42/1.5/true/nil", "\"a\\n\\\\\\\"\"", "{\"a\":\"x\",\"b\":[1,null,false]}"}
	for i := range texts {
		r := evaluate(t, p, texts[i])
		if i < len(wants) {
			if r.Diagnostic.Code != "" || r.Value.Kind != core.String || r.Value.Text != wants[i] { t.Error("scalar text or JSON encoding failed") }
			if i >= 2 {
				var decoded core.Value
				if !core.ParseJSON(a, []byte(r.Value.Text), &decoded) { t.Error("JSON result did not parse") }
				decoded.Free(a)
			}
		} else if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("nontext or non-JSON value was accepted") }
		r.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
