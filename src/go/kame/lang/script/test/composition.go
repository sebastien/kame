package scriptcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/script"
	"kame/lang/source"
)

func TestCompositionFormsAndFormatting(t *testing.T) {
	a := t.Allocator()
	text := "# before\ninclude ./folder/a\\ b.km # required\ninclude? ../optional.km\nif false\n  include ./a.km\n\nelif true # selected\n  if true\n    # nested\n    include? ./b.km\n# after\n(if true then 42 else 0)\nwhen = true\n"
	parsed := script.Parse(a, "composition.kmk", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 7 {
		t.Fatal("composition parse failed")
		return
	}
	if parsed.Items[1].Kind != script.Include || parsed.Items[1].Include != "./folder/a b.km" || parsed.Items[2].Kind != script.Include || !parsed.Items[2].OptionalInclude || parsed.Items[3].Kind != script.Composition || parsed.Items[5].Kind != script.Expression {
		t.Error("composition classification failed")
	}
	leaf := parsed.Items[3].Branches[1].Items[0].Branches[0].Items[1]
	if leaf.Kind != script.Include || !leaf.OptionalInclude || leaf.Include != "./b.km" {
		t.Error("nested declaration lost its include path")
	}
	formatted := script.FormatWithIndent(a, parsed, "  ")
	defer mem.FreeString(a, formatted)
	again := script.Parse(a, "again.kmk", formatted)
	defer again.Free()
	second := script.FormatWithIndent(a, again, "  ")
	defer mem.FreeString(a, second)
	if len(again.Diagnostics) != 0 || formatted != second || formatted != text {
		t.Errorf("composition formatting changed: first %q, second %q", formatted, second)
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
		"x = (include ./a.km)\n", "x = (() => (include ./a.km))\n", "(str (include ./a.km))\n",
		"[(include ./a.km)]\n", "{x: (include ./a.km)}\n", "\"@{include ./a.km}\"\n",
		"(if (include ./a.km) then nil else nil)\n", "(if false then (include variable) else nil)\n",
		"(if true then (include ./a.km) else 42)\n", "(if true then (include ./a.km) else (if false then 42 else nil))\n",
		"(include ())\n", "(include ./a.km ./b.km)\n", "(include \"\")\n", "(include ./a*.km)\n",
		"(include \"@{path ()}\")\n", "(include ./@{path ()})\n", "(include \"a\x00b\")\n",
		"build : @{include ./a.km}\n", "build :\n\techo @{include ./a.km}\n",
		"include\n", "include relative.km\n", "include \"./quoted.km\"\n", "include ./a.km ./b.km\n",
		"include ./[ab].km\n", "include ./x{a,b}.km\n", "include ./@{name ()}\n", "include ./a\x00b\n",
		"if false\n  include relative.km\nelse\n  include ./valid.km\n", "when true\nend\n",
	}
	for i := range cases {
		parsed := script.Parse(a, "bad.kmk", cases[i])
		if len(parsed.Diagnostics) == 0 && (len(parsed.Items) != 1 || parsed.Items[0].Rule == nil || len(parsed.Items[0].Rule.RecipeDiagnostics) == 0) {
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
