package exprcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/expr"
)

func TestStructuralLayoutRoundTrip(t *testing.T) {
	const long = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	cases := []string{
		"((x z => if x then z else nil) (f " + long + " y) 2)",
		"(match x\n    \"p\" => (f " + long + " x)\n    else => nil)",
		"(x rest... => {name: x, data: (f " + long + " rest)})",
		"((choose " + long + " x) [x, y])",
		"(f \"\"\"literal\n  text\t\nend\"\"\" x)",
		"(" + long + " |> map navigation-item |> filter (item => item.visible) |> join \"\\n\")",
		"(value |> transform (nested " + long + " data) _ |> finish)",
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
		t.Errorf("source %q did not parse: %v", text, parsed.Diagnostics)
		return
	}
	formatted := expr.FormatAt(a, parsed.Expr, 2)
	defer mem.FreeString(a, formatted)
	again := expr.Parse(a, "style", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 {
		t.Errorf("expanded source %q did not parse: %v", formatted, again.Diagnostics)
		return
	}
	canonical := expr.FormatAt(a, again.Expr, 2)
	defer mem.FreeString(a, canonical)
	if formatted != canonical {
		t.Error("expanded layout was not idempotent")
	}
}
