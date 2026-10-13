package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestComputedPatternMatchAndReplacement(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "computed", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		`(match? @(ROOT)/{name:*}.@(EXT) "./src/demo.c")`,
		`(replace @(ROOT)/{name:*}.@(EXT) @(OUT)/{name} "./src/demo.c")`,
		`(capture "name" (regex-match @(ROOT)/{name:~[a-z]+}.@(EXT) "./src/demo.c"))`,
		`(regex-replace @(ROOT)/{name:~[a-z]+}.@(EXT) @(OUT)/{name} "./src/demo.c")`,
		`(match "./src/demo.c" [@(ROOT)/{name:*}.@(EXT) name])`,
		`(match? ./a*/@(EXT)/{name:*} "./abc/c/demo")`,
		`(match? ./a*/@(EXT)/{name:*} "./a*/c/demo")`,
	}
	for i := range texts {
		r := evaluate(t, p, `(let [ROOT "./src" OUT """../out {literal}""" EXT "c"] ` + texts[i] + `)`)
		if r.Diagnostic.Code != "" { t.Errorf("computed operation failed: %s", r.Diagnostic.Code) }
		if i == 0 || i == 6 {
			if r.Value.Kind != core.Bool || !r.Value.Bool { t.Error("computed match failed") }
		} else if i == 5 {
			if r.Value.Kind != core.Bool || r.Value.Bool { t.Error("authored literal glob outside a capture became syntax") }
		} else {
			want := "demo"
			if i == 1 || i == 3 { want = "../out {literal}/demo" }
			if r.Value.Kind != core.String || r.Value.Text != want { t.Error("computed capture/expansion failed") }
		}
		r.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
