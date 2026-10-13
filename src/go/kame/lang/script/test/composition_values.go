package script_test

import (
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestCompositionValueSurfacesRejectHiddenHeads(t *testing.T) {
	a := t.Allocator()
	cases := []string{"(include ./a.km)", "(if :false (include? ./a.km) :nil)", "unused = ([] (include ./a.km))", "if (if :true :false :true)\n\tunused = ([] (include? ./a.km))\n"}
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
	defer slices.Free(a, diagnostics)
	if len(diagnostics) != 1 || diagnostics[0].Code != "EXPR_INVALID" {
		t.Error("shared validation missed an unused lambda")
	}
}
