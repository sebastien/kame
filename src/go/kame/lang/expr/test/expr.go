package expr_test

import (
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func parse(t *testing.T, text string) *expr.Result {
	r := expr.Parse(t.Allocator(), "test.km", text)
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
	copy := expr.Parse(t.Allocator(), "formatted.km", formatted)
	if len(copy.Diagnostics) != 0 || copy.Expr == nil || copy.Expr.Kind != expr.String || len(copy.Expr.Parts) != 3 {
		t.Error("formatted string did not parse equivalently")
	}
	copy.Free()
	mem.FreeString(t.Allocator(), formatted)
}

func TestInvalidNumberReportsParseSpan(t *testing.T) {
	r := expr.Parse(t.Allocator(), "test.km", "1_")
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
	r := expr.Parse(t.Allocator(), "test.km", "files.1...4")
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

func TestPlaceholderSectionParsesAndFormats(t *testing.T) {
	r := parse(t, "((f _ __ ___))")
	defer r.Free()
	if r.Expr == nil || r.Expr.Kind != expr.Section || len(r.Expr.Parameters) != 3 || len(r.Expr.Body) != 1 { t.Error("placeholder section did not parse"); return }
	body := r.Expr.Body[0]
	if body.Kind != expr.Application || len(body.Items) != 4 || body.Items[1].Kind != expr.Placeholder || body.Items[1].Int != 0 || body.Items[3].Int != 2 { t.Error("section placeholders did not classify") }
	formatted := expr.Format(t.Allocator(), r.Expr)
	if formatted != "((f _0 _1 _2))" { t.Errorf("Format() = %s", formatted) }
	copy := parse(t, formatted)
	if copy.Expr == nil || copy.Expr.Kind != expr.Section || len(copy.Expr.Parameters) != 3 { t.Error("formatted section did not re-parse") }
	copy.Free()
	mem.FreeString(t.Allocator(), formatted)
}

func TestPlaceholderRecognitionStaysContextual(t *testing.T) {
	section := parse(t, "((f _1))")
	defer section.Free()
	if section.Expr == nil || section.Expr.Kind != expr.Section || len(section.Expr.Parameters) != 2 { t.Error("digit placeholder did not classify"); return }
	if section.Expr.Body[0].Items[1].Int != 1 { t.Error("placeholder index was wrong") }
	repeat := parse(t, "((cons _ _))")
	defer repeat.Free()
	if repeat.Expr == nil || repeat.Expr.Kind != expr.Section || len(repeat.Expr.Parameters) != 1 { t.Error("repeated placeholder did not reuse one argument") }
	names := parse(t, "(f _)")
	defer names.Free()
	if names.Expr == nil || names.Expr.Kind != expr.Application || len(names.Expr.Items) != 2 || names.Expr.Items[1].Kind != expr.Name { t.Error("placeholder outside a section stayed a name") }
	longer := parse(t, "(_0x)")
	defer longer.Free()
	if longer.Expr == nil || longer.Expr.Kind != expr.Application || longer.Expr.Items[0].Kind != expr.Name { t.Error("underscore-prefixed name became a placeholder") }
	call := parse(t, "((f))")
	defer call.Free()
	if call.Expr == nil || call.Expr.Kind != expr.Application { t.Error("placeholder-free nested call became a section") }
}

func TestPatternPathsClassify(t *testing.T) {
	r := parse(t, "./{**}/{*}.c")
	defer r.Free()
	if r.Expr == nil || r.Expr.Kind != expr.Path || r.Expr.Pattern == nil { t.Error("match pattern path did not classify"); return }
	if r.Expr.Pattern.Matchers != 2 || r.Expr.Pattern.References != 0 || r.Expr.Text != "./{**}/{*}.c" { t.Error("match pattern groups were wrong") }
	expand := parse(t, "./build/{_0}/{_1}.c")
	defer expand.Free()
	if expand.Expr == nil || expand.Expr.Pattern == nil || expand.Expr.Pattern.References != 2 || expand.Expr.Pattern.Matchers != 0 { t.Error("expansion pattern path did not classify") }
	named := parse(t, "./build/{name}.o")
	defer named.Free()
	if named.Expr == nil || named.Expr.Pattern == nil || named.Expr.Pattern.References != 1 { t.Error("named reference did not classify") }
	plain := parse(t, "./src/main.c")
	defer plain.Free()
	if plain.Expr == nil || plain.Expr.Pattern != nil { t.Error("plain path became a pattern") }
	escaped := parse(t, "./a\\{b}.c")
	defer escaped.Free()
	if escaped.Expr == nil || escaped.Expr.Pattern != nil || escaped.Expr.Text != "./a\\{b}.c" { t.Error("escaped brace path became a pattern") }
}

func TestPatternStringsClassifyAndFormat(t *testing.T) {
	r := parse(t, "\"./{**}/{*}.c\"")
	defer r.Free()
	if r.Expr == nil || r.Expr.Kind != expr.String || r.Expr.Pattern == nil || r.Expr.Pattern.Matchers != 2 { t.Error("pattern string did not classify"); return }
	if len(r.Expr.Parts) != 0 { t.Error("pattern string kept string parts") }
	formatted := expr.Format(t.Allocator(), r.Expr)
	if formatted != "\"./{**}/{*}.c\"" { t.Errorf("Format() = %s", formatted) }
	mem.FreeString(t.Allocator(), formatted)
	escaped := parse(t, "\"line\\n{*}\"")
	defer escaped.Free()
	if escaped.Expr == nil || escaped.Expr.Pattern == nil || escaped.Expr.Text != "line\n{*}" { t.Error("quoted-string escapes were not decoded before pattern parsing") }
	formatted = expr.Format(t.Allocator(), escaped.Expr)
	if formatted != "\"line\\n{*}\"" { t.Errorf("escaped pattern Format() = %s", formatted) }
	mem.FreeString(t.Allocator(), formatted)
	interp := parse(t, "\"{(count files)}/*.c\"")
	defer interp.Free()
	if interp.Expr == nil || interp.Expr.Pattern != nil { t.Error("interpolated string became a pattern") }
}

func TestMixedPatternGroupsAreRejected(t *testing.T) {
	r := expr.Parse(t.Allocator(), "test.km", "./{**}/{name}.c")
	defer r.Free()
	if len(r.Diagnostics) == 0 || r.Diagnostics[0].Code != "PARSE_ERR" { t.Error("mixed matcher and reference groups were accepted") }
	plain := parse(t, "./{*.c}")
	defer plain.Free()
	if plain.Expr == nil || plain.Expr.Pattern != nil || plain.Expr.Text != "./{*.c}" { t.Error("structurally invalid matcher did not stay a plain path") }
	literal := parse(t, "\"literal \\{ brace\"")
	defer literal.Free()
	if literal.Expr == nil || literal.Expr.Pattern != nil { t.Error("escaped-brace string became a pattern") }
}

func TestComparisonOperatorsParseAsNameAtoms(t *testing.T) {
	checkOp(t, "=")
	checkOp(t, "==")
	checkOp(t, "!=")
	checkOp(t, "<")
	checkOp(t, ">")
	checkOp(t, "<=")
	checkOp(t, ">=")
	bad := expr.Parse(t.Allocator(), "test.km", "===")
	defer bad.Free()
	if len(bad.Diagnostics) == 0 { t.Error("malformed operator run was accepted") }
}

func checkOp(t *testing.T, text string) {
	r := parse(t, text)
	if r.Expr == nil || r.Expr.Kind != expr.Name || r.Expr.Text != text {
		t.Errorf("operator %q did not parse as a name atom", text)
		r.Free()
		return
	}
	r.Free()
	app := parse(t, "("+text+" 1 2)")
	if app.Expr == nil || app.Expr.Kind != expr.Application || len(app.Expr.Items) != 3 || app.Expr.Items[0].Text != text {
		t.Errorf("operator %q did not parse as an application head", text)
		app.Free()
		return
	}
	formatted := expr.Format(t.Allocator(), app.Expr)
	expected := "(" + text + " 1 2)"
	if formatted != expected {
		t.Errorf("Format() = %q, want %q", formatted, expected)
	}
	mem.FreeString(t.Allocator(), formatted)
	app.Free()
}

func TestVerbatimLiteralIsRawAndRoundTrips(t *testing.T) {
	r := parse(t, "\"\"\"hello @(x) world\"\"\"")
	defer r.Free()
	if r.Expr == nil || r.Expr.Kind != expr.String || !r.Expr.Verbatim || r.Expr.VerbatimLen != 3 || len(r.Expr.Parts) != 1 || r.Expr.Parts[0].Expr != nil || r.Expr.Parts[0].Text != "hello @(x) world" {
		t.Error("verbatim literal did not stay raw")
		return
	}
	formatted := expr.Format(t.Allocator(), r.Expr)
	if formatted != "\"\"\"hello @(x) world\"\"\"" { t.Errorf("Format() = %q", formatted) }
	copy := parse(t, formatted)
	if copy.Expr == nil || !copy.Expr.Verbatim || len(copy.Expr.Parts) != 1 || copy.Expr.Parts[0].Text != "hello @(x) world" { t.Error("formatted verbatim did not re-parse") }
	copy.Free()
	mem.FreeString(t.Allocator(), formatted)
	long := parse(t, "\"\"\"\"has \"\"\" inside\"\"\"\"")
	defer long.Free()
	if long.Expr == nil || !long.Expr.Verbatim || long.Expr.VerbatimLen != 4 || long.Expr.Parts[0].Text != "has \"\"\" inside" { t.Error("longer verbatim delimiter did not round-trip") }
	unclosed := expr.Parse(t.Allocator(), "test.km", "\"\"\"open")
	defer unclosed.Free()
	if len(unclosed.Diagnostics) == 0 || unclosed.Diagnostics[0].Code != "TPL_PARSE" { t.Error("unclosed verbatim literal was not TPL_PARSE") }
}
