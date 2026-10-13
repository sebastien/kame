package script_test

import (
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestCompositionFormsAndFormatting(t *testing.T) {
	a := t.Allocator()
	text := "# before\n(include \"folder/a b.km\")\n(include? ../optional.km)\n(if :false (include ./a.km) :true (if :true (include? ./b.km) :nil) :nil)\n# after\n(if :true 42 0)\nwhen = :true\n"
	parsed := script.Parse(a, "composition.kmk", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 7 {
		t.Fatal("composition parse failed")
		return
	}
	if parsed.Items[1].Kind != script.Include || parsed.Items[1].Include != "folder/a b.km" || parsed.Items[1].Expression.Kind != expr.Application || parsed.Items[2].Kind != script.Include || !parsed.Items[2].OptionalInclude || parsed.Items[3].Kind != script.Composition || parsed.Items[5].Kind != script.Expression {
		t.Error("composition classification failed")
	}
	leaf := script.CompositionInclude(parsed.Items[1].Expression)
	if !leaf.OK || leaf.Optional || leaf.Path != "folder/a b.km" || leaf.Span.Start != parsed.Items[1].Span.Start {
		t.Error("borrowed include form lost decoded path or span")
	}
	formatted := script.Format(a, parsed)
	defer mem.FreeString(a, formatted)
	again := script.Parse(a, "again.kmk", formatted)
	defer again.Free()
	second := script.Format(a, again)
	defer mem.FreeString(a, second)
	if len(again.Diagnostics) != 0 || formatted != second {
		t.Error("composition formatting is not idempotent")
	}
	original := source.New(a, "fragment.km", text)
	defer original.Free(a)
	fragment := script.ParseFragment(a, original, "km", 0, len(text))
	defer fragment.Free()
	if len(fragment.Diagnostics) != 0 || fragment.Items[3].Kind != script.Composition {
		t.Error("value fragment did not classify composition")
	}
}

func TestCompositionValidatesInactiveAndForbiddenPlacements(t *testing.T) {
	a := t.Allocator()
	cases := []string{
		"x = (include ./a.km)\n", "x = ([] (include ./a.km))\n", "(str (include ./a.km))\n",
		"[(include ./a.km)]\n", "[x: (include ./a.km)]\n", "\"@(include ./a.km)\"\n",
		"(if (include ./a.km) :nil)\n", "(if :false (include variable) :nil)\n",
		"(if :true (include ./a.km) 42)\n", "(if :true (include ./a.km) (if :false 42 :nil))\n",
		"(include)\n", "(include ./a.km ./b.km)\n", "(include \"\")\n", "(include ./a*.km)\n",
		"(include \"@(path)\")\n", "(include ./@(path))\n", "(include \"a\x00b\")\n",
		"build : @(include ./a.km)\n", "build :\n\techo @(include ./a.km)\n",
		"include ./a.km\n", "include? ./a.km\n", "when :true\nend\n",
	}
	for i := range cases {
		parsed := script.Parse(a, "bad.kmk", cases[i])
		if len(parsed.Diagnostics) == 0 {
			t.Errorf("invalid composition accepted: %s", cases[i])
		}
		parsed.Free()
	}
	valid := script.Parse(a, "recipe.kmk", "build :\n\techo '(include ./a.km)'\n\t@include ./template.txt\n")
	defer valid.Free()
	if len(valid.Diagnostics) != 0 {
		t.Error("raw recipe or document include was treated as source composition")
	}
}
