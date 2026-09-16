package fixture_test

import (
	"littlemake/lang/expr"
	"littlemake/lang/script"
	"littlemake/lang/source"
	"littlemake/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func fixtureText(t *testing.T, path string) string {
	bytes, err := os.ReadFile(t.Allocator(), path)
	if err != nil { t.Fatalf("ReadFile(%s): %s", path, err.Error()); return "" }
	text := strings.Clone(t.Allocator(), string(bytes))
	slices.Free(t.Allocator(), bytes)
	return text
}

func TestRepresentativeFixtures(t *testing.T) {
	a := t.Allocator()
	text := fixtureText(t, "tests/data/lang/expr/pipe-chain.lm")
	e := expr.Parse(a, "pipe-chain.lm", text)
	mem.FreeString(a, text)
	if len(e.Diagnostics) != 0 { t.Errorf("pipe-chain fixture diagnostics=%d %s", len(e.Diagnostics), e.Diagnostics[0].Message) }
	e.Free()
	text = fixtureText(t, "tests/data/lang/template/target/literal-only.lm")
	target := template.ParseTarget(a, "literal-only.lm", text)
	mem.FreeString(a, text)
	if len(target.Diagnostics) != 0 { t.Error("literal target fixture did not parse") }
	target.Free()
	text = fixtureText(t, "tests/data/lang/rule/definition/quoted.lm")
	s := script.Parse(a, "quoted.lm", text)
	mem.FreeString(a, text)
	if len(s.Diagnostics) != 0 { t.Errorf("quoted definition fixture diagnostics=%d %s", len(s.Diagnostics), s.Diagnostics[0].Message) }
	s.Free()
	text = fixtureText(t, "tests/data/lang/rule/rules/inputs.lm")
	s = script.Parse(a, "inputs.lm", text)
	mem.FreeString(a, text)
	if len(s.Diagnostics) != 0 { t.Errorf("rule input fixture diagnostics=%d %s", len(s.Diagnostics), s.Diagnostics[0].Message) }
	s.Free()
	text = fixtureText(t, "tests/data/lang/script/default-task.lm")
	s = script.Parse(a, "default-task.lm", text)
	mem.FreeString(a, text)
	if len(s.Diagnostics) != 0 { t.Errorf("script fixture diagnostics=%d %s", len(s.Diagnostics), s.Diagnostics[0].Message) }
	s.Free()
}

func TestInvalidFixture(t *testing.T) {
	entries, err := os.ReadDir(t.Allocator(), "tests/data/lang/invalid")
	if err != nil { t.Fatalf("ReadDir(invalid): %s", err.Error()); return }
	defer os.FreeDirEntry(t.Allocator(), entries)
	for i := range entries {
		name := entries[i].Name
		if !strings.HasSuffix(name, ".lm") { continue }
		path := "tests/data/lang/invalid/" + name
		text := fixtureText(t, path)
		count := 0
		diagnostic := source.Diagnostic{}
		if strings.HasPrefix(name, "expr-") {
			parsed := expr.Parse(t.Allocator(), name, text); count = len(parsed.Diagnostics); if count != 0 { diagnostic = parsed.Diagnostics[0] }; parsed.Free()
		} else if strings.HasPrefix(name, "template-string-") {
			parsed := template.ParseString(t.Allocator(), name, text); count = len(parsed.Diagnostics); if count != 0 { diagnostic = parsed.Diagnostics[0] }; parsed.Free()
		} else if strings.HasPrefix(name, "template-target-") {
			parsed := template.ParseTarget(t.Allocator(), name, text); count = len(parsed.Diagnostics); if count != 0 { diagnostic = parsed.Diagnostics[0] }; parsed.Free()
		} else {
			parsed := script.Parse(t.Allocator(), name, text); count = len(parsed.Diagnostics); if count != 0 { diagnostic = parsed.Diagnostics[0] }; parsed.Free()
		}
		mem.FreeString(t.Allocator(), text)
		if count == 0 { t.Errorf("%s: expected diagnostic", path); continue }
		expected := source.Error
		if name == "rule-recipe-malformed-interpolation.lm" || strings.HasPrefix(name, "template-string-malformed-") { expected = source.Warning }
		if diagnostic.Code != "PARSE_ERR" || diagnostic.Severity != expected { t.Errorf("%s: diagnostic = %s/%d", path, diagnostic.Code, diagnostic.Severity) }
	}
}

const (
	fixtureExpr = iota
	fixtureString
	fixtureTarget
	fixtureScript
)

func TestValidFixtureTree(t *testing.T) {
	checkFixtureDirectory(t, "tests/data/lang/expr", fixtureExpr)
	checkFixtureDirectory(t, "tests/data/lang/template/string", fixtureString)
	checkFixtureDirectory(t, "tests/data/lang/template/target", fixtureTarget)
	checkFixtureDirectory(t, "tests/data/lang/rule", fixtureScript)
	checkFixtureDirectory(t, "tests/data/lang/script", fixtureScript)
}

func TestFormatIdempotence(t *testing.T) {
	a := t.Allocator()
	e := expr.Parse(a, "test.lm", "(value | first | second b)")
	formatted := expr.Format(a, e.Expr)
	e2 := expr.Parse(a, "formatted.lm", formatted)
	formatted2 := expr.Format(a, e2.Expr)
	if len(e.Diagnostics) != 0 || len(e2.Diagnostics) != 0 || formatted != formatted2 { t.Error("expression format was not idempotent") }
	mem.FreeString(a, formatted2); e2.Free(); mem.FreeString(a, formatted); e.Free()
	template1 := template.ParseString(a, "test.lm", "echo @(count files) @<")
	formatted = template.FormatString(a, template1)
	template2 := template.ParseString(a, "formatted.lm", formatted)
	formatted2 = template.FormatString(a, template2)
	if len(template1.Diagnostics) != 0 || len(template2.Diagnostics) != 0 || formatted != formatted2 { t.Error("template format was not idempotent") }
	mem.FreeString(a, formatted2); template2.Free(); mem.FreeString(a, formatted); template1.Free()
	script1 := script.Parse(a, "test.lm", "value = [name: \"app\"]\n./out : ./in\n  echo @<")
	formatted = script.Format(a, script1)
	script2 := script.Parse(a, "formatted.lm", formatted)
	formatted2 = script.Format(a, script2)
	if len(script1.Diagnostics) != 0 || len(script2.Diagnostics) != 0 || formatted != formatted2 { t.Error("script format was not idempotent") }
	mem.FreeString(a, formatted2); script2.Free(); mem.FreeString(a, formatted); script1.Free()
}

func checkFixtureDirectory(t *testing.T, path string, kind int) {
	entries, err := os.ReadDir(t.Allocator(), path)
	if err != nil { t.Fatalf("ReadDir(%s): %s", path, err.Error()); return }
	defer os.FreeDirEntry(t.Allocator(), entries)
	for i := range entries {
		entry := entries[i]
		name := path + "/" + entry.Name
		if entry.IsDir { checkFixtureDirectory(t, name, kind); continue }
		if !strings.HasSuffix(entry.Name, ".lm") { continue }
		text := fixtureText(t, name)
		if kind == fixtureExpr {
			parsed := expr.Parse(t.Allocator(), entry.Name, text)
			if len(parsed.Diagnostics) != 0 { t.Errorf("%s: expression diagnostics=%d %s", name, len(parsed.Diagnostics), parsed.Diagnostics[0].Message) }
			parsed.Free()
		} else if kind == fixtureString {
			parsed := template.ParseString(t.Allocator(), entry.Name, text)
			if len(parsed.Diagnostics) != 0 { t.Errorf("%s: template diagnostics=%d", name, len(parsed.Diagnostics)) }
			parsed.Free()
		} else if kind == fixtureTarget {
			parsed := template.ParseTarget(t.Allocator(), entry.Name, text)
			if len(parsed.Diagnostics) != 0 { t.Errorf("%s: target diagnostics=%d", name, len(parsed.Diagnostics)) }
			parsed.Free()
		} else {
			parsed := script.Parse(t.Allocator(), entry.Name, text)
			if len(parsed.Diagnostics) != 0 { t.Errorf("%s: script diagnostics=%d", name, len(parsed.Diagnostics)) }
			parsed.Free()
		}
		mem.FreeString(t.Allocator(), text)
	}
}
