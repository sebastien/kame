package script_test

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestKashSourceKeepsCommandAndValueBoundaries(t *testing.T) {
	a := t.Allocator()
	text := "# source\nname = \"world\"; number = 1;\necho revision=$name # inline\n(banner WHO) = (cat \"hi \" WHO)\ncopy = ${name}; files = ./output;\n:cwd . printf \"%s\" @(banner name) | cat > \"output file\"\n"
	s := script.ParseKash(a, "test.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 9 { t.Error("valid Kash statements did not parse"); return }
	if s.Items[1].Definition.ValueKind != definition.ValueExpression || s.Items[1].Definition.Expression.Kind != expr.String || s.Items[2].Definition.Expression.Kind != expr.Integer { t.Error("Kash definitions inherited build word/template semantics") }
	if s.Items[3].Kind != script.Command || s.Items[3].Expression.Kind != expr.CommandGraph || len(s.Items[3].Expression.Items[0].Items) != 2 { t.Error("command '=' was mistaken for a definition") }
	if !s.Items[5].Definition.Function || s.Items[6].Definition.Expression.Kind != expr.Name || s.Items[7].Definition.Expression.Kind != expr.Path { t.Error("Kame header/value or Kash wrappers were not delegated") }
	graph := s.Items[8].Expression
	if len(graph.Items) != 2 || graph.Items[0].Items[0].Kind != expr.CommandSetup || graph.Items[1].Items[1].Kind != expr.CommandRedirection { t.Error("raw graph lost stage boundaries") }
	if graph.Span.Start <= s.Items[7].Span.End || graph.Items[1].Span.End > len(text) { t.Error("raw graph lost original source offsets") }
	formatted := script.Format(a, s)
	copy := script.ParseKash(a, "formatted.kash", formatted)
	if len(copy.Diagnostics) != 0 { t.Error("formatted Kash failed to parse") }
	again := script.Format(a, copy)
	if formatted != again { t.Error("Kash formatting is not idempotent") }
	mem.FreeString(a, formatted); mem.FreeString(a, again); copy.Free()
}

func TestKashStatementDelimitersAndMultilineValues(t *testing.T) {
	a := t.Allocator()
	text := ";;\r\nquoted = \"$(literal)\"; flag = :true;\nvalue = (cat\n \"nested;\"\n \"value\")\nprintf \"line one\nline two;still quoted\"; echo a\\;b\necho one \\\n two; echo $(printf nested\n | cat)\n// literal\n"
	s := script.ParseKash(a, "test.ksh", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 8 { t.Error("nested syntax or quoted delimiters split statements"); return }
	if s.Items[0].Definition.Expression.Kind != expr.String || s.Items[0].Definition.Expression.Parts[0].Expr != nil { t.Error("plain Kame definition string became a capture") }
	if s.Items[7].Kind != script.Command { t.Error("Kash interpreted // as a comment") }
}

func TestKashRejectsMalformedAndUnsupportedSource(t *testing.T) {
	invalid := []string{"name =\necho next", "name = one two", "name = a\nname = b", "(f X) = X\nf = 1", "value = ${name}suffix", "value = @(cat \"a\")suffix", "value = (cat \"a\" $name)", "echo |\necho next", "echo >\necho next", "echo > a | cat", "echo | cat < a", "echo $(printf one; printf two)", "if cat { echo yes }", "match value { echo yes }", "echo &", "value = 1 ?? 2", "echo \"unclosed"}
	for i := range invalid {
		s := script.ParseKash(t.Allocator(), "invalid.kash", invalid[i])
		if len(s.Diagnostics) == 0 { t.Error("malformed or unsupported Kash source was accepted") }
		if len(s.Diagnostics) != 0 && (s.Diagnostics[0].Span.Start < 0 || s.Diagnostics[0].Span.End > len(invalid[i])) { t.Error("Kash diagnostic escaped the original source") }
		s.Free()
	}
}
