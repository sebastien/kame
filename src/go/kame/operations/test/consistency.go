package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/math"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestPredicateAndSequenceConsistency(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "consistency.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	checks := []string{
		`(eq? 1 1.0)`, `(ne? 1 2)`, `(lt? 1 2)`, `(gt? 2 1)`, `(lte? 1 1)`, `(gte? 1 1)`,
		`(in? [x: :nil] [[x: :nil]])`, `(not (in? "x" ["xy"]))`, `(eq? (has? 1 [1.0]) (in? 1 [1.0]))`,
		`(nil? :nil)`, `(not (nil? uppercase))`, `(bool? :false)`, `(int? 1)`, `(float? 1.5)`,
		`(number? 1)`, `(number? 1.5)`, `(not (number? "1"))`, `(callable? uppercase)`,
		`(resource? (resource "mem://fixture/file"))`, `(pattern? ./{name:*})`, `(not (process? [status: 0]))`,
		`(empty? "")`, `(empty? [])`, `(empty? (merge))`, `(not (empty? :nil))`, `(not (empty? :false))`,
		`(not (empty? 0))`, `(not (empty? " "))`, `(not (empty? uppercase))`, `(not (empty? [:nil]))`,
		`(blank? :nil)`, `(not (blank? []))`,
		`(has-key? "x" [x: :nil])`, `(has-key? "x" [x: uppercase])`, `(not (has-key? "missing" [x: :nil]))`,
		`(eq? (if (has-key? "x" [x: :nil]) (get "x" [x: :nil]) (fail! "unselected")) :nil)`,
		`(success? [status: 0 signal: 0])`, `(not (success? [status: 75 signal: 0]))`,
		`(not (success? [status: 0 signal: 15]))`,
		`(eq? (first "é🙂") (nth 0 "é🙂"))`, `(eq? (first "") :nil)`, `(eq? (first []) :nil)`,
		`((first [nil?]) :nil)`,
	}
	for i := range checks {
		r := evaluate(t, p, checks[i])
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Bool || !r.Value.Bool {
			t.Error("consistency check failed: " + checks[i])
		}
		r.Free(a)
	}
	invalid := []string{
		`(in? 1 [x: 1])`, `(has-key? 1 [x: 1])`, `(has-key? "x" [])`,
		`(success? [])`, `(success? [status: 0])`, `(success? [signal: 0])`,
		`(success? [status: "0" signal: 0])`, `(success? [status: 0 signal: :false])`,
		`(success? [status: uppercase signal: 0])`, `(apply uppercase [])`, `(apply uppercase ["a" "b"])`,
	}
	for i := range invalid {
		r := evaluate(t, p, invalid[i])
		if r.Diagnostic.Code != "EXPR_INVALID" {
			t.Error("invalid consistency call accepted: " + invalid[i])
		}
		r.Free(a)
	}
	r := evaluate(t, p, `(get "x" (fail! "eager default") [x: 1])`)
	if r.Diagnostic.Code != "USER_ERROR" {
		t.Error("get silently changed its eager-default contract")
	}
	r.Free(a)
	c := eval.Context{Program: p, Scope: p.Scope, Run: a}
	for kind := core.Nil; kind <= core.Process; kind++ {
		predicate := evaluate(t, p, core.KindName(kind)+"?")
		for input := core.Nil; input <= core.Process; input++ {
			r = c.Call(predicate.Value, []core.Value{{Kind: input}})
			if r.Diagnostic.Code != "" || r.Value.Kind != core.Bool || r.Value.Bool != (input == kind) {
				t.Error("kind predicate rejected or misclassified an opaque value")
			}
			r.Free(a)
		}
		predicate.Free(a)
	}
	empty := evaluate(t, p, "empty?")
	r = c.Call(empty.Value, []core.Value{{Kind: core.Bytes}})
	if r.Diagnostic.Code != "" || !r.Value.Bool {
		t.Error("empty bytes were not empty")
	}
	r.Free(a)
	empty.Free(a)
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestExactNumericOrdering(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "ordering.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	checks := []string{
		`(< 9007199254740992 9007199254740993)`, `(> 9007199254740993 9007199254740992)`,
		`(< -9223372036854775808 9223372036854775807)`,
		`(< 9223372036854775807 9223372036854775808.0)`,
		`(> 9223372036854775807 9223372036854774784.0)`,
		`(= -9223372036854775808 -9223372036854775808.0)`,
		`(> -9223372036854775808 -9223372036854777856.0)`,
		`(< -9223372036854775808 -9223372036854774784.0)`,
		`(> 9007199254740993 9007199254740992.0)`,
		`(< 9007199254740992.0 9007199254740993)`,
		`(< 0 0.5)`, `(> 0 -0.5)`, `(< -1 -0.5)`, `(> 1 0.5)`,
		`(<= 0 -0.0)`, `(>= -0.0 0)`,
		`(eq? (lt 1 2) (lt? 1 2))`, `(eq? (is 1 1) (eq? 1 1))`,
	}
	for i := range checks {
		r := evaluate(t, p, checks[i])
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Bool || !r.Value.Bool {
			t.Error("numeric ordering failed: " + checks[i])
		}
		r.Free(a)
	}
	less := evaluate(t, p, "lt?")
	c := eval.Context{Program: p, Scope: p.Scope, Run: a}
	operands := []core.Value{{Kind: core.Int, Int: 9223372036854775807}, {Kind: core.Float, Float: math.Inf(1)}}
	r := c.Call(less.Value, operands)
	if r.Diagnostic.Code != "" || !r.Value.Bool {
		t.Error("finite integer was not below positive infinity")
	}
	r.Free(a)
	operands[0] = core.Value{Kind: core.Float, Float: math.Inf(-1)}
	operands[1] = core.Value{Kind: core.Int, Int: -9223372036854775808}
	r = c.Call(less.Value, operands)
	if r.Diagnostic.Code != "" || !r.Value.Bool {
		t.Error("negative infinity was not below finite integer")
	}
	r.Free(a)
	for i := 0; i < 2; i++ {
		operands[i] = core.Value{Kind: core.Float, Float: math.NaN()}
		r = c.Call(less.Value, operands)
		if r.Diagnostic.Code != "EXPR_INVALID" {
			t.Error("NaN was given a fictitious order")
		}
		r.Free(a)
		operands[i] = core.Value{Kind: core.Int}
	}
	less.Free(a)
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestPredicateNamingContract(t *testing.T) {
	r := eval.NewRegistry(t.Allocator())
	operations.Register(r)
	canonical := []string{"eq?", "ne?", "lt?", "gt?", "lte?", "gte?", "in?", "has-key?", "success?", "empty?",
		"nil?", "bool?", "number?", "int?", "float?", "string?", "bytes?", "list?", "record?", "callable?", "resource?", "pattern?", "process?"}
	for i := range canonical {
		if !strings.HasSuffix(canonical[i], "?") || !r.HasOperation(canonical[i]) {
			t.Error("missing canonical predicate: " + canonical[i])
		}
	}
	aliases := []string{"eq", "is", "ne", "lt", "gt", "lte", "gte", "has?"}
	for i := range aliases {
		if !r.HasOperation(aliases[i]) {
			t.Error("compatibility alias was removed: " + aliases[i])
		}
	}
	if r.HasOperation("is?") || !r.HasOperation("not") || !r.HasOperation("bool") {
		t.Error("identity alias or logical/conversion exceptions changed")
	}
	r.Free()
}
