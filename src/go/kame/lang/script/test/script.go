package script_test

import (
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func sameSpan(span source.Span, start int, end int) bool {
	return span.Start == start && span.End == end
}

func TestScriptComposesPositionedLanguageForms(t *testing.T) {
	text := "# setup\nvalue = 42\n./out : value\n\techo @<\n(name value)\n\tstray\n"
	s := script.Parse(t.Allocator(), "test.km", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 4 {
		if len(s.Diagnostics) != 0 {
			t.Errorf("script items=%d diagnostic=%s span=%d:%d", len(s.Items), s.Diagnostics[0].Message, s.Diagnostics[0].Span.Start, s.Diagnostics[0].Span.End)
		} else {
			t.Errorf("script items=%d, want 4", len(s.Items))
		}
		return
	}
	if s.Items[0].Kind != script.Comment || s.Items[1].Definition == nil || s.Items[2].Rule == nil || s.Items[3].Expression == nil {
		t.Error("script item kind did not retain its delegated AST")
	}
	if !sameSpan(s.Items[2].Rule.Span, 19, 41) || !sameSpan(s.Items[2].Rule.Body[0].Span, 34, 41) || !sameSpan(s.Items[3].Expression.Span, 42, 54) {
		t.Error("delegated AST spans were not relative to the script source")
	}
}

func TestScriptReportsOrphanedIndent(t *testing.T) {
	s := script.Parse(t.Allocator(), "test.km", "\techo stray\n")
	defer s.Free()
	if len(s.Diagnostics) != 1 || s.Diagnostics[0].Code != "PARSE_ERR" || !sameSpan(s.Diagnostics[0].Span, 0, 11) {
		t.Error("orphaned indentation did not produce a positioned parse error")
	}
}

func TestBorrowedParseMatchesOwningParseWithFewerAllocations(t *testing.T) {
	a := t.Allocator()
	tracker := a.(*mem.Tracker)
	name := "borrowed.km"
	text := "# setup\nvalue = 42\n./out : value\n\techo @<\n(name value)\n"

	before := tracker.Stats().TotalAlloc
	owned := script.Parse(a, name, text)
	ownedAllocations := tracker.Stats().TotalAlloc - before
	defer owned.Free()

	before = tracker.Stats().TotalAlloc
	borrowed := script.ParseBorrowed(a, name, text)
	borrowedAllocations := tracker.Stats().TotalAlloc - before
	defer borrowed.Free()

	if owned.BorrowedSource || !borrowed.BorrowedSource || borrowed.Source.Name != name || borrowed.Source.Text != text {
		t.Error("parse ownership or borrowed source content was not retained")
	}
	if len(owned.Items) != len(borrowed.Items) || len(owned.Diagnostics) != len(borrowed.Diagnostics) {
		t.Error("borrowed parse changed item or diagnostic counts")
		return
	}
	for i := range owned.Items {
		if owned.Items[i].Kind != borrowed.Items[i].Kind || owned.Items[i].Text != borrowed.Items[i].Text || owned.Items[i].Span.Start != borrowed.Items[i].Span.Start || owned.Items[i].Span.End != borrowed.Items[i].Span.End {
			t.Errorf("item %d differs between owning and borrowed parses", i)
		}
	}
	for i := range owned.Diagnostics {
		if owned.Diagnostics[i].Code != borrowed.Diagnostics[i].Code || owned.Diagnostics[i].Message != borrowed.Diagnostics[i].Message || owned.Diagnostics[i].Span.Start != borrowed.Diagnostics[i].Span.Start || owned.Diagnostics[i].Span.End != borrowed.Diagnostics[i].Span.End {
			t.Errorf("diagnostic %d differs between owning and borrowed parses", i)
		}
	}
	ownedText, borrowedText := script.Format(a, owned), script.Format(a, borrowed)
	if ownedText != borrowedText {
		t.Error("borrowed parse changed canonical formatting")
	}
	mem.FreeString(a, ownedText)
	mem.FreeString(a, borrowedText)
	if len(owned.Items) > 2 && (owned.Items[2].Rule == nil || borrowed.Items[2].Rule == nil || owned.Items[2].Rule.Span.Start != borrowed.Items[2].Rule.Span.Start || owned.Items[2].Rule.Span.End != borrowed.Items[2].Rule.Span.End) {
		t.Error("borrowed parse changed rule spans")
	}
	if ownedAllocations < borrowedAllocations+uint64(len(text)) {
		t.Errorf("owning parse allocated %d bytes and borrowed parse allocated %d, want at least %d bytes saved", ownedAllocations, borrowedAllocations, len(text))
	}
}

func TestFormatScriptIsCanonical(t *testing.T) {
	s := script.Parse(t.Allocator(), "test.km", "# note\nvalue = 42\n./out : ./in\n  echo ok")
	defer s.Free()
	formatted := script.Format(t.Allocator(), s)
	if formatted != "# note\nvalue = 42\n./out : ./in\n\techo ok\n" {
		t.Errorf("Format() = %q", formatted)
	}
	mem.FreeString(t.Allocator(), formatted)
}

func TestFormatScriptPreservesBlankLines(t *testing.T) {
	text := "\nvalue=42\n\n\n# note\n\n./out : ./in\n\techo ok\n\n(name value)\n\n"
	s := script.Parse(t.Allocator(), "test.km", text)
	defer s.Free()
	formatted := script.Format(t.Allocator(), s)
	want := "\nvalue = 42\n\n\n# note\n\n./out : ./in\n\techo ok\n\n(name value)\n\n"
	if formatted != want {
		t.Errorf("Format() = %q, want %q", formatted, want)
	}
	mem.FreeString(t.Allocator(), formatted)
}

func TestScriptParsesMultilineDefinition(t *testing.T) {
	s := script.Parse(t.Allocator(), "test.km", "value = [\n  name: \"app\"\n]\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 1 || s.Items[0].Definition == nil {
		t.Error("multiline definition did not compose")
	}
}

func TestContinuationsPreserveSourceSpansAndRecipeBackslashes(t *testing.T) {
	a := t.Allocator()
	text := "# comment \\\nwords = one \\\n  two \\\r\n  three\npaths = (list ./a \\\n ./b)\n./out : ./a \\\n ./b @(paths)\n\tprintf one \\\n\t two\n"
	s := script.Parse(a, "continued.kmk", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 4 || s.Source.Text != text {
		t.Error("continuation changed source or item boundaries")
		return
	}
	words := s.Items[1].Definition.Words
	if len(words) != 3 || words[0].Text != "one" || words[1].Text != "two" || words[2].Text != "three" {
		t.Error("continued definition did not split words")
		return
	}
	position := s.Source.Position(words[2].Span.Start)
	if position.Line != 4 || position.Column != 3 {
		t.Error("continuation lost authored position")
	}
	r := s.Items[3].Rule
	if len(r.Inputs) != 3 || r.Inputs[0].Text != "./a" || r.Inputs[1].Text != "./b" || len(r.Body) != 2 || r.Body[0].Text != "printf one \\" {
		t.Error("continued header or shell backslash changed")
	}
}

func TestOptionalIncludesPreserveParsingAndFormatting(t *testing.T) {
	text := "include? ./optional.kmk\ninclude ./required.kmk\n"
	parsed := script.Parse(t.Allocator(), "test.kmk", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 2 || !parsed.Items[0].OptionalInclude || parsed.Items[1].OptionalInclude || parsed.Items[0].Include != "./optional.kmk" {
		t.Error("optional include did not retain its path and required distinction")
		return
	}
	formatted := script.Format(t.Allocator(), parsed)
	defer mem.FreeString(t.Allocator(), formatted)
	if formatted != text {
		t.Error("optional include formatting changed")
	}
}

func TestGeneratedDeclarationParsesFormatsAndRetainsName(t *testing.T) {
	text := "MODULES = [\"core\" \"cli\"]\ngenerate checks = (map ([module] [kind: \"task\" target: (join (list \"check-\" module) \"\") inputs: [] order-only: [] recipe: [\"go test ./...\"]]) MODULES)\n"
	parsed := script.Parse(t.Allocator(), "generated.kmk", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 2 {
		message := ""
		for i := range parsed.Diagnostics {
			message += parsed.Diagnostics[i].Message + " "
		}
		t.Errorf("generated declaration parse shape: diagnostics=%d items=%d %s", len(parsed.Diagnostics), len(parsed.Items), message)
		return
	}
	if parsed.Items[1].Kind != script.Generate || parsed.Items[1].GenerateName != "checks" || parsed.Items[1].Expression == nil {
		t.Errorf("generated declaration item kind=%d name=%q", parsed.Items[1].Kind, parsed.Items[1].GenerateName)
		return
	}
	formatted := script.Format(t.Allocator(), parsed)
	defer mem.FreeString(t.Allocator(), formatted)
	again := script.Parse(t.Allocator(), "generated.kmk", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || len(again.Items) != 2 || again.Items[1].Kind != script.Generate || again.Items[1].GenerateName != "checks" {
		t.Error("generated declaration formatting changed its structure")
	}
}
