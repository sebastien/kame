package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestMixedWordExpressionsKeepSpansAndTypedBoundaries(t *testing.T) {
	a := t.Allocator()
	text := `$(printf --config=(name).json (name) "literal(call)" (name)"")`
	parsed := expr.Parse(a, "words.km", text)
	if len(parsed.Diagnostics) != 0 { t.Error("mixed words did not parse"); parsed.Free(); return }
	words := parsed.Expr.Items[0].Items
	if len(words) != 5 { t.Error("mixed expressions split argv"); parsed.Free(); return }
	mixed, standalone, literal, scalar := words[1], words[2], words[3], words[4]
	if mixed.Bool || len(mixed.Parts) != 3 || mixed.Parts[0].Text != "--config=" || mixed.Parts[2].Text != ".json" {
		t.Error("mixed word lost its literal fragments")
	} else {
		part := mixed.Parts[1]
		if part.Form != "(" || part.Expr.Kind != expr.Application || text[part.Span.Start:part.Span.End] != "(name)" || text[part.Expr.Span.Start:part.Expr.Span.End] != "(name)" { t.Error("expression lost its parser boundary or authored span") }
	}
	if !standalone.Bool || literal.Bool || len(literal.Parts) != 1 || literal.Parts[0].Expr != nil || literal.Parts[0].Text != "literal(call)" || scalar.Bool { t.Error("quoting changed typed splicing or literal parentheses") }
	cloned := expr.Clone(a, parsed.Expr)
	parsed.Free()
	formatted := expr.Compact(a, cloned)
	expr.Free(a, cloned)
	again := expr.Parse(a, "formatted.km", formatted)
	if len(again.Diagnostics) != 0 || again.Expr.Items[0].Items[4].Bool { t.Error("formatting enabled typed splicing on a scalar word") }
	again.Free()
	mem.FreeString(a, formatted)
}
