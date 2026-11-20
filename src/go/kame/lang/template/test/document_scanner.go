package templatecheck

import (
	"kame/internal/testing"
	"kame/lang/expr"
	"kame/lang/template"
)

func TestDocumentScannerKeepsExpressionContentsAndKeywordPrefixesLiteral(t *testing.T) {
	texts := []string{
		`echo @{str "@if true" "@end if"}`,
		"echo user@end2.example @else_foo @raw/path @endif",
		"@else_foo\n@raw/path\n@end2\n@endif\n",
		"prefix @if true text @end if",
	}
	for _, text := range texts {
		doc := template.ParseDocument(t.Allocator(), "scanner.kmt", text, "plain")
		if len(doc.Diagnostics) != 0 {
			t.Error("document scanner interpreted value contents or keyword prefixes as directives")
		}
		doc.Free()
	}
}

func TestRawScannerFindsClosingAfterLiteralDirectiveOnSameLine(t *testing.T) {
	body := "literal @if true text "
	open := "<p><!-- @raw -->"
	doc := template.ParseDocument(t.Allocator(), "raw.html", open+body+"<!-- @end raw -->tail", "html")
	defer doc.Free()
	if len(doc.Diagnostics) != 0 || len(doc.Root.Items) != 4 {
		t.Fatal("raw scanner missed a wrapped closing directive on the same line")
		return
	}
	literal := doc.Root.Items[2]
	if literal.Kind != expr.String || len(literal.Parts) != 1 || literal.Parts[0].Text != body || !sameSpan(literal.Span, len(open), len(open+body)) {
		t.Error("raw text or authored span changed")
	}
}

func TestRawScannerNeverParsesMalformedLiteralContents(t *testing.T) {
	bodies := []string{"literal @{broken ", "literal @if ) text ", "literal {([broken ", "literal @end2 @else_foo @raw/path "}
	for _, body := range bodies {
		text := "@raw\n" + body + "\n@end raw\ntail"
		doc := template.ParseDocument(t.Allocator(), "raw.kmt", text, "plain")
		if len(doc.Diagnostics) != 0 || len(doc.Root.Items) != 3 {
			t.Error("raw scanner parsed malformed literal contents or missed closing")
		} else if doc.Root.Items[1].Kind != expr.String || len(doc.Root.Items[1].Parts) != 1 || doc.Root.Items[1].Parts[0].Text != body+"\n" || doc.Root.Items[1].Span.End != len("@raw\n")+len(body)+1 {
			t.Error("raw scanner changed literal bytes or authored spans")
		}
		doc.Free()
	}
}

func TestBareEndAllowsAdjacentProsePunctuation(t *testing.T) {
	text := "Hi <!-- @if true -->there<!-- @end -->!"
	doc := template.ParseDocument(t.Allocator(), "punctuation.html", text, "html")
	defer doc.Free()
	if len(doc.Diagnostics) != 0 || len(doc.Root.Items) < 2 {
		t.Error("wrapped bare end rejected adjacent prose punctuation")
	} else {
		last := doc.Root.Items[len(doc.Root.Items)-1]
		if last.Kind != expr.String || len(last.Parts) != 1 || last.Parts[0].Text != "!" || !sameSpan(last.Span, len(text)-1, len(text)) {
			t.Error("wrapped bare end consumed or moved adjacent punctuation")
		}
	}
}

func TestBareTrimMarkerDelimitsAdjacentText(t *testing.T) {
	for _, text := range []string{"@else-", "@raw-", "@end raw-"} {
		doc := template.ParseDocument(t.Allocator(), "trim.kmt", text, "plain")
		if len(doc.Diagnostics) == 0 || doc.Diagnostics[0].Code != "TPL_PARSE" {
			t.Error("accepted a glued trim marker: " + text)
		}
		doc.Free()
	}
}

func TestForParameterShapeDiagnostics(t *testing.T) {
	texts := []string{"@for x [1, 2]", "@for 1 in [1, 2]", "@for [x] in [1, 2]", "@for x x in [1, 2]", "@for x... y in [1, 2]"}
	for _, text := range texts {
		doc := template.ParseDocument(t.Allocator(), "for.kmt", text, "plain")
		if len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Code != "TPL_PARSE" || !sameSpan(doc.Diagnostics[0].Span, 0, len(text)) {
			t.Errorf("%s: unexpected @for diagnostics: %+v", text, doc.Diagnostics)
		}
		doc.Free()
	}
	valid := template.ParseDocument(t.Allocator(), "for.kmt", "@for x rest... in [[1, 2]] sep str \",\" \" \"\n@{x}\n@end for", "plain")
	if len(valid.Diagnostics) != 0 {
		t.Error("valid @for parameters were rejected")
	}
	valid.Free()
}
