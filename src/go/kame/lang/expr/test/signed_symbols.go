package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestSignedSymbolsRetainTextSpansAndFormatting(t *testing.T) {
	a := t.Allocator()
	texts := []string{":+files", ":-symlinks", ":+dir", ":-other-kind", ":+_name2", ":-ready?", ":+action!", ":+true", ":-false", ":+nil"}
	for i := range texts {
		parsed := expr.Parse(a, "signed.km", texts[i])
		if len(parsed.Diagnostics) != 0 || parsed.Expr == nil {
			t.Error("signed symbol did not parse: " + texts[i])
			parsed.Free()
			continue
		}
		if parsed.Expr.Kind != expr.Symbol || parsed.Expr.Text != texts[i][1:] || !sameSpan(parsed.Expr.Span, 0, len(texts[i])) {
			t.Error("signed symbol lost its kind, text or span")
		}
		cloned := expr.Clone(a, parsed.Expr)
		formatted := expr.Format(a, cloned)
		if formatted != texts[i] { t.Error("signed symbol formatting changed its spelling") }
		again := expr.Parse(a, "formatted.km", formatted)
		if len(again.Diagnostics) != 0 || again.Expr == nil || again.Expr.Kind != expr.Symbol || again.Expr.Text != texts[i][1:] {
			t.Error("formatted signed symbol did not round-trip")
		}
		again.Free()
		mem.FreeString(a, formatted)
		expr.Free(a, cloned)
		parsed.Free()
	}
}

func TestSignedSymbolsRequireOrdinaryNames(t *testing.T) {
	texts := []string{":+", ":-", ":+1", ":-1", ":++files", ":--files", ":+ files", ":-dir/child", ":+dir.name"}
	for i := range texts {
		parsed := expr.Parse(t.Allocator(), "invalid.km", texts[i])
		if len(parsed.Diagnostics) == 0 { t.Error("malformed signed symbol was accepted: " + texts[i]) }
		parsed.Free()
	}
}
