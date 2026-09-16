package expr_test

import (
	"littlemake/lang/expr"
	"littlemake/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func parse(t *testing.T, text string) *expr.Result {
	r := expr.Parse(t.Allocator(), "test.lm", text)
	if len(r.Diagnostics) != 0 {
		t.Errorf("Parse(%q) returned %d diagnostics", text, len(r.Diagnostics))
	}
	return r
}

func sameSpan(span source.Span, start int, end int) bool {
	return span.Start == start && span.End == end
}

func TestRecordAndReferenceSpans(t *testing.T) {
	r := parse(t, "(build [name: \"app\" count: 2] project.name)")
	defer r.Free()
	if r.Expr == nil || r.Expr.Kind != expr.Application || !sameSpan(r.Expr.Span, 0, len(r.Source.Text)) {
		t.Error("application did not retain its complete span")
		return
	}
	record := r.Expr.Items[1]
	if record.Kind != expr.Record || !sameSpan(record.Fields[0].Span, 8, 12) || !sameSpan(record.Fields[0].Value.Span, 14, 19) {
		t.Error("record keys and values did not retain positions")
	}
	reference := r.Expr.Items[2]
	if reference.Kind != expr.Reference || len(reference.Reference) != 2 || !sameSpan(reference.Reference[1].Span, 37, 42) {
		t.Error("reference components did not retain positions")
	}
}

func TestPipeRewritesWithOriginalSpans(t *testing.T) {
	r := parse(t, "(value | transform _ a)")
	defer r.Free()
	if r.Expr == nil || r.Expr.Kind != expr.Application || len(r.Expr.Items) != 3 || r.Expr.Items[0].Text != "transform" || r.Expr.Items[1].Text != "value" || r.Expr.Items[2].Text != "a" {
		t.Error("pipe did not normalize to an application")
		return
	}
	if !sameSpan(r.Expr.Span, 0, 23) || !sameSpan(r.Expr.Items[1].Span, 1, 6) {
		t.Error("pipe rewrite lost source spans")
	}
}

func TestStringInterpolationAndRoundTrip(t *testing.T) {
	r := parse(t, "\"hello @(name)\\n\"")
	defer r.Free()
	if r.Expr == nil || r.Expr.Kind != expr.String || len(r.Expr.Parts) != 3 || r.Expr.Parts[1].Expr == nil || !sameSpan(r.Expr.Parts[1].Span, 7, 14) {
		t.Error("string interpolation parts did not retain spans")
		return
	}
	formatted := expr.Format(t.Allocator(), r.Expr)
	copy := expr.Parse(t.Allocator(), "formatted.lm", formatted)
	if len(copy.Diagnostics) != 0 || copy.Expr == nil || copy.Expr.Kind != expr.String || len(copy.Expr.Parts) != 3 {
		t.Error("formatted string did not parse equivalently")
	}
	copy.Free()
	mem.FreeString(t.Allocator(), formatted)
}

func TestInvalidNumberReportsParseSpan(t *testing.T) {
	r := expr.Parse(t.Allocator(), "test.lm", "1_")
	defer r.Free()
	if len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "PARSE_ERR" || !sameSpan(r.Diagnostics[0].Span, 0, 2) {
		t.Error("invalid number did not report its exact parse span")
	}
}

func TestSelectorsAndBraceInterpolationRetainSpans(t *testing.T) {
	selector := parse(t, "@<1..-1")
	defer selector.Free()
	if selector.Expr == nil || selector.Expr.Kind != expr.Selector || selector.Expr.Text != "@<1..-1" || !sameSpan(selector.Expr.Span, 0, 7) {
		t.Error("selector was not parsed with its source span")
	}
	string := parse(t, "\"Bundle {(count files)}\"")
	defer string.Free()
	if string.Expr == nil || len(string.Expr.Parts) != 2 || !string.Expr.Parts[1].Brace || !sameSpan(string.Expr.Parts[1].Span, 8, 23) {
		t.Error("brace interpolation was not retained")
	}
	formatted := expr.Format(t.Allocator(), string.Expr)
	if formatted != "\"Bundle {(count files)}\"" { t.Errorf("Format() = %s", formatted) }
	mem.FreeString(t.Allocator(), formatted)
}

func TestValidSelectorRequiresCompleteSelector(t *testing.T) {
	if !expr.ValidSelector("@<1..-1") || expr.ValidSelector("@1...") {
		t.Error("ValidSelector accepted an incomplete selector")
	}
}

func TestInvalidReferenceSliceReportsParseError(t *testing.T) {
	r := expr.Parse(t.Allocator(), "test.lm", "files.1...4")
	defer r.Free()
	if len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "PARSE_ERR" { t.Error("invalid reference slice was accepted") }
}

func TestPathAndPipeChain(t *testing.T) {
	path := parse(t, "./src/main.c")
	defer path.Free()
	if path.Expr.Kind != expr.Path || path.Expr.Text != "./src/main.c" { t.Error("explicit path did not parse") }
	chain := parse(t, "(value | first | second b)")
	defer chain.Free()
	if chain.Expr.Kind != expr.Application || len(chain.Expr.Items) != 3 || chain.Expr.Items[0].Text != "second" || chain.Expr.Items[1].Text != "b" || chain.Expr.Items[2].Kind != expr.Application { t.Error("pipe chain did not normalize") }
}
