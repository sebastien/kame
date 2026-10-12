package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestOperationValuesSupportCallbacksAliasesAndContainers(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "operations.km", "callback = count\ncallbacks = [count uppercase]\nrecord = [callback: count]\nresult = [(callback [1 2]) (map count [[1] [2 3]]) (reduce str [\"a\" \"b\"] \"\")]\n")
	p := eval.Compile(a, engine, parsed, registry)
	node := p.Definition("result")
	engine.Request(node)
	stepUntilCurrent(engine, node)
	if !node.Current || len(node.Latest.List) != 3 || node.Latest.List[0].Int != 2 || node.Latest.List[1].List[1].Int != 2 || node.Latest.List[2].Text != "ab" {
		t.Error("operation alias or callback failed")
	}
	for i := range p.Definitions {
		if p.Definitions[i].Name == "callbacks" || p.Definitions[i].Name == "record" {
			item := p.Definition(p.Definitions[i].Name)
			engine.Request(item)
			stepUntilCurrent(engine, item)
			if !item.Current || item.Latest.HasTransientCallable() {
				t.Error("operation container could not publish")
			}
		}
	}
	sources := []string{"(let [f count] (f [1 2 3]))", "(let [count ([x] 7)] (count []))", "(let [ops [callback: count]] (ops.callback [1 2]))", "(let [f =] (f 1 1))"}
	for i := range sources {
		r := evaluate(t, p, sources[i])
		if r.Diagnostic.Code != "" {
			t.Error("operation lookup or shadowing failed")
		}
		if i == 0 && r.Value.Int != 3 {
			t.Error("local operation alias failed")
		}
		if i == 1 && r.Value.Int != 7 {
			t.Error("lexical operation shadow ignored")
		}
		if i == 2 && r.Value.Int != 2 {
			t.Error("record operation call failed")
		}
		if i == 3 && !r.Value.Bool {
			t.Error("comparison alias is not first-class")
		}
		r.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestOperationValuesCannotBypassContractsOrBecomeSpecialForms(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "operations.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	sources := []string{"(let [f count] (f))", "(map count [1])", "(let [f read] (f missing))", "(map read [\"blocked\"])", "if", "run", "(map if [])", "unknown", "(unknown)"}
	codes := []string{"EXPR_INVALID", "EXPR_INVALID", "CAP_DENIED", "CAP_DENIED", "EXPR_INVALID", "EXPR_INVALID", "EXPR_INVALID", "REF_MISSING", "OP_UNKNOWN"}
	for i := range sources {
		r := evaluate(t, p, sources[i])
		if r.Diagnostic.Code != codes[i] {
			t.Error("unexpected diagnostic for " + sources[i] + ": " + r.Diagnostic.Code)
		}
		r.Free(a)
	}
	specialForms := []string{"?", "let", "def", "eval", "if", "and", "or", "with", "match", "run", "pipe", "await"}
	for i := range specialForms {
		if !eval.IsSpecialForm(specialForms[i]) || registry.Add(eval.Operation{Name: specialForms[i], Call: countOperation}) {
			t.Errorf("special form accepted as operation: %s", specialForms[i])
		}
	}
	if eval.IsSpecialForm("count") || eval.IsSpecialForm("kash") || eval.IsSpecialForm("=") {
		t.Error("callable mistaken for special form")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestAliasedOperationResumesAndInvalidates(t *testing.T) {
	checkRetainedOperands(t, "callback = async\nresult = (list (once) (callback))")
}

func TestOperationCallableRejectsForeignInvocation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "operations.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	other := eval.Compile(a, engine, parsed, registry)
	r := evaluate(t, p, "count")
	function := r.Value.Callable.(*eval.Function)
	if function.Kind != eval.FunctionOperation || function.Owner != p {
		t.Error("operation callable has wrong kind or owner")
	}
	again := evaluate(t, p, "count")
	if again.Value.Callable != r.Value.Callable {
		t.Error("operation callable was not interned")
	}
	again.Free(a)
	context := eval.Context{Program: other, Scope: other.Scope, Run: a}
	failed := context.Call(r.Value, []core.Value{core.Value{Kind: core.List}})
	if failed.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("foreign operation callable was invoked")
	}
	failed.Free(a)
	r.Free(a)
	engine.Free()
	p.Free()
	other.Free()
	parsed.Free()
	registry.Free()
}
