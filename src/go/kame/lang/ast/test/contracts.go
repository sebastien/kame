package astcheck

import (
	"bytes"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/ast"
	"kame/lang/format"
	"kame/lang/script"
	"strings"
)

func TestSchemaTwoSemanticFields(t *testing.T) {
	a := t.Allocator()
	definitionLanguages := []string{"km", script.KameFileLanguage, "kash"}
	for _, lang := range definitionLanguages {
		buffer := bytes.Buffer{}
		if ast.WriteAST(&buffer, lang, "functions", "def f () = 1\ndef v = 1\n") != 0 || !strings.Contains(buffer.String(), "\"function\":true") || !strings.Contains(buffer.String(), "\"function\":false") {
			t.Error("schema-2 definition lost zero-parameter function/value distinction")
		}
		buffer.Reset()
	}
	patterns := "[./{name:~[a-z]+}, ./{name:*}]"
	for _, test := range []struct{ lang, text string }{
		{"expr", patterns},
		{"km", patterns},
		{"kash", "def p = " + patterns},
		{script.KameFileLanguage, "rule ./{name:~[a-z]+} ./{name:*} : ${PREP}, ${BUILD} ./in/{name:~[a-z]+}"},
		{"template", "@{nop " + patterns + "}"},
	} {
		formatted := format.Source(a, test.lang, "regex", test.text, "tabs", 4)
		if !formatted.OK {
			t.Error("regex source failed formatting")
		} else {
			buffer := bytes.Buffer{}
			if ast.WriteAST(&buffer, test.lang, "regex", formatted.Text) != 0 || !strings.Contains(buffer.String(), "\"regex\":true") || !strings.Contains(buffer.String(), "\"regex\":false") || !strings.Contains(buffer.String(), "\"schema\":2") {
				t.Error("formatted regex mode missing from schema-2 AST")
			}
			if test.lang == script.KameFileLanguage && (!strings.Contains(buffer.String(), "\"sequenceEnd\":true") || !strings.Contains(buffer.String(), "\"sequenceEnd\":false")) {
				t.Error("input sequence boundary missing from AST")
			}
			buffer.Reset()
		}
		mem.FreeString(a, formatted.Text)
		mem.FreeString(a, formatted.Code)
		mem.FreeString(a, formatted.Message)
	}
}

func TestValueASTPreservesCommentItems(t *testing.T) {
	buffer := bytes.Buffer{}
	defer buffer.Reset()
	if ast.WriteAST(&buffer, "km", "comments.km", "# before\nv = 1\n# after\n") != 0 || !strings.Contains(buffer.String(), "\"text\":\"# before\"") || !strings.Contains(buffer.String(), "\"text\":\"# after\"") {
		t.Error("value AST dropped authored comments")
	}
}

func TestRuleCapturePatternFieldAndAuthoredCoordinates(t *testing.T) {
	_ = t.Allocator()
	texts := []string{`rule "./out\t{name}.o"`, `rule ./out/{name}.o : ./in/{name}.c`, `rule "./out\"/{name:~[a-z]+}.o"`}
	for i := range texts {
		buffer := bytes.Buffer{}
		if ast.WriteAST(&buffer, script.KameFileLanguage, "captures", texts[i]) != 0 {
			t.Error("capture AST failed")
		}
		text := buffer.String()
		if !strings.Contains(text, `"pattern":{"kind":"target-template"`) {
			t.Error("capture AST lacks stable pattern field")
		}
		if i == 0 && (!strings.Contains(text, `"span":{"start":13,"end":19},"kind":"capture"`) || !strings.Contains(text, `"pattern":{"kind":"target-template","span":{"start":6,"end":21}`)) {
			t.Error("quoted target AST lost authored content or capture spans")
		}
		if i == 1 && strings.Count(text, `"pattern":{"kind":"target-template"`) != 2 || strings.Contains(text, `"template":{"kind":"target-template"`) {
			t.Error("capture fields should appear only as patterns")
		}
		if i == 2 && (!strings.Contains(text, `"span":{"start":14,"end":28},"kind":"capture"`) || !strings.Contains(text, `"regex":true`) || !strings.Contains(text, `"pattern":{"kind":"target-template","span":{"start":6,"end":30}`)) {
			t.Error("quoted regex target AST lost authored coordinates")
		}
		buffer.Reset()
	}
}
