package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestStreamPredicatesArePureBooleanAndHostFree(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	defer registry.Free()
	parsed := script.Parse(a, "predicates.km", "flag = :true\nmatching = ([x] (eq x \"a\"))\nbad = (read ./missing)\ncycle = cycle\n")
	defer parsed.Free()
	engine := core.NewEngine(a)
	p := eval.Compile(a, engine, parsed, registry)
	defer p.Free()
	defer engine.Free()
	texts := []string{"(eq item \"a\")", "([x] (eq x \"a\"))", "(let [x item] (eq x \"a\"))", "(eq item \"b\")", "item", "(read ./missing)", "(out! item)", "$(printf x)", "(eval \"1\")", "(env \"HOME\")"}
	expected := []int{1, 1, 1, 0, -eval.StreamPredicateBoolean, -eval.StreamPredicateEffect, -eval.StreamPredicateEffect, -eval.StreamPredicateEffect, -eval.StreamPredicateEffect, -eval.StreamPredicateEffect}
	for i := range texts {
		r := expr.Parse(a, "predicate.km", texts[i])
		if len(r.Diagnostics) != 0 { t.Errorf("predicate parse failed: %s", texts[i]); r.Free(); continue }
		actual := p.EvaluateStreamPredicate(r.Expr, p.Scope, []byte("a\r\n"))
		if actual != expected[i] { t.Errorf("%s: got %d want %d", texts[i], actual, expected[i]) }
		r.Free()
	}
	names := []string{"flag", "matching", "bad", "cycle"}
	statuses := []int{1, 1, -eval.StreamPredicateEffect, -eval.StreamPredicateFailure}
	for i := range names {
		r := expr.Parse(a, "predicate.km", names[i])
		actual := p.EvaluateStreamPredicate(r.Expr, p.Scope, []byte("a\n"))
		if actual != statuses[i] { t.Errorf("%s: got %d want %d", names[i], actual, statuses[i]) }
		r.Free()
	}
}
