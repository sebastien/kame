package programcheck

import (
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
)

func TestCompositionSessionRejectsValueHeadsBeforePriorWork(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	cases := []program.Fragment{
		{Name: "hidden.template", Lang: "template", Inline: true, Text: "@{if false then (include ./a.km) else nil}"},
		{Name: "hidden.template", Lang: "template", Inline: true, Text: "@{(unused => nil) (() => (include? ./a.km))}"},
		{Name: "hidden.kash", Lang: "kash", Text: "unused = (() => (include ./a.km))\n"},
		{Name: "hidden.expr", Lang: "expr", Text: "(if false then (include ./a.km) else nil)"},
	}
	for i := range cases {
		fragments := []program.Fragment{{Name: "prior.km", Lang: "km", Inline: true, Text: "(out! 42)\n"}, cases[i]}
		compiled := program.CompileSession(a, fragments, registry, program.Options{})
		if compiled.Session != nil {
			compiled.Session.Free()
			t.Error("session scheduled prior output despite forbidden source heads")
		}
		if len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Code != "PARSE_ERR" || compiled.Diagnostics[0].Source != cases[i].Name {
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
