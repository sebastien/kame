package scriptcheck

import (
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"strings"
	"testing"
)

func TestKashRequiresExplicitBindingsInAllBlockContexts(t *testing.T) {
	for _, text := range []string{"value = 1\n", "if true\n\tvalue = 1\n", "def f ()\n\tvalue = 1\n"} {
		parsed := script.ParseKash(nil, "invalid.kash", text)
		if len(parsed.Diagnostics) == 0 || !strings.Contains(parsed.Diagnostics[0].Message, "bare binding") {
			t.Errorf("bare binding was accepted in %q: %v", text, parsed.Diagnostics)
		}
		parsed.Free()
	}
	recipe := script.ParseKashRuleBody(nil, "recipe", "value = 1\n")
	defer recipe.Free()
	if len(recipe.Diagnostics) == 0 || !strings.Contains(recipe.Diagnostics[0].Message, "bare binding") {
		t.Fatalf("bare binding was accepted in a rule: %v", recipe.Diagnostics)
	}
	const valid = "def value = 1\nlet next = value + 1\necho a = b\n"
	parsed := script.ParseKash(nil, "bindings.kash", valid)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 3 || parsed.Items[1].Definition.Expression.Items[0].Infix != "+" ||
		parsed.Items[2].Kind != script.Command {
		t.Fatalf("binding grammar consumed a command or lost the value expression: %v", parsed.Diagnostics)
	}
}

func TestDefinitionRHSUsesValueExpressionGrammar(t *testing.T) {
	const input = "value = f 1 2 +\n    3\n" +
		"selected = match value\n    3 => true\n    else => false\n" +
		"next = 4\n"
	parsed := script.Parse(nil, "definitions.km", input)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 3 {
		t.Fatalf("value expressions consumed the wrong definition range: %v", parsed.Diagnostics)
	}
	value := parsed.Items[0].Definition.Expression
	if value.Infix != "+" || value.Items[1].Kind != expr.Application || !value.Items[1].Juxtaposed {
		t.Fatal("definition did not keep juxtaposition and operator precedence")
	}
	if match := parsed.Items[1].Definition.Expression; match.Kind != expr.Match || len(match.Items) != 2 {
		t.Fatal("definition did not keep its match arms")
	}
	if next := parsed.Items[2].Definition; next.Name != "next" || next.Expression.Int != 4 {
		t.Fatal("match consumed a following definition")
	}
}

func TestValueBlockDisambiguatesContinuedExpressions(t *testing.T) {
	const text = "value = # Compute the result\n\tlet x = 1 + 1\n\tif x == 2\n\t\treturn x + 1\n\treturn\n\n" +
		"continued =\n\tif true then 3 else 0\n" +
		"selected = match value\n\t3 => true\n\telse => false\n" +
		"next = 4\n"
	s := script.Parse(nil, "blocks.km", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 4 {
		t.Fatalf("value blocks failed to parse: %v", s.Diagnostics)
	}
	d := s.Items[0].Definition.Expression
	if d.Kind != expr.ValueBlock || len(d.Body) != 3 || d.Body[2].Kind != expr.ValueReturn ||
		s.Items[1].Definition.Expression.Kind != expr.Conditional || s.Items[2].Definition.Expression.Kind != expr.Match {
		t.Fatalf("incorrect distinction between blocks and expressions: %+v", d)
	}
	formatted := script.Format(nil, s)
	again := script.Parse(nil, "formatted.km", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || script.Format(nil, again) != formatted ||
		again.Items[2].Definition.Expression.Kind != expr.Match {
		t.Fatalf("formatter changed the value definition grammar: %q %v", formatted, again.Diagnostics)
	}
}

func TestKashAssignAndYieldAreValueStatements(t *testing.T) {
	const text = "def items ()\n\tlet count = 1\n\tset count = count + 1\n\tset env/PORT = 8000 + count\n\tyield [count, count + 1]\n\treturn\nprintf value = literal\n"
	s := script.ParseKash(nil, "statements.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 2 {
		t.Fatalf("assignment and yield failed to parse: %v", s.Diagnostics)
	}
	f := s.Items[0].Definition.Expression
	if !f.Bool || len(f.Body) != 5 || f.Body[1].Kind != expr.KashSet ||
		f.Body[2].Text != "env/PORT" || f.Body[3].Kind != expr.KashYield ||
		s.Items[1].Kind != script.Command {
		t.Fatalf("incorrect statement classifications: %+v", f)
	}
	if formatted := script.Format(nil, s); formatted != text {
		t.Fatalf("statements formatted as %q", formatted)
	}

	for _, text := range []string{
		"yield 1",
		"set env/BAD-NAME = 1",
		"set value =",
		"def items ()\n\tyield 1\n\treturn 2",
		"def items ()\n\tdefer yield 1",
	} {
		s := script.ParseKash(nil, "invalid.kash", text)
		if len(s.Diagnostics) == 0 || s.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("accepted invalid statement %q: %v", text, s.Diagnostics)
		}
		s.Free()
	}
}

func TestNestedGeneratorDoesNotChangeEnclosingReturn(t *testing.T) {
	const text = "def outer ()\n\tdef inner ()\n\t\tyield 1\n\treturn 0\n"
	s := script.ParseKash(nil, "nested.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 1 {
		t.Fatalf("nested generator failed to parse: %v", s.Diagnostics)
	}
	outer := s.Items[0].Definition.Expression
	if outer.Bool || !outer.Body[0].Items[0].Bool {
		t.Fatalf("generator flag propagated across a function boundary: %+v", outer)
	}
}

func TestFunctionHeadersRequireDefInBothSourceGrammars(t *testing.T) {
	for _, lang := range []string{"km", "kash"} {
		for _, text := range []string{"(value) = 1\n", "(add x y) = x + y\n"} {
			authored := source.New(nil, "invalid.km", text)
			parsed := script.ParseFragment(nil, authored, lang, 0, len(text))
			if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
				t.Errorf("%s accepted old function header %q", lang, text)
			}
			parsed.Free()
			authored.Free(nil)
		}
	}
}

func TestLongDefHeaderKeepsParametersOnOneLine(t *testing.T) {
	header := "def example first " + strings.Repeat("x", 72) + " rest... ="
	s := script.Parse(nil, "long.km", header+" first\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 {
		t.Fatalf("function failed to parse: %v", s.Diagnostics)
	}
	if formatted := script.Format(nil, s); formatted != header+"\n\tfirst\n" {
		t.Fatalf("formatter split the function header: %q", formatted)
	}
}

func TestReturnRequiresAnEnclosingBody(t *testing.T) {
	for _, example := range []struct{ lang, text string }{
		{"km", "return\n"},
		{"km", "return 1\n"},
		{"km", "if true\n\treturn 1\n"},
		{"kash", "return\n"},
		{"kash", "if true\n\treturn 1\n"},
	} {
		authored := source.New(nil, "invalid."+example.lang, example.text)
		parsed := script.ParseFragment(nil, authored, example.lang, 0, len(example.text))
		if len(parsed.Diagnostics) != 1 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%s did not reject %q with a parse error: %v", example.lang, example.text, parsed.Diagnostics)
		}
		parsed.Free()
		authored.Free(nil)
	}
}
