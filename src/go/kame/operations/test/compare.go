package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/math"
	"solod.dev/so/testing"
)

func TestStructuralEqualityAndFilterShorthand(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "equality.km", "(identity value) = value")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		"(eq [1 [2 :nil] [a: 3 b: [4]]] [1.0 [2 :nil] [b: [4] a: 3]])",
		"(is [a: 1 b: 2] [b: 2 a: 1])", "(ne [1 2] [2 1])",
		"(eq [] [])", "(eq (parse-json \"{}\") (parse-json \"{}\"))",
		"(ne [] (parse-json \"{}\"))", "(ne [a: 1] [b: 1])",
		"(ne [a: 1] [a: 1 b: 2])", "(ne [1] [1 2])",
		"(ne [1] [\"1\"])", "(ne :nil :false)",
		"(ne 9007199254740992 9007199254740993)",
		"(ne 9007199254740993 9007199254740992.0)",
		"(eq 9007199254740992 9007199254740992.0)",
		"(ne 9223372036854775807 9223372036854775808.0)",
		"(eq -9223372036854775808 -9223372036854775808.0)",
		// Ordering retains float64 coercion, unlike exact integer equality.
		"(not (lt 9007199254740992 9007199254740993))",
		"(not (gt 9007199254740993 9007199254740992))",
		"(lte 9007199254740993 9007199254740992)",
		"(gte 9007199254740992 9007199254740993)",
	}
	for i := range texts {
		r := evaluate(t, p, texts[i])
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Bool || !r.Value.Bool { t.Error("equality contract failed: " + texts[i]) }
		r.Free(a)
	}
	r := evaluate(t, p, "(filter [[a: 1 b: 2] [a: 2] [b: 2 a: 1]] [b: 2 a: 1])")
	if r.Diagnostic.Code != "" || len(r.Value.List) != 2 { t.Error("filter did not use structural equality") }
	r.Free(a)
	invalid := []string{"(eq count :nil)", "(ne [0 count] [1])", "(is [] [count])", "(eq [f: count] [other: 1])", "(eq ./a/{name} \"a\")", "(eq (resource \"mem://test/a\") :nil)", "(filter [[count]] [])", "(eq [([x] x)] [])", "(let [xs [identity]] (eq xs xs))", "(let [xs [identity]] (filter [xs] xs))", "(lt [] [])"}
	for i := range invalid {
		r = evaluate(t, p, invalid[i])
		if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("opaque value or unordered kind accepted: " + invalid[i]) }
		r.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestByteEqualityAndHostValueLimits(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "equality.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	function := evaluate(t, p, "eq")
	c := eval.Context{Program: p, Scope: p.Scope, Run: a}
	first := core.Value{Kind: core.Bytes, Bytes: []byte{0, 255, 13, 10}}
	second := core.Value{Kind: core.Bytes, Bytes: []byte{0, 255, 13, 10}}
	r := c.Call(function.Value, []core.Value{first, second})
	if r.Diagnostic.Code != "" || !r.Value.Bool { t.Error("equal binary values differ") }
	r.Free(a)
	second.Bytes = []byte{0, 255, 13}
	r = c.Call(function.Value, []core.Value{first, second})
	if r.Diagnostic.Code != "" || r.Value.Bool { t.Error("binary equality ignored length") }
	r.Free(a)
	second.Bytes = []byte{0, 254, 13, 10}
	r = c.Call(function.Value, []core.Value{first, second})
	if r.Diagnostic.Code != "" || r.Value.Bool { t.Error("binary equality ignored a byte") }
	r.Free(a)
	r = c.Call(function.Value, []core.Value{first, core.Value{Kind: core.String, Text: string(first.Bytes)}})
	if r.Diagnostic.Code != "" || r.Value.Bool { t.Error("bytes coerced to string") }
	r.Free(a)
	var nested [257]core.Value
	for i := 0; i < 256; i++ { nested[i] = core.Value{Kind: core.List, List: nested[i+1:i+2]} }
	r = c.Call(function.Value, []core.Value{nested[0], core.Value{Kind: core.Nil}})
	if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("deep host value bypassed equality limit") }
	r.Free(a)
	r = c.Call(function.Value, []core.Value{nested[1], nested[1]})
	if r.Diagnostic.Code != "" || !r.Value.Bool { t.Error("valid equality depth rejected") }
	r.Free(a)
	nested[0].List = nested[:1]
	r = c.Call(function.Value, []core.Value{nested[0], core.Value{Kind: core.Nil}})
	if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("cyclic host container accepted") }
	r.Free(a)
	duplicate := core.Value{Kind: core.Record, Record: []core.RecordField{{Key: "key"}, {Key: "key"}}}
	r = c.Call(function.Value, []core.Value{duplicate, duplicate})
	if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("duplicate host record keys accepted") }
	r.Free(a)
	r = c.Call(function.Value, []core.Value{core.Value{Kind: core.Process}, core.Value{Kind: core.Nil}})
	if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("process identity used as equality") }
	r.Free(a)
	for i := 0; i < 2; i++ {
		floating := math.NaN()
		if i == 1 { floating = math.Inf(1) }
		r = c.Call(function.Value, []core.Value{core.Value{Kind: core.Int}, core.Value{Kind: core.Float, Float: floating}})
		if r.Diagnostic.Code != "" || r.Value.Bool { t.Error("nonfinite float equaled integer") }
		r.Free(a)
	}
	function.Free(a)
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
