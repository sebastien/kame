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
	invalid := []string{"name =\necho next", "name = one two", "name = a\nname = b", "(f X) = X\nf = 1", "value = ${name}suffix", "value = @(cat \"a\")suffix", "value = (cat \"a\" $name)", "echo |\necho next", "echo >\necho next", "echo > a | cat", "echo | cat < a", "echo $(printf one; printf two)", "if cat { echo yes }", "match value { echo yes }", "echo && cat", "value = 1 ??", "echo \"unclosed"}
	for i := range invalid {
		s := script.ParseKash(t.Allocator(), "invalid.kash", invalid[i])
		if len(s.Diagnostics) == 0 { t.Error("malformed or unsupported Kash source was accepted") }
		if len(s.Diagnostics) != 0 && (s.Diagnostics[0].Span.Start < 0 || s.Diagnostics[0].Span.End > len(invalid[i])) { t.Error("Kash diagnostic escaped the original source") }
		s.Free()
	}
}

func TestKashValueRecoveryKeepsExpressionBoundary(t *testing.T) {
	a := t.Allocator()
	s := script.ParseKash(a, "recover.kash", "value = $(false) ?? missing ?? \"fallback\"; printf $value")
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 2 { t.Fatal("Kash recovery did not parse"); return }
	e := s.Items[0].Definition.Expression
	if e.Kind != expr.ValueRecovery || e.Items[1].Kind != expr.ValueRecovery || e.Items[0].Kind != expr.CommandCapture { t.Error("value recovery lost right-associative binding") }
	invalid := []string{"value = [1]?? [2]", "value = :nil ??\nnext = 1", "value = @(:nil ?? 1)", "value = (:nil ?? 1)"}
	for i := range invalid {
		bad := script.ParseKash(a, "bad.kash", invalid[i])
		if len(bad.Diagnostics) == 0 { t.Error("Kash recovery entered embedded expression syntax or crossed a statement") }
		bad.Free()
	}
}

func TestKashIfRetainsNestedBodiesAndLocalDefinitions(t *testing.T) {
	a := t.Allocator()
	s := script.ParseKash(a, "control.kash", "if @(:true)\n  name = \"inner\"\n  if false\n    printf no\n  else\n    printf $name\nelif true\n  printf fallback\nelse\n  printf no\nprintf after\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 2 { t.Fatal("nested conditional did not parse"); return }
	e := s.Items[0].Expression
	if e.Kind != expr.KashIf || len(e.Items) != 3 || e.Items[0].Body[0].Kind != expr.KashDefinition || e.Items[0].Body[1].Kind != expr.KashIf || e.Items[1].Items[0].Kind != expr.CommandTest { t.Error("conditional AST lost its language or lexical boundaries") }
	formatted := script.Format(a, s)
	defer mem.FreeString(a, formatted)
	again := script.ParseKash(a, "canonical.kash", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 { t.Error("canonical control syntax did not parse") }
	canonical := script.Format(a, again)
	defer mem.FreeString(a, canonical)
	if formatted != canonical { t.Error("control formatting changed block nesting") }
}

func TestKashMatchRetainsSubjectAndArmBoundaries(t *testing.T) {
	a := t.Allocator()
	s := script.ParseKash(a, "match.kash", "match $(printf main.c)\n  case \"{name:*}.c\"\n    local = name\n    printf $local\n  else\n    printf no\nprintf after\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 2 { t.Fatal("match did not parse"); return }
	e := s.Items[0].Expression
	if e.Kind != expr.KashMatch || len(e.Items) != 2 || e.Body[0].Kind != expr.CommandCapture || e.Items[0].Items[0].Pattern == nil || e.Items[0].Body[0].Kind != expr.KashDefinition { t.Error("match lost its subject, pattern or lexical definition") }
	cloned := expr.Clone(a, e)
	defer expr.Free(a, cloned)
	formatted := expr.FormatKashControl(a, cloned, "\t")
	defer mem.FreeString(a, formatted)
	again := script.ParseKash(a, "canonical.kash", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || len(again.Items) != 1 { t.Error("canonical match failed to reparse") }
}
