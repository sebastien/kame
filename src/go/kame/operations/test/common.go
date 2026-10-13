package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

// The test allocator and sanitizer check closure/wrapper release as well as
// diagnostics. Bound containers deliberately share their callable with a scope.
func TestRejectedArgumentCallableOwnership(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "ownership.km", "(identity value) = value")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		"(uppercase [f: [([x] x)]])",
		"(join [[f: ([x] x)]] \",\")",
		"(lt [[f: ([x] x)]] [])",
		"(gt [] [[f: ([x] x)]])",
		"(lte [[f: ([x] x)]] [])",
		"(gte [] [[f: ([x] x)]])",
		"(eq [[f: ([x] x)]] [])",
		"(ne [] [[f: ([x] x)]])",
		"(filter [] [f: [([x] x)]])",
		"(filter [[f: ([x] x)]] [])",
		"(map [f: [([x] x)]] [])",
		"(reduce [f: [([x] x)]] [])",
		"(flatmap ([x] [f: ([y] y)]) [1])",
		"(slice [[f: ([x] x)]] -2)",
		"(sorted [0 [f: ([x] x)]])",
		"(unique [0 [f: ([x] x)]])",
		"(apply [f: [([x] x)]] [])",
		"(tool [f: [([x] x)]])",
		"(replace [f: [([x] x)]] \"a\" \"b\")",
		"(sub [f: [([x] x)]] \"a\" \"b\")",
		"(match? [f: [([x] x)]])",
		"(match? {*} [f: [([x] x)]])",
		"((match? {*}) [f: [([x] x)]])",
		"(replace ./a/{name} \"b\" [[f: ([x] x)]])",
		"(regex-replace ./a/{name} \"b\" [[f: ([x] x)]])",
		"(capture 0 [captures: ([x] x)])",
		"(capture \"x\" [named: [([x] x)]])",
		"(render \"text\" [f: [([x] x)]] \"unknown\")",
		"(let [f ([x] x)] (uppercase [f: [f]]))",
		"(let [f ([x] x)] (eq [f: [f]] []))",
		"(let [xs [identity]] (uppercase [f: xs]))",
		"(let [xs [identity]] (eq xs xs))",
		"(let [xs [identity]] (join [xs] \",\"))",
		"(let [xs [identity]] (filter [xs] xs))",
	}
	for i := range texts {
		r := evaluate(t, p, texts[i])
		if (r.Diagnostic.Code != "EXPR_INVALID" && r.Diagnostic.Code != "PAT_INVALID" && r.Diagnostic.Code != "TPL_STYLE") || r.Waiting {
			t.Error("nested callable argument was not rejected: " + texts[i])
		}
		r.Free(a)
	}
	r := evaluate(t, p, "(render \"text\" [f: [([x] x)]])")
	if r.Diagnostic.Code != "" || r.Value.Kind != core.String || r.Value.Text != "text" {
		t.Error("render freed transferred payload callable ownership twice")
	}
	r.Free(a)
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
