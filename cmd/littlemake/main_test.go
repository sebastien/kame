package main

import (
	"bytes"
	"os"
	"solod.dev/so/io"
	"strings"
	"testing"
)

type input struct {
	text string
	pos  int
}

func (in *input) Read(out []byte) (int, error) {
	if in.pos == len(in.text) {
		return 0, io.EOF
	}
	n := copy(out, in.text[in.pos:])
	in.pos += n
	return n, nil
}

func TestParseASTGoldens(t *testing.T) {
	tests := []struct{ lang, name string }{{"expr", "expr"}, {"template", "template"}, {"rule", "rule"}, {"script", "script"}, {"expr", "invalid-expr"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, readErr := os.ReadFile("testdata/parse/" + test.name + ".lm")
			if readErr != nil {
				t.Fatal(readErr)
			}
			want, readErr := os.ReadFile("testdata/parse/" + test.name + ".json")
			if readErr != nil {
				t.Fatal(readErr)
			}
			var out bytes.Buffer
			status := writeAST(&out, test.lang, "testdata/parse/"+test.name+".lm", string(text))
			wantStatus := 0
			if test.name == "invalid-expr" {
				wantStatus = 1
			}
			if status != wantStatus || out.String() != string(want) {
				t.Errorf("parse %s: status=%d output=%q", test.name, status, out.String())
			}
		})
	}
}

func TestParseDiagnosticsAndUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := Run([]string{"do", "parse", "--lang", "expr"}, &input{text: "["}, &out, &errOut); status != 1 || !strings.Contains(out.String(), "PARSE_ERR") {
		t.Errorf("invalid source: status=%d output=%q", status, out.String())
	}
	out.Reset()
	errOut.Reset()
	if status := Run([]string{"do", "parse"}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_NO_VALUE") {
		t.Errorf("missing language: status=%d stderr=%q", status, errOut.String())
	}
}

func TestParseCLIUsageErrors(t *testing.T) {
	tests := []struct {
		args   []string
		code   string
		status int
	}{
		{[]string{"do", "parse", "--lang", "unknown"}, "OPT_VALUE_INVALID", 2},
		{[]string{"do", "parse", "--unknown"}, "OPT_UNKNOWN", 2},
	}
	for _, test := range tests {
		var out, errOut bytes.Buffer
		if status := Run(test.args, &input{}, &out, &errOut); status != test.status || !strings.Contains(errOut.String(), test.code) {
			t.Errorf("args=%q status=%d stderr=%q", test.args, status, errOut.String())
		}
	}
}

func TestParseASTPreservesNestedTemplateSpans(t *testing.T) {
	tests := []struct {
		lang, text, want string
	}{
		{"rule", "\"./{x}\" : input", "\"span\":{\"start\":3,\"end\":6}"},
		{"script", "value = \"\"", "\"template\":{\"kind\":\"template\",\"span\":{\"start\":9,\"end\":9},\"parts\":[]}"},
	}
	for _, test := range tests {
		t.Run(test.lang, func(t *testing.T) {
			var out bytes.Buffer
			if status := writeAST(&out, test.lang, "test.lm", test.text); status != 0 || !strings.Contains(out.String(), test.want) {
				t.Errorf("parse %s: status=%d output=%q", test.lang, status, out.String())
			}
		})
	}
}
