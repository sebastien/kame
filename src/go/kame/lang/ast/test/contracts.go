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
		if lang == "rule" { text = "./{name:~[a-z]+} ./{name:*} : @{PREP}, @{BUILD} ./in/{name:~[a-z]+}" }
		if lang == "template" { text = "@(nop " + text + ")" }
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

func TestRuleCapturePatternFieldAndAuthoredCoordinates(t *testing.T) {
	a := t.Allocator()
	texts := []string{`"./out\t{name}.o" :`, `./out/{name}.o : ./in/{name}.c`, `"./out\"/{name:~[a-z]+}.o" :`}
	for i := range texts {
		buffer := bytes.NewBuffer(a, nil)
		if ast.WriteAST(&buffer, "rule", "captures", texts[i]) != 0 { t.Error("capture AST failed") }
		text := buffer.String()
		if !strings.Contains(text, `"pattern":{"kind":"target-template"`) { t.Error("capture AST lacks stable pattern field") }
		if i == 0 && (!strings.Contains(text, `"span":{"start":8,"end":14},"kind":"capture"`) || !strings.Contains(text, `"pattern":{"kind":"target-template","span":{"start":1,"end":16}`)) { t.Error("quoted target AST lost authored content or capture spans") }
		if i == 1 && strings.Count(text, `"template":{"kind":"target-template"`) != 2 { t.Error("schema-1 legacy template aliases disappeared") }
		if i == 2 && (!strings.Contains(text, `"span":{"start":9,"end":23},"kind":"capture"`) || !strings.Contains(text, `"regex":true`) || !strings.Contains(text, `"pattern":{"kind":"target-template","span":{"start":1,"end":25}`)) { t.Error("quoted regex target AST lost authored coordinates") }
		buffer.Free()
	}
}
