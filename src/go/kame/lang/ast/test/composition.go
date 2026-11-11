package astcheck

import (
	"bytes"
	"kame/internal/testing"
	"kame/lang/ast"
	"strings"
)

func TestCompositionASTPreservesFormsAndIncludeFields(t *testing.T) {
	langs := []string{"km", "kmk"}
	for i := range langs {
		buffer := bytes.Buffer{}
		status := ast.WriteAST(&buffer, langs[i], "composition", "(include? \"a b.km\")\n(if :true (include ./c.km) :nil)\n")
		text := buffer.String()
		if status != 0 || !strings.Contains(text, "\"kind\":\"composition\"") || !strings.Contains(text, "\"kind\":\"include\"") || !strings.Contains(text, "\"path\":\"a b.km\"") || !strings.Contains(text, "\"optional\":true") || !strings.Contains(text, "\"expression\":{\"kind\":\"application\"") {
			t.Error("composition AST lost original expression or additive include fields")
		}
		buffer.Reset()
	}
}
