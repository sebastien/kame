package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestMatchAndSub(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Fatal("registration failed")
		return
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	cases := []string{
		`(match? ./{*}.{*} "./foo.txt")`,
		`(not (match? ./{*}.{*} "./foo"))`,
		`(not (match? ./{*}.{*} "./.txt"))`,
		`((match? ./{*}.{*}) "./foo.txt")`,
		`(match? {name:~[a-z]+} "demo")`,
		`(match? (pattern (str "{" "name:*}")) "demo")`,
		`(match? ./{prefix:**}/{name:*} "./a/b/c")`,
		`(match? {name:*}-{name:*} "a-a")`,
		`(not (match? {name:*}-{name:*} "a-b"))`,
		`(eq (map (match? ./{*}.{*}) ["./foo" "./foo.txt"]) [:false :true])`,
		`(eq (filter-out (match? ./{*}.{*}) ["./foo" "./foo.txt"]) ["./foo"])`,
		`(eq (filter (match? ./{*}.{*}) ["./foo" "./foo.txt"]) ["./foo.txt"])`,
		`(eq (filter-out (match? ./{*}.{*}) (map (sub ./journal/{name:*} ./{name}) ["./journal/foo" "./journal/bar.txt"])) ["./foo"])`,
		`(eq (sub ./journal/{name:*} missing "./other/foo") "./other/foo")`,
		`(eq (sub ./journal/{name:*} ./{name} ["./journal/foo" "./other/foo"]) ["./foo" "./other/foo"])`,
		`(eq (sub "a" "b" "banana") (replace "a" "b" "banana"))`,
		`(eq (sub {name:*}.c {name}.o ["a.c" "b.h"]) (replace {name:*}.c {name}.o ["a.c" "b.h"]))`,
		`(eq (sub {*}.c "x" []) [])`,
		`(eq (sub {*}.c "x" :nil) :nil)`,
		`(let [name "outer"] (nop (match? {name:*} "inner") (eq name "outer")))`,
	}
	for i := range cases {
		result := evaluate(t, program, cases[i])
		if result.Diagnostic.Code != "" || result.Value.Kind != core.Bool || !result.Value.Bool {
			t.Error("match/sub failed: " + cases[i])
		}
		result.Free(a)
	}
	rejections := []string{
		`(match? "plain" "plain")`,
		`(match? {*} :nil)`,
		`(match? {*} ["a"])`,
		`((match? {*}) 42)`,
		`((match? {*}) "a" "b")`,
		`(match? {name} "a")`,
		`(match? (pattern "plain"))`,
		`(sub {*}.c "x" ["a.c" :nil])`,
	}
	for i := range rejections {
		result := evaluate(t, program, rejections[i])
		if result.Diagnostic.Code == "" {
			t.Error("invalid match/sub accepted: " + rejections[i])
		}
		result.Free(a)
	}
	var subject strings.Builder
	subject = strings.NewBuilder(a)
	for i := 0; i < 1600; i++ {
		subject.WriteByte('a')
	}
	budgetCalls := []string{"(match? {~a*}z \"", "((match? {~a*}z) \"", "(sub {~a*}z \"x\" \"", "(replace {~a*}z \"x\" \""}
	for i := range budgetCalls {
		result := evaluate(t, program, budgetCalls[i]+subject.String()+"\")")
		if result.Diagnostic.Code != "PAT_LIMIT" {
			t.Error("matcher budget failure became a nonmatch: " + budgetCalls[i])
		}
		result.Free(a)
	}
	subject.Free()
	program.Free()
	registry.Free()
	engine.Free()
	parsed.Free()
}
