package template_test

import (
	"kame/lang/expr"
	"kame/lang/template"
	"solod.dev/so/testing"
)

func TestDocumentScannerKeepsExpressionContentsAndKeywordPrefixesLiteral(t *testing.T) {
	texts := []string{
		"echo @(str \"\"\"@if(:true)\"\"\")",
		"echo user@end2.example @else_foo @raw/path @endif",
		"@else_foo\n@raw/path\n@end2\n@endif\n",
		"@if(:true)echo @(str \"\"\"@if(:true)\"\"\")@end(if)",
	}
	for _, text := range texts {
		doc := template.ParseDocument(t.Allocator(), "scanner.tmpl", text, "plain")
		if len(doc.Diagnostics) != 0 {
			t.Error("document scanner interpreted value contents or keyword prefixes as directives")
		}
		doc.Free()
	}
	valid := template.ParseDocument(t.Allocator(), "bare.tmpl", "@if(:true)yes@else no@end", "plain")
	defer valid.Free()
	if len(valid.Diagnostics) != 0 {
		t.Error("accepted bare inline markers stopped working")
	}
}

func TestRawScannerFindsClosingAfterLiteralDirectiveOnSameLine(t *testing.T) {
	text := "@raw\nliteral @if(:true) text @end(raw)tail"
	doc := template.ParseDocument(t.Allocator(), "raw.tmpl", text, "plain")
	defer doc.Free()
	if len(doc.Diagnostics) != 0 || len(doc.Root.Items) != 3 {
		t.Fatal("raw scanner missed a later closing directive on the same line")
		return
	}
	if doc.Root.Items[1].Kind != expr.String || len(doc.Root.Items[1].Parts) != 1 || doc.Root.Items[1].Parts[0].Text != "literal @if(:true) text " || doc.Root.Items[1].Span.Start != len("@raw\n") || doc.Root.Items[1].Span.End != len("@raw\nliteral @if(:true) text ") {
		t.Error("raw text or authored span changed")
	}
}

func TestRawScannerNeverParsesMalformedLiteralContents(t *testing.T) {
	bodies := []string{"literal @(broken ", "literal @if() text ", "literal {([broken ", "literal @end2 @else_foo @raw/path "}
	for _, body := range bodies {
		text := "@raw\n" + body + "@end(raw)tail"
		doc := template.ParseDocument(t.Allocator(), "raw.tmpl", text, "plain")
		if len(doc.Diagnostics) != 0 || len(doc.Root.Items) != 3 {
			t.Error("raw scanner parsed malformed literal contents or missed closing")
		} else if doc.Root.Items[1].Kind != expr.String || len(doc.Root.Items[1].Parts) != 1 || doc.Root.Items[1].Parts[0].Text != body || doc.Root.Items[1].Span.End != len("@raw\n")+len(body) {
			t.Error("raw scanner changed literal bytes or authored spans")
		}
		doc.Free()
	}
}

func TestBareEndAllowsAdjacentProsePunctuation(t *testing.T) {
	texts := []string{"Hi @if(:true)there@end!", "@raw\nthere@end!"}
	for _, text := range texts {
		doc := template.ParseDocument(t.Allocator(), "punctuation.tmpl", text, "plain")
		if len(doc.Diagnostics) != 0 || len(doc.Root.Items) < 2 {
			t.Error("bare end rejected adjacent prose punctuation")
		} else {
			last := doc.Root.Items[len(doc.Root.Items)-1]
			if last.Kind != expr.String || len(last.Parts) != 1 || last.Parts[0].Text != "!" || last.Span.Start != len(text)-1 || last.Span.End != len(text) {
				t.Error("bare end consumed or moved adjacent punctuation")
			}
		}
		doc.Free()
	}
}

func TestBareTrimMarkerDelimitsAdjacentText(t *testing.T) {
	texts := []string{
		"@if(:false)@if(fail! \"unselected\")bad@end(if)@else-safe@end(if)",
		"@if(:true)body@end-tail",
		"@raw-body@end-tail",
		"@raw\nbody@end-tail",
	}
	for _, text := range texts {
		doc := template.ParseDocument(t.Allocator(), "trim.tmpl", text, "plain")
		if len(doc.Diagnostics) != 0 {
			t.Error("trim marker did not delimit adjacent text: " + text)
		} else if text != texts[0] {
			last := doc.Root.Items[len(doc.Root.Items)-1]
			if last.Kind != expr.Application || last.Items[0].Text != "template-trim-start" {
				t.Error("bare end trim was not lowered")
			} else {
				tail := last.Items[1].Items[1]
				if tail.Kind != expr.String || len(tail.Parts) != 1 || tail.Parts[0].Text != "tail" || tail.Span.Start != len(text)-4 || tail.Span.End != len(text) {
					t.Error("trim marker consumed or moved adjacent text")
				}
			}
		}
		doc.Free()
	}
}

func TestForParameterShapeDiagnostics(t *testing.T) {
	texts := []string{"@for(x [1 2])", "@for([1] [1 2])", "@for([[x]] [1 2])", "@for([x x] [1 2])", "@for([x... y] [1 2])"}
	messages := []string{
		"@for parameters must be a list of names; got name",
		"@for parameter list must contain only names; got int",
		"@for parameter list must contain only names; got list",
		"@for parameters must be unique names, with any rest parameter last",
		"@for parameters must be unique names, with any rest parameter last",
	}
	starts := []int{5, 6, 6, 5, 5}
	ends := []int{6, 7, 9, 10, 13}
	for i := range texts {
		doc := template.ParseDocument(t.Allocator(), "for.tmpl", texts[i], "plain")
		if len(doc.Diagnostics) != 1 {
			t.Error("invalid @for parameters must produce one shape diagnostic")
		} else if doc.Diagnostics[0].Code != "TPL_PARSE" || doc.Diagnostics[0].Message != messages[i] || doc.Diagnostics[0].Span.Start != starts[i] || doc.Diagnostics[0].Span.End != ends[i] {
			t.Error("@for shape diagnostic lost actual kind or parameter span")
		}
		doc.Free()
	}
	valid := template.ParseDocument(t.Allocator(), "for.tmpl", "@for([x] [1 2])@(x)@end(for)", "plain")
	if len(valid.Diagnostics) != 0 { t.Error("valid @for parameters were rejected") }
	valid.Free()
}
