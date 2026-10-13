package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/math"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestUnclosedPipeFreesDiscardedTrees(t *testing.T) {
	r := expr.Parse(t.Allocator(), "pipe.km", "(x | f [y]")
	defer r.Free()
	if len(r.Diagnostics) == 0 || r.Expr == nil || r.Expr.Kind != expr.Name || r.Expr.Text != "x" {
		t.Error("unclosed pipe did not preserve its left expression and diagnostics")
	}
}

func TestNamedPatternTrailingEscapeHasAuthoredSpan(t *testing.T) {
	a := t.Allocator()
	parsed := expr.ParsePatternText(a, "{name:abc\\", 7)
	defer parsed.Pattern.Free(a)
	defer slices.Free(a, parsed.Diagnostics)
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
