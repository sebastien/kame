package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestStructuralLayoutRoundTrip(t *testing.T) {
	const long = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	cases := []string{
		"(let [x (f " + long + " y) z 2] (if x z :nil))",
		"(match x [\"p\" (f " + long + " x)] [:else :nil])",
		"([x rest...] [name: x data: (f " + long + " rest)])",
		"((choose " + long + " x) [x y])",
		"(f \"\"\"literal\n  text\t\nend\"\"\" x)",
	}
	for i := range cases {
		checkLayoutRoundTrip(t, cases[i])
	}
}

func checkLayoutRoundTrip(t *testing.T, text string) {
	a := t.Allocator()
	parsed := expr.Parse(a, "style", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 {
		t.Error("source did not parse")
		return
	}
	formatted := expr.FormatAt(a, parsed.Expr, 2)
	defer mem.FreeString(a, formatted)
	again := expr.Parse(a, "style", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 {
		t.Error("expanded source did not parse")
		return
	}
	canonical := expr.FormatAt(a, again.Expr, 2)
	defer mem.FreeString(a, canonical)
	if formatted != canonical {
		t.Error("expanded layout was not idempotent")
	}
}
