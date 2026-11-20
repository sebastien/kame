package operationscheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
)

func TestComputedPatternMatchAndReplacement(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "computed", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		`(match? ${ROOT}/{name:*}.${EXT} "./src/demo.c")`,
		`(subst ${ROOT}/{name:*}.${EXT} ${OUT}/{name} "./src/demo.c")`,
		`(regex-group "name" (regex-match ${ROOT}/{name:~[a-z]+}.${EXT} "./src/demo.c"))`,
		`(resub ${ROOT}/{name:~[a-z]+}.${EXT} ${OUT}/{name} "./src/demo.c")`,
		`match "./src/demo.c"
    ${ROOT}/{name:*}.${EXT} => name`,
		`(match? ./a\*/${EXT}/{name:*} "./abc/c/demo")`,
		`(match? ./a*/${EXT}/{name:*} "./a*/c/demo")`,
		`(subst ${ROOT}/{**}.${EXT} ${OUT}/{_0} "./src/demo.c")`,
	}
	for i := range texts {
		r := evaluate(t, p, `((ROOT OUT EXT => `+texts[i]+`) "./src" """../out {literal}""" "c")`)
		if r.Diagnostic.Code != "" {
			t.Errorf("computed operation failed: %s", r.Diagnostic.Code)
		}
		if i == 0 || i == 6 {
			if r.Value.Kind != core.Bool || !r.Value.Bool {
				t.Error("computed match failed")
			}
		} else if i == 5 {
			if r.Value.Kind != core.Bool || r.Value.Bool {
				t.Error("authored literal glob outside a capture became syntax")
			}
		} else {
			want := "demo"
			kind := core.String
			if i == 1 || i == 3 || i == 7 {
				want = "../out {literal}/demo"
				kind = core.Path
			}
			if r.Value.Kind != kind || r.Value.Text != want {
				t.Error("computed capture/expansion failed")
			}
		}
		r.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
