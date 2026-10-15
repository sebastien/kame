package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func applyListSize(c *eval.Context, state any, values []core.Value) eval.Result {
	_, _ = c, state
	if len(values) != 1 || values[0].Kind != core.List {
		return eval.Result{Diagnostic: core.Diagnostic{Code: "EXPR_INVALID"}}
	}
	return eval.Result{Value: core.Value{Kind: core.Int, Int: int64(len(values[0].List))}}
}

func TestApplyNativeAndLambdaArguments(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	if !registry.Add(eval.Operation{Name: "list-size", Call: applyListSize, MinArity: 1, MaxArity: 1}) {
		t.Error("custom unary operation registration failed")
	}
	parsed := script.Parse(a, "apply.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	checks := []string{
		`(eq (apply count [1 2]) 2)`,
		`(eq ((nop [1 2]) | apply count) 2)`,
		`(eq (apply count []) 0)`,
		`(eq (apply count [1]) 1)`,
		`(eq (apply first [1 2]) 1)`,
		`(eq ((nop [1 2]) | apply first) 1)`,
		`(eq (apply first []) :nil)`,
		`(eq (apply first [1]) 1)`,
		`(eq (let [size count] (apply size [1 2])) 2)`,
		`(eq (apply list-size [1 2]) 2)`,
		`(eq ((nop []) | apply list-size) 0)`,
		`(eq (apply nth [1 [10 20]]) 20)`,
		`(eq ((nop [1 [10 20]]) | apply nth) 20)`,
		`(eq (apply get ["x" [x: 7]]) 7)`,
		`(eq (apply str ["a" "b"]) "ab")`,
		`(eq ((nop ["a" "b"]) | apply str) "ab")`,
		`(eq (apply str []) "")`,
		`(eq (apply list [1 2]) [1 2])`,
		`(eq (apply list []) [])`,
		`(eq (apply ([xs] (count xs)) [1 2]) 2)`,
		`(eq ((nop [1 2]) | apply ([xs] (first xs))) 1)`,
		`(eq (apply ([x y] (str x y)) ["a" "b"]) "ab")`,
		`(eq ((nop ["a" "b"]) | apply ([x y] (str x y))) "ab")`,
	}
	for i := range checks {
		r := evaluate(t, p, checks[i])
		if r.Diagnostic.Code != "" || r.Waiting || r.Value.Kind != core.Bool || !r.Value.Bool {
			t.Error("apply argument check failed: " + checks[i])
		}
		r.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
