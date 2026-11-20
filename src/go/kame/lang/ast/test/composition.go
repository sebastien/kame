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
		status := ast.WriteAST(&buffer, langs[i], "composition", "include? ./a\\ b.km\nif true\n  include ./c.km\n")
		text := buffer.String()
		if status != 0 || !strings.Contains(text, "\"kind\":\"composition\"") || !strings.Contains(text, "\"kind\":\"include\"") || !strings.Contains(text, "\"path\":\"./a b.km\"") || !strings.Contains(text, "\"optional\":true") || !strings.Contains(text, "\"condition\":{\"kind\":\"boolean\"") {
			t.Error("composition AST lost a condition or include fields")
		}
		buffer.Reset()
	}
}
