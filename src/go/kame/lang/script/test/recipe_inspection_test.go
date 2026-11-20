package scriptcheck

import (
	"bytes"
	"fmt"
	"kame/lang/ast"
	"kame/lang/format"
	"kame/lang/script"
	"strings"
	"testing"
)

func TestKnownKashRecipeInspectionUsesAuthoredSpansAndFormatsBlocks(t *testing.T) {
	const text = "if true\n  rule ./out\n    if true\n      return    \"text\"\n"
	const want = "if true\n\trule ./out\n\t\tif true\n\t\t\treturn \"text\"\n"
	result := format.Source(nil, script.KameFileLanguage, "nested.km", text, "tabs", 4)
	if !result.OK || result.Text != want {
		t.Fatalf("Kash rule formatted as %q: %s", result.Text, result.Message)
	}
	result = format.Source(nil, script.KameFileLanguage, "nested.km", want, "tabs", 4)
	if !result.OK || result.Text != want {
		t.Fatalf("Kash rule format is not idempotent: %q %s", result.Text, result.Message)
	}
	var encoded bytes.Buffer
	span := fmt.Sprintf(`"span":{"start":%d,"end":%d}`, strings.Index(text, "return"), len(text)-1)
	if ast.WriteAST(&encoded, script.KameFileLanguage, "nested.km", text) != 0 || !strings.Contains(encoded.String(), `"bodyStatements":[`) ||
		!strings.Contains(encoded.String(), `"kind":"return",`+span) {
		t.Fatalf("Kash rule body lost authored structure: %s", encoded.String())
	}
}

func TestRecipeInspectionDefersUnknownSelectorsAndPreservesForeignText(t *testing.T) {
	for _, example := range []struct{ lang, text, want string }{
		{script.KameFileLanguage, "task check ; {shell: \"sh\"}\n  f() { return 0; }; f\n", "task check ; {shell: \"sh\"}\n\tf() { return 0; }; f\n"},
		{"km", "SHELL = selected\ntask check\n  f() { return 0; }; f\n", "SHELL = selected\ntask check\n\tf() { return 0; }; f\n"},
		{"km", "include ./config.km\ntask check\n  f() { return 0; }; f\n", "include ./config.km\ntask check\n\tf() { return 0; }; f\n"},
	} {
		result := format.Source(nil, example.lang, "program.km", example.text, "tabs", 4)
		if !result.OK || result.Text != example.want {
			t.Errorf("foreign or unknown recipe formatted as %q: %s", result.Text, result.Message)
		}
	}
	for _, lang := range []string{"km", script.KameFileLanguage} {
		const text = "rule ./out\n  return 1 ?\n"
		result := format.Source(nil, lang, "program.km", text, "tabs", 4)
		var encoded bytes.Buffer
		if result.OK || result.Code != "PARSE_ERR" || ast.WriteAST(&encoded, lang, "program.km", text) != 1 ||
			!strings.Contains(encoded.String(), `"code":"PARSE_ERR"`) {
			t.Errorf("%s accepted invalid return: %s", lang, encoded.String())
		}
	}
}

func TestRecipeInspectionPreservesMultilineLiteralContents(t *testing.T) {
	const text = "rule ./out\n  return \"\"\"before\n    after\"\"\"\n"
	const want = "rule ./out\n\treturn \"\"\"before\n\t  after\"\"\"\n"
	result := format.Source(nil, "km", "program.km", text, "tabs", 4)
	if !result.OK || result.Text != want {
		t.Fatalf("multiline recipe literal formatted as %q: %s", result.Text, result.Message)
	}
}
