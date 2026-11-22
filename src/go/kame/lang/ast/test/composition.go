package astcheck

import (
	"bytes"
	"kame/internal/testing"
	"kame/lang/ast"
	"kame/lang/script"
	"strings"
)

func TestCompositionASTPreservesFormsAndIncludeFields(t *testing.T) {
	langs := []string{"km", script.KameFileLanguage}
	for i := range langs {
		buffer := bytes.Buffer{}
		status := ast.WriteAST(&buffer, langs[i], "composition", "include? ./a\\ b.km\nif true\n  if true\n    include ./c.km\nif true\n  if true\n    include (joinpath ./ \"d.km\")\nif true\n  for item in []\n    rule ./e = item\n")
		text := buffer.String()
		if status != 0 || !strings.Contains(text, "\"kind\":\"composition\"") || !strings.Contains(text, "\"kind\":\"include\"") || !strings.Contains(text, "\"path\":\"./a b.km\"") || !strings.Contains(text, "\"optional\":true") || !strings.Contains(text, "\"condition\":{\"kind\":\"boolean\"") {
			t.Error("composition AST lost a condition or include fields")
		}
		if strings.Count(text, `"loadsSources":true,"expandsSources":true`) != 4 ||
			strings.Count(text, `"loadsSources":true,"expandsSources":false,"declaresRuleWork":true`) != 3 ||
			strings.Count(text, `"declaresRuleWork":true`) != 6 {
			t.Error("composition AST did not distinguish flattening from scoped rule work: " + text)
		}
		buffer.Reset()
	}
}
