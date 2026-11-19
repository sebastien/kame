package operationscheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"strings"
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
		`(match? (pattern "{name:~[a-z]+}") "demo")`,
		`(match? (pattern (str "{" "name:*}")) "demo")`,
		`(match? ./{prefix:**}/{name:*} "./a/b/c")`,
		`(match? (pattern "{name:*}-{name:*}") "a-a")`,
		`(not (match? (pattern "{name:*}-{name:*}") "a-b"))`,
		`(eq (map (match? ./{*}.{*}) ["./foo", "./foo.txt"]) [false, true])`,
		`(eq (filter-out (match? ./{*}.{*}) ["./foo", "./foo.txt"]) ["./foo"])`,
		`(eq (filter (match? ./{*}.{*}) ["./foo", "./foo.txt"]) ["./foo.txt"])`,
		`(eq (filter-out (match? ./{*}.{*}) (map (subst ./journal/{name:*} ./{name}) ["./journal/foo", "./journal/bar.txt"])) ["./foo"])`,
		`(eq (subst ./journal/{name:*} missing "./other/foo") "./other/foo")`,
		`(eq (subst ./journal/{name:*} ./{name} ["./journal/foo", "./other/foo"]) ["./foo", "./other/foo"])`,
		`(eq (subst "a" "b" "banana") "bbnbnb")`,
		`(eq (subst (pattern "{name:*}.c") (pattern '{name}.o') ["a.c", "b.h"]) ["a.o", "b.h"])`,
		`(eq (subst (pattern "{*}.c") "x" []) [])`,
		`(eq (subst (pattern "{*}.c") "x" nil) nil)`,
		`((name => (nop (match? (pattern "{name:*}") "inner") (eq name "outer"))) "outer")`,
	}
	for i := range cases {
		result := evaluate(t, program, cases[i])
		if result.Diagnostic.Code != "" || result.Value.Kind != core.Bool || !result.Value.Bool {
			t.Error("match/subst failed: " + cases[i])
		}
		result.Free(a)
	}
	rejections := []string{
		`(match? "plain" "plain")`,
		`(match? (pattern "{*}") nil)`,
		`(match? (pattern "{*}") ["a"])`,
		`((match? (pattern "{*}")) 42)`,
		`((match? (pattern "{*}")) "a" "b")`,
		`(match? (pattern '{name}') "a")`,
		`(match? (pattern "plain"))`,
		`(subst (pattern "{*}.c") "x" ["a.c", nil])`,
	}
	for i := range rejections {
		result := evaluate(t, program, rejections[i])
		if result.Diagnostic.Code == "" {
			t.Error("invalid match/subst accepted: " + rejections[i])
		}
		result.Free(a)
	}
	var subject strings.Builder
	subject = strings.Builder{}
	for i := 0; i < 1600; i++ {
		subject.WriteByte('a')
	}
	budgetCalls := []string{"(match? (pattern \"{~a*}z\") \"", "((match? (pattern \"{~a*}z\")) \"", "(subst (pattern \"{~a*}z\") \"x\" \""}
	for i := range budgetCalls {
		result := evaluate(t, program, budgetCalls[i]+subject.String()+"\")")
		if result.Diagnostic.Code != "PAT_LIMIT" {
			t.Error("matcher budget failure became a nonmatch: " + budgetCalls[i])
		}
		result.Free(a)
	}
	subject.Reset()
	program.Free()
	registry.Free()
	engine.Free()
	parsed.Free()
}
