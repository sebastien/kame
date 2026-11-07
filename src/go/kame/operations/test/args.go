package operations_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestParseArgs(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("standard library registration failed")
	}
	parsed := script.Parse(a, "args", "")
	program := eval.Compile(a, engine, parsed, registry)
	cases := []string{
		`(eq (parse-args [fonts: [names: ["-f" "--fonts"] arity: :many repeat: :append] css: [names: ["-c" "--css"] arity: :many repeat: :append]] ["-f" "a.ttf" "b.ttf" "--css" "main.css" "-f" "c.ttf"]) [options: [fonts: ["a.ttf" "b.ttf" "c.ttf"] css: ["main.css"]] operands: []])`,
		`(eq (parse-args [x: [names: ["--x"] arity: :one repeat: :replace] flag: [names: ["-v"] arity: :flag repeat: :replace]] ["operand" "--x=a" "--x=" "-v" "--" "-bad"]) [options: [x: [""] flag: :true] operands: ["operand" "-bad"]])`,
		`(eq (parse-args [x: [names: ["--x"] arity: :many repeat: :append]] ["--x=-odd" "two words" "" "-" "--" "tail"]) [options: [x: ["-odd" "two words" "" "-"]] operands: ["tail"]])`,
	}
	for i := range cases {
		r := evaluate(t, program, cases[i])
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Bool || !r.Value.Bool {
			t.Errorf("parse-args case %d failed: %s", i, r.Diagnostic.Message)
		}
		r.Free(a)
	}
	errors := []string{
		`(parse-args [x: [names: ["-x" "-x"] arity: :one repeat: :append]] [])`,
		`(parse-args [x: [names: ["-x"] arity: :one repeat: :reject]] ["-x" "a" "-x" "b"])`,
		`(parse-args [x: [names: ["-x"] arity: :many repeat: :append]] ["-x" "--"])`,
		`(parse-args [] ["--secret-value"])`,
	}
	for i := range errors {
		r := evaluate(t, program, errors[i])
		if r.Diagnostic.Code == "" {
			t.Errorf("invalid parse-args case %d accepted", i)
		}
		r.Free(a)
	}
	program.Free()
	parsed.Free()
	registry.Free()
	engine.Free()
}
