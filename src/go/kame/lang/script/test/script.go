package script_test

import (
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func sameSpan(span source.Span, start int, end int) bool { return span.Start == start && span.End == end }

func TestScriptComposesPositionedLanguageForms(t *testing.T) {
	text := "# setup\nvalue = 42\n./out : value\n\techo @<\n(name value)\n"
	s := script.Parse(t.Allocator(), "test.km", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 4 {
		if len(s.Diagnostics) != 0 { t.Errorf("script items=%d diagnostic=%s span=%d:%d", len(s.Items), s.Diagnostics[0].Message, s.Diagnostics[0].Span.Start, s.Diagnostics[0].Span.End) } else { t.Errorf("script items=%d, want 4", len(s.Items)) }
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

func TestFormatScriptIsCanonical(t *testing.T) {
	s := script.Parse(t.Allocator(), "test.km", "# note\nvalue = 42\n./out : ./in\n  echo ok")
	defer s.Free()
	formatted := script.Format(t.Allocator(), s)
	if formatted != "# note\nvalue = 42\n./out : ./in\n\techo ok\n" { t.Errorf("Format() = %q", formatted) }
	mem.FreeString(t.Allocator(), formatted)
}

func TestScriptParsesMultilineDefinition(t *testing.T) {
	s := script.Parse(t.Allocator(), "test.km", "value = [\n  name: \"app\"\n]\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 1 || s.Items[0].Definition == nil { t.Error("multiline definition did not compose") }
}
