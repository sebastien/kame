package ast_test

import (
	"kame/lang/ast"
	"solod.dev/so/bytes"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestCompositionASTPreservesFormsAndIncludeFields(t *testing.T) {
	langs := []string{"km", "kmk"}
	for i := range langs {
		buffer := bytes.NewBuffer(t.Allocator(), nil)
		status := ast.WriteAST(&buffer, langs[i], "composition", "(include? \"a b.km\")\n(if :true (include ./c.km) :nil)\n")
		text := buffer.String()
		if status != 0 || !strings.Contains(text, "\"kind\":\"composition\"") || !strings.Contains(text, "\"kind\":\"include\"") || !strings.Contains(text, "\"path\":\"a b.km\"") || !strings.Contains(text, "\"optional\":true") || !strings.Contains(text, "\"expression\":{\"kind\":\"application\"") {
			t.Error("composition AST lost original expression or additive include fields")
		}
		buffer.Free()
	}
}
