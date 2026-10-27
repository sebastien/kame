package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestListSpreadValuesTypesAndCallableOwnership(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "spread.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	r := evaluate(t, p, `(let [prefix [1 [2] :nil]] [0 ...prefix ...[] 3 ...[4]])`)
	if r.Diagnostic.Code != "" || len(r.Value.List) != 6 || r.Value.List[2].Kind != core.List || r.Value.List[3].Kind != core.Nil || r.Value.List[5].Int != 4 {
		t.Error("spread changed order, empty list behavior, or nesting")
	}
	r.Free(a)
	r = evaluate(t, p, `(let [functions [([x] x)]] (map ([f] (f 42)) [...functions]))`)
	if r.Diagnostic.Code != "" || len(r.Value.List) != 1 || r.Value.List[0].Int != 42 {
		t.Error("spread lost callable ownership")
	}
	r.Free(a)
	invalid := []string{`[...:nil]`, `[..."text"]`, `[...42]`, `[...[name: "record"]]`, `[...([x] x)]`, `[...1 (fail! "must not run")]`}
	for i := range invalid {
		r = evaluate(t, p, invalid[i])
		if r.Diagnostic.Code != "EXPR_INVALID" {
			t.Error("spread accepted a non-list or evaluated a later entry")
		}
		r.Free(a)
	}
	r = evaluate(t, p, `(if :false [..."invalid"] [1])`)
	if r.Diagnostic.Code != "" || len(r.Value.List) != 1 {
		t.Error("unselected spread became eager")
	}
	r.Free(a)
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
