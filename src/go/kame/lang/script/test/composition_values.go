package scriptcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
)

func TestCompositionValueSurfacesRejectHiddenHeads(t *testing.T) {
	a := t.Allocator()
	cases := []string{"(include? ./a.km)", "(if :false (include ./a.km) :nil)", "unused = ([] (include ./a.km))", "if (if :true :false :true)\n\tunused = ([] (include? ./a.km))\n", "def library\n\t(include ./a.km)\n"}
	for i := range cases {
		parsed := script.ParseKash(a, "hidden.kash", cases[i])
		if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "EXPR_INVALID" {
			t.Errorf("Kash accepted hidden source head: %s", cases[i])
		}
		for j := range parsed.Items {
			if parsed.Items[j].Kind == script.Include || parsed.Items[j].Kind == script.Composition {
				t.Error("Kash classified source composition")
			}
		}
		parsed.Free()
		original := source.New(a, "fragment.kash", cases[i])
		fragment := script.ParseFragment(a, original, "kash", 0, len(cases[i]))
		if len(fragment.Diagnostics) == 0 || fragment.Diagnostics[0].Code != "EXPR_INVALID" {
			t.Error("Kash fragment skipped value validation")
		}
		fragment.Free()
		original.Free(a)
	}
	original := source.New(a, "fragment.expr", "(if :false (include ./a.km) :nil)")
	defer original.Free(a)
	fragment := script.ParseFragment(a, original, "expr", 0, len(original.Text))
	defer fragment.Free()
	if len(fragment.Diagnostics) == 0 || fragment.Diagnostics[0].Code != "EXPR_INVALID" || fragment.Items[0].Kind != script.Expression {
		t.Error("expression fragment allowed source composition")
	}
	root := expr.Parse(a, "value", "([] (if :false (include ./a.km) :nil))")
	defer root.Free()
	diagnostics := script.ValidateValueExpression(a, root.Expr)
	defer mem.FreeSlice(nil, diagnostics)
	if len(diagnostics) != 1 || diagnostics[0].Code != "EXPR_INVALID" {
		t.Error("shared validation missed an unused lambda")
	}
}

func TestKashLibraryCompositionAndFormatting(t *testing.T) {
	a := t.Allocator()
	parsed := script.ParseKash(a, "library.kash", "# library\n(include ./nested.km)\nvalue = 1\ndef check\n\tprintf %s $value\n")
	defer parsed.Free()
	script.ValidateKashLibrary(parsed)
	if len(parsed.Diagnostics) != 0 || parsed.Items[1].Kind != script.Include {
		t.Fatal("literal Kash include or definition-only library rejected")
		return
	}
	formatted := script.Format(a, parsed)
	defer mem.FreeString(a, formatted)
	again := script.ParseKash(a, "formatted.kash", formatted)
	defer again.Free()
	script.ValidateKashLibrary(again)
	if len(again.Diagnostics) != 0 || again.Items[1].Kind != script.Include || again.Items[1].Include != "./nested.km" {
		t.Error("Kash formatting changed library composition")
	}
	texts := []string{"printf side-effect", "(out! 1)", "if true\n\tprintf work\n", "let value = 1"}
	for i := range texts {
		text := texts[i]
		invalid := script.ParseKash(a, "invalid.kash", text)
		script.ValidateKashLibrary(invalid)
		if len(invalid.Diagnostics) == 0 || invalid.Diagnostics[0].Code != "PARSE_ERR" {
			t.Error("library accepted top-level work: " + text)
		}
		invalid.Free()
	}
}
