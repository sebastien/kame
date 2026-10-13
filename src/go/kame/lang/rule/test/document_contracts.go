package rule_test

import (
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestRecipeUsesDocumentScannerForInlineTrimmedMatch(t *testing.T) {
	a := t.Allocator()
	bodies := []string{
		"prefix @match(1)@case(1)yes@end(match) suffix",
		"@-match(1)-\n\t@-case(1)-\n\tyes\n\t@-end(match)-",
		"prefix @if(:true)yes@end(if) suffix",
		"Hi @if(:true)there@end!",
	}
	for _, body := range bodies {
		text := "build : dep\n\t" + body
		parsed := rule.ParseRule(a, "document.kmk", text)
		if len(parsed.Diagnostics) != 0 || parsed.Rule == nil || parsed.Rule.BodyDoc == nil {
			t.Error("inline/trimmed document recipe was not detected")
		} else {
			if parsed.Rule.BodyDoc.Root.Span.Start != len("build : dep\n") || parsed.Rule.BodyDoc.Root.Span.End != len(text) {
				t.Error("document recipe lost authored span")
			}
			formatted := rule.FormatRule(a, parsed.Rule)
			roundtrip := rule.ParseRule(a, "roundtrip.kmk", formatted)
			if len(roundtrip.Diagnostics) != 0 || roundtrip.Rule.BodyDoc == nil {
				t.Error("document recipe did not round trip")
			}
			roundtrip.Free()
			mem.FreeString(a, formatted)
		}
		parsed.Free()
	}
	plain := rule.ParseRule(a, "plain.kmk", "build : dep\n\techo \\@if(:true)\n\techo @if (:true)")
	defer plain.Free()
	if len(plain.Diagnostics) != 0 || plain.Rule.BodyDoc != nil {
		t.Error("ordinary or escaped recipe text became a document")
	}
}

func TestRecipeDocumentScannerRespectsValuesPrefixesAndRawClosing(t *testing.T) {
	a := t.Allocator()
	bodies := []string{
		"echo @(str \"\"\"@if(:true)\"\"\")",
		"echo user@end2.example @else_foo @raw/path",
		"@else_foo\n\t@raw/path\n\t@end2",
	}
	for _, body := range bodies {
		parsed := rule.ParseRule(a, "literal.kmk", "build : dep\n\t" + body)
		if len(parsed.Diagnostics) != 0 || parsed.Rule == nil || parsed.Rule.BodyDoc != nil {
			t.Error("recipe scanner promoted value contents or keyword prefixes to a document")
		}
		parsed.Free()
	}
	raw := rule.ParseRule(a, "raw.kmk", "build : dep\n\t@raw\n\tliteral @if(:true) text @end(raw)")
	defer raw.Free()
	if len(raw.Diagnostics) != 0 || raw.Rule == nil || raw.Rule.BodyDoc == nil {
		t.Error("recipe raw block missed its later same-line closing directive")
	}
}

func TestRawRecipeKeepsMalformedContentsLiteral(t *testing.T) {
	bodies := []string{"literal @(broken ", "literal @if() text "}
	for _, body := range bodies {
		parsed := rule.ParseRule(t.Allocator(), "raw.kmk", "build : dep\n\t@raw\n\t" + body + "@end(raw)")
		if len(parsed.Diagnostics) != 0 || parsed.Rule == nil || parsed.Rule.BodyDoc == nil || len(parsed.Rule.BodyDoc.Diagnostics) != 0 {
			t.Error("raw recipe document parsed malformed literal contents or missed closing")
		} else if len(parsed.Rule.BodyDoc.Root.Items) != 2 || len(parsed.Rule.BodyDoc.Root.Items[1].Parts) != 1 || parsed.Rule.BodyDoc.Root.Items[1].Parts[0].Text != "\t"+body {
			t.Error("raw recipe changed literal malformed bytes")
		}
		parsed.Free()
	}
}
