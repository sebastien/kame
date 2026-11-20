package evalcheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
)

func TestListSpreadValuesTypesAndCallableOwnership(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "spread.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	r := evaluate(t, p, `((prefix => [0, ...prefix, ...[], 3, ...[4]]) [1, [2], nil])`)
	if r.Diagnostic.Code != "" || len(r.Value.List) != 6 || r.Value.List[2].Kind != core.List || r.Value.List[3].Kind != core.Nil || r.Value.List[5].Int != 4 {
		t.Error("spread changed order, empty list behavior, or nesting")
	}
	r.Free(a)
	r = evaluate(t, p, `((functions => (map (f => (f 42)) [...functions])) [(x => x)])`)
	if r.Diagnostic.Code != "" || len(r.Value.List) != 1 || r.Value.List[0].Int != 42 {
		t.Error("spread lost callable ownership")
	}
	r.Free(a)
	invalid := []string{`[...nil]`, `[..."text"]`, `[...42]`, `[...(nop {name: "record"})]`, `[...(x => x)]`, `[...1, (fail! "must not run")]`}
	for i := range invalid {
		r = evaluate(t, p, invalid[i])
		if r.Diagnostic.Code != "EXPR_INVALID" {
			t.Error("spread accepted a non-list or evaluated a later entry")
		}
		r.Free(a)
	}
	r = evaluate(t, p, `(if false then [..."invalid"] else [1])`)
	if r.Diagnostic.Code != "" || len(r.Value.List) != 1 {
		t.Error("unselected spread became eager")
	}
	r.Free(a)
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
