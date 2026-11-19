package exprcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/expr"
	"math"
)

func TestUnclosedPipePreservesPartialAST(t *testing.T) {
	for _, input := range []string{"(x |> f [y]", "(tool \"git\" |> first"} {
		r := expr.Parse(t.Allocator(), "pipe.km", input)
		if len(r.Diagnostics) == 0 || expr.Ungroup(r.Expr).Kind != expr.ValuePipe {
			t.Error("unclosed pipe did not preserve its parsed stages and diagnostics")
		}
		r.Free()
	}
}

func TestNamedPatternTrailingEscapeHasAuthoredSpan(t *testing.T) {
	a := t.Allocator()
	parsed := expr.ParsePatternText(a, "{name:abc\\", 7)
	defer parsed.Pattern.Free(a)
	defer mem.FreeSlice(nil, parsed.Diagnostics)
	if len(parsed.Diagnostics) != 1 || parsed.Diagnostics[0].Message != "unfinished pattern escape" || !sameSpan(parsed.Diagnostics[0].Span, 16, 17) {
		t.Error("trailing capture escape did not return a bounded diagnostic")
	}
}

func TestFloatFormattingPreservesKindAndBits(t *testing.T) {
	a := t.Allocator()
	inputs := []string{"1.0", "1e0", "-2.0", "0.0", "-0.0", "1.25", "1e30", "1e-30"}
	for i := range inputs {
		r := expr.Parse(a, "float.km", inputs[i])
		if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Float {
			r.Free()
			t.Fatal("float fixture did not parse")
			return
		}
		text := expr.Format(a, r.Expr)
		if text != inputs[i] {
			t.Errorf("formatting changed numeric spelling %q to %q", inputs[i], text)
		}
		again := expr.Parse(a, "formatted.km", text)
		if len(again.Diagnostics) != 0 || again.Expr == nil || again.Expr.Kind != expr.Float {
			t.Errorf("formatted float %q lost numeric kind", text)
		} else if math.Float64bits(r.Expr.Float) != math.Float64bits(again.Expr.Float) {
			t.Errorf("formatted float %q changed its value bits", text)
		}
		again.Free()
		mem.FreeString(a, text)
		r.Free()
	}
}
