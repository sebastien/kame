package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestComputedPatternAuthoredPartsAndClone(t *testing.T) {
	a := t.Allocator()
	text := `@(ROOT)/{one:*}.@(EXT)/{two:~[a-z]+}`
	r := expr.Parse(a, "computed", text)
	if len(r.Diagnostics) != 0 || r.Expr.Pattern == nil { t.Error("computed pattern did not classify"); r.Free(); return }
	p := r.Expr.Pattern
	if p.Matchers != 2 || p.References != 0 || p.Parts[0].Kind != expr.PatternInterpolation || p.Parts[0].Index != 0 || p.Parts[2].Index != 0 || p.Parts[len(p.Parts)-1].Index != 1 {
		t.Error("aggregate capture/interpolation indices changed")
	}
	if p.Parts[0].Span.Start != 0 || p.Parts[0].Span.End != 7 || p.Parts[2].Span.Start != 8 || p.Parts[2].Span.End != 15 { t.Error("computed pattern lost authored byte spans") }
	cloned := expr.Clone(a, r.Expr)
	r.Free()
	formatted := expr.Format(a, cloned)
	if formatted != text { t.Error("cloned computed pattern lost formatting/source lifetime") }
	again := expr.Parse(a, "again", formatted)
	if len(again.Diagnostics) != 0 || again.Expr.Pattern == nil || again.Expr.Pattern.Matchers != 2 { t.Error("formatted computed pattern did not reparse") }
	again.Free()
	mem.FreeString(a, formatted)
	expr.Free(a, cloned)
}

func TestComputedPatternClassificationAndGroupBoundaries(t *testing.T) {
	a := t.Allocator()
	texts := []string{`@(ROOT)/{name}`, `@(ROOT)/{bad:}`, `@(ROOT)/\{name}`, `"@(ROOT)/{name:*}"`, `@(ROOT)/{a:*}/{b}`, `./{@(NAME):*}`, `./{name:@(GLOB)}`, `./{name:~@(REGEX)}`, `@(ROOT)/{name:~(a|)}`}
	for i := range texts {
		r := expr.Parse(a, "computed", texts[i])
		if i == 0 {
			if len(r.Diagnostics) != 0 || r.Expr.Pattern == nil || r.Expr.Pattern.References != 1 { t.Error("computed expansion did not classify") }
		} else if i < 4 {
			if len(r.Diagnostics) != 0 || r.Expr.Pattern != nil { t.Error("literal fallback/string changed classification") }
		} else if len(r.Diagnostics) == 0 || r.Diagnostics[0].Code != "PARSE_ERR" { t.Error("invalid authored group accepted") }
		if i == 5 && (r.Diagnostics[0].Span.Start != 3 || r.Diagnostics[0].Span.End != 5) { t.Error("group interpolation diagnostic lost source span") }
		if i == 8 && (r.Diagnostics[0].Span.Start != 17 || r.Diagnostics[0].Span.End != 18) { t.Error("computed regex diagnostic lost source span") }
		r.Free()
	}
	// A regex-looking value expression must not change malformed authored-group
	// fallback into a source regex diagnostic.
	r := expr.Parse(a, "computed", `@(nop """./{name:~a}""")/{bad:}`)
	if len(r.Diagnostics) != 0 || r.Expr.Pattern != nil { t.Error("interpolation source influenced authored regex classification") }
	r.Free()
}
