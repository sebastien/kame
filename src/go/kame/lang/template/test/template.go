package template_test

import (
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func sameSpan(span source.Span, start int, end int) bool { return span.Start == start && span.End == end }

func TestStringTemplateRetainsExpansionSpans(t *testing.T) {
	parsed := template.ParseString(t.Allocator(), "test.km", "before @(project.name) @< after")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Parts) != 5 {
		t.Error("template did not parse literal, expression, and selector parts")
		return
	}
	if parsed.Parts[1].Kind != template.Expression || !sameSpan(parsed.Parts[1].Span, 7, 22) || parsed.Parts[3].Kind != template.Selector || !sameSpan(parsed.Parts[3].Span, 23, 25) {
		t.Error("template expansion spans were not retained")
	}
}

func TestCommandToolReferenceIsGlobalTemplatePart(t *testing.T) {
	parsed := template.ParseString(t.Allocator(), "test.km", "@(x/gcc) -c @<")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Parts) != 3 || parsed.Parts[0].Kind != template.Tool || parsed.Parts[0].Text != "gcc" || !sameSpan(parsed.Parts[0].Span, 0, 8) {
		t.Error("command reference did not parse as a tool part")
		return
	}
	formatted := template.FormatString(t.Allocator(), parsed)
	if formatted != "@(x/gcc) -c @<" { t.Errorf("formatted command reference = %q", formatted) }
	mem.FreeString(t.Allocator(), formatted)
}

func TestMalformedExpansionStaysLiteralWithWarning(t *testing.T) {
	parsed := template.ParseString(t.Allocator(), "test.km", "echo @(name")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 1 || parsed.Diagnostics[0].Severity != source.Warning || len(parsed.Parts) != 1 || parsed.Parts[0].Text != "echo @(name" {
		t.Error("malformed expansion was not retained as literal text with a warning")
	}
}

func TestClosedInvalidExpansionIsAnError(t *testing.T) {
	parsed := template.ParseString(t.Allocator(), "test.km", "@(1_)")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 1 || parsed.Diagnostics[0].Severity != source.Error || parsed.Parts[0].Text != "@(1_)" {
		t.Error("closed invalid expansion did not retain the parser error")
	}
}

func TestMalformedSelectorStaysLiteral(t *testing.T) {
	parsed := template.ParseString(t.Allocator(), "test.km", "@1...")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Parts) != 1 || parsed.Parts[0].Kind != template.Literal || parsed.Parts[0].Text != "@1..." {
		t.Error("malformed selector was tokenized as a selector")
	}
}

func TestTargetMatchesLeftmostShortestCapture(t *testing.T) {
	target := template.ParseTarget(t.Allocator(), "test.km", "./{stem:*}.o")
	defer target.Free()
	if len(target.Diagnostics) != 0 || !sameSpan(target.Parts[1].Span, 2, 10) {
		t.Error("target capture did not parse with a span")
		return
	}
	match := target.MatchTarget(t.Allocator(), "./archive.o")
	if match == nil || len(match.Captures) != 1 || match.Captures[0].Name != "stem" || match.Captures[0].Text != "archive" {
		t.Error("target template did not capture stem")
		return
	}
	match.Free(t.Allocator())
}

func TestTargetDoubleStarCrossesSlash(t *testing.T) {
	target := template.ParseTarget(t.Allocator(), "test.km", "./{path:**}/{name:*}.c")
	defer target.Free()
	match := target.MatchTarget(t.Allocator(), "./src/lib/main.c")
	if match == nil || len(match.Captures) != 2 || match.Captures[0].Text != "src/lib" || match.Captures[1].Text != "main" {
		t.Error("double-star capture did not cross slash")
		return
	}
	match.Free(t.Allocator())
	if target.MatchTarget(t.Allocator(), "./src/lib/main.c/") != nil { t.Error("target matching was not anchored") }
}

func TestTargetEscapesAndLiteralTarget(t *testing.T) {
	escaped := template.ParseTarget(t.Allocator(), "test.km", "./\\{literal\\}-{name}")
	defer escaped.Free()
	match := escaped.MatchTarget(t.Allocator(), "./{literal}-ok")
	if match == nil || match.Captures[0].Text != "ok" { t.Error("escaped target literal was not matched") } else { match.Free(t.Allocator()) }
	plain := template.ParseTarget(t.Allocator(), "test.km", "./literal")
	defer plain.Free()
	match = plain.MatchTarget(t.Allocator(), "./literal")
	if len(plain.Diagnostics) != 0 || match == nil { t.Error("literal target was not accepted") } else { match.Free(t.Allocator()) }
}

func TestTargetCharacterClassesRejectDescendingRanges(t *testing.T) {
	invalid := template.ParseTarget(t.Allocator(), "test.km", "{name:[z-a]}")
	defer invalid.Free()
	if len(invalid.Diagnostics) != 1 {
		t.Error("descending character class range was accepted")
	}
	valid := template.ParseTarget(t.Allocator(), "test.km", "{name:[!a-c-a]}")
	defer valid.Free()
	match := valid.MatchTarget(t.Allocator(), "z")
	if len(valid.Diagnostics) != 0 || match == nil {
		t.Error("valid character class literals, negation, or range were rejected")
	} else {
		match.Free(t.Allocator())
	}
}

func TestFormatTemplateCanonicalizesEscapes(t *testing.T) {
	parsed := template.ParseString(t.Allocator(), "test.km", "a\\@b @(name)")
	defer parsed.Free()
	formatted := template.FormatString(t.Allocator(), parsed)
	if formatted != "a\\@b @(name)" { t.Errorf("FormatString() = %q", formatted) }
	mem.FreeString(t.Allocator(), formatted)
}

func TestDocumentIfElseLowersWithoutBlankDebt(t *testing.T) {
	doc := template.ParseDocument(t.Allocator(), "test.hash", "# @if(cond)\nyes\n# @else\nno\n# @end\n", "hash")
	defer doc.Free()
	if len(doc.Diagnostics) != 0 || doc.Root == nil { t.Error("hash document did not parse") }
}

func TestDocumentStrayEndIsBlockError(t *testing.T) {
	doc := template.ParseDocument(t.Allocator(), "test.tmpl", "@end\n", "plain")
	defer doc.Free()
	if len(doc.Diagnostics) == 0 || doc.Diagnostics[0].Code != "TPL_BLOCK" { t.Error("stray @end was not TPL_BLOCK") }
}

func TestDocumentUnknownStyleIsStyleError(t *testing.T) {
	doc := template.ParseDocument(t.Allocator(), "test.tmpl", "hi\n", "unknown")
	defer doc.Free()
	if len(doc.Diagnostics) == 0 || doc.Diagnostics[0].Code != "TPL_STYLE" { t.Error("unknown style was not TPL_STYLE") }
}
