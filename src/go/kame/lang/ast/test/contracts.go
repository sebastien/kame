package ast_test

import (
	"kame/lang/ast"
	"kame/lang/format"
	"solod.dev/so/bytes"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestSchemaOneAdditiveSemanticFields(t *testing.T) {
	a := t.Allocator()
	definitionLanguages := []string{"km", "kmk", "kash"}
	for _, lang := range definitionLanguages {
		buffer := bytes.NewBuffer(a, nil)
		if ast.WriteAST(&buffer, lang, "functions", "(f) = 1\nv = 1\n") != 0 || !strings.Contains(buffer.String(), "\"function\":true") || !strings.Contains(buffer.String(), "\"function\":false") || !strings.Contains(buffer.String(), "\"valueKind\":\"expression\"") {
			t.Error("schema-1 definition lost zero-parameter function/value distinction")
		}
		buffer.Free()
	}
	patternLanguages := []string{"expr", "km", "kmk", "kash", "rule", "template"}
	for _, lang := range patternLanguages {
		text := "[./{name:~[a-z]+} ./{name:*}]"
		if lang == "kash" { text = "p = " + text }
		if lang == "rule" { text = "./{name:~[a-z]+} ./{name:*} : @(PREP), @(BUILD) ./in/{name:~[a-z]+}" }
		if lang == "template" { text = "@(" + text + ")" }
		formatted := format.Source(a, lang, "regex", text, "tabs", 4)
		if !formatted.OK {
			t.Error("regex source failed formatting")
		} else {
			buffer := bytes.NewBuffer(a, nil)
			if ast.WriteAST(&buffer, lang, "regex", formatted.Text) != 0 || !strings.Contains(buffer.String(), "\"regex\":true") || !strings.Contains(buffer.String(), "\"regex\":false") || !strings.Contains(buffer.String(), "\"schema\":1") {
				t.Error("formatted regex mode missing from additive schema-1 AST")
			}
			if lang == "rule" && (!strings.Contains(buffer.String(), "\"sequenceEnd\":true") || !strings.Contains(buffer.String(), "\"sequenceEnd\":false")) {
				t.Error("input sequence boundary missing from AST")
			}
			buffer.Free()
		}
		mem.FreeString(a, formatted.Text)
		mem.FreeString(a, formatted.Code)
		mem.FreeString(a, formatted.Message)
	}
}

func TestValueASTPreservesCommentItems(t *testing.T) {
	buffer := bytes.NewBuffer(t.Allocator(), nil)
	defer buffer.Free()
	if ast.WriteAST(&buffer, "km", "comments.km", "# before\nv = 1\n// after\n") != 0 || !strings.Contains(buffer.String(), "\"text\":\"# before\"") || !strings.Contains(buffer.String(), "\"text\":\"// after\"") {
		t.Error("value AST dropped authored comments")
	}
}
