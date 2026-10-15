package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestCanonicalTransformationPipes(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	r := eval.NewRegistry(a)
	operations.Register(r)
	source := script.Parse(a, "conventions.km", "")
	p := eval.Compile(a, engine, source, r)
	checks := []string{
		`(eq ((nop [x: 1]) | set "x" 2) [x: 2])`,
		`(let [r [x: 1]] (eq (list (r | set "x" 2) r) [[x: 2] [x: 1]]))`,
		`(eq ("./src/demo.c" | resub ./src/{name:~[a-z]+}.c ./out/{name}.o) "./out/demo.o")`,
		`(eq ("./src/demo.c" | regex-match ./src/{name:~[a-z]+}.c | capture "name") "demo")`,
		`(eq ("abc" | digest "sha256") (digest "sha256" "abc"))`,
	}
	for i := range checks {
		result := evaluate(t, p, checks[i])
		if result.Diagnostic.Code != "" || result.Value.Kind != core.Bool || !result.Value.Bool {
			t.Error("canonical transformation pipe failed: " + checks[i])
		}
		result.Free(a)
	}
	p.Free(); source.Free(); r.Free(); engine.Free()
}

func TestLiteralSubSignatureErrors(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	r := eval.NewRegistry(a)
	operations.Register(r)
	source := script.Parse(a, "sub.km", "")
	p := eval.Compile(a, engine, source, r)
	checks := []string{`(sub "a" "b")`, `(sub 1 "b" "a")`, `(sub "a" 1 "a")`, `(sub "a" "b" [])`, `(replace "a" "b")`}
	for i := range checks {
		result := evaluate(t, p, checks[i])
		if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("literal substitution must report a signature error: " + checks[i]) }
		result.Free(a)
	}
	p.Free(); source.Free(); r.Free(); engine.Free()
}

func TestShRejectsUnsupportedOptionsBeforeDryRun(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	r := eval.NewRegistry(a)
	operations.Register(r)
	source := script.Parse(a, "sh.km", "")
	p := eval.Compile(a, engine, source, r)
	p.DryRun = true
	checks := []string{
		`(sh! "true")`, `(sh! (merge) "true")`, `("true" | sh! (merge))`,
		`(sh! [cwd: "ignored"] "true")`, `("true" | sh! [env: (merge)])`,
		`(sh! [unknown: ([x] x)] "true")`, `(sh! [] "true")`,
	}
	for i := range checks {
		parsed := expr.Parse(a, "sh.km", checks[i])
		if len(parsed.Diagnostics) != 0 { t.Error("shell convention test did not parse") }
		context := eval.Context{Program: p, Scope: p.Scope, Run: a, Grants: []eval.Grant{{Capability: eval.Run}}}
		result := p.EvaluateWith(parsed.Expr, &context)
		if i < 3 {
			if result.Diagnostic.Code != "" || result.Waiting || result.Value.Kind != core.String { t.Error("empty/no shell options rejected: " + checks[i]) }
		} else if result.Diagnostic.Code != "EXPR_INVALID" || result.Waiting {
			t.Error("unsupported shell options silently ignored: " + checks[i])
		}
		result.Free(a)
		parsed.Free()
	}
	p.Free(); source.Free(); r.Free(); engine.Free()
}
