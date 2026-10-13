package program_test

import (
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

func TestCompositionSessionRejectsValueHeadsBeforePriorWork(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	cases := []program.Fragment{
		{Name: "hidden.template", Lang: "template", Inline: true, Text: "@(if :false (include ./a.km) :nil)"},
		{Name: "hidden.template", Lang: "template", Inline: true, Text: "@(let [unused ([] (include? ./a.km))] :nil)"},
		{Name: "hidden.kash", Lang: "kash", Text: "unused = ([] (include ./a.km))\n"},
		{Name: "hidden.expr", Lang: "expr", Text: "(if :false (include ./a.km) :nil)"},
	}
	for i := range cases {
		fragments := []program.Fragment{{Name: "prior.km", Lang: "km", Text: "(out! 42)\n"}, cases[i]}
		compiled := program.CompileSession(a, fragments, registry, program.Options{})
		if compiled.Session != nil {
			compiled.Session.Free()
			t.Error("session scheduled prior output despite forbidden source heads")
		}
		if len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Code != "EXPR_INVALID" || compiled.Diagnostics[0].Source != cases[i].Name {
			t.Error("session lost value-placement diagnostic or source")
		}
		compiled.Free(a)
	}
	fragments := []program.Fragment{{Name: "legit.template", Lang: "template", Inline: true, Text: "@include ./template.txt\n"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session == nil || len(compiled.Diagnostics) != 0 {
		t.Error("legitimate document @include was rejected")
		return
	}
	compiled.Session.Free()
}
