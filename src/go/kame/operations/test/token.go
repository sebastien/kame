package operationscheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
)

func TestSubToken(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("standard library registration failed")
	}
	parsed := script.Parse(a, "token", "")
	program := eval.Compile(a, engine, parsed, registry)
	cases := []string{
		`(eq (sub-token "Font" "New" "Font,Font Font-alt Font_1 éFont Font² /Font/") "New,New Font-alt Font_1 éFont Font² /New/")`,
		`(eq (sub-token "a.b" "$1" "a.b,a.b") "$1,$1")`,
		`(eq (sub-token "a" "aa" "a,a") "aa,aa")`,
		`(eq (sub-token "Font" "New" "ⅣFont Font① Font- -Font") "ⅣFont Font① Font- -Font")`,
	}
	for i := range cases {
		r := evaluate(t, program, cases[i])
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Bool || !r.Value.Bool {
			t.Errorf("sub-token case %d failed: %s", i, r.Diagnostic.Message)
		}
		r.Free(a)
	}
	r := evaluate(t, program, `(sub-token "" "x" "text")`)
	if r.Diagnostic.Code == "" {
		t.Error("empty sub-token search accepted")
	}
	r.Free(a)
	program.Free()
	parsed.Free()
	registry.Free()
	engine.Free()
}
