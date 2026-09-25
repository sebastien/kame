package main

import (
	"bytes"
	"littlemake/program"
	"os"
	"solod.dev/so/io"
	"solod.dev/so/mem"
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

func TestPrimaryInvocationMaterializesInlineSource(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"-n", "-c", "task default :\n\techo ignored"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(errOut.String(), "[default] complete") {
		t.Errorf("primary invocation status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestPrimaryInvocationUsageAndTargetListing(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := Run([]string{"-j", "0"}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_VALUE_INVALID") {
		t.Errorf("invalid jobs status=%d stderr=%q", status, errOut.String())
	}
	out.Reset(); errOut.Reset()
	if status := Run([]string{"-n", "-c", "task first :\ntask second :"}, &input{}, &out, &errOut); status != 0 || out.String() != "first\nsecond\n" {
		t.Errorf("target listing status=%d stdout=%q", status, out.String())
	}
}

func TestFormatCheckAndStandardInput(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := Run([]string{"do", "fmt"}, &input{text: "value=42\n"}, &out, &errOut); status != 0 || out.String() != "value = 42\n" {
		t.Errorf("format stdin status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestPlanDoesNotMaterializeRecipe(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"do", "plan", "-c", "task build :\n\techo should-not-run", "build"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(out.String(), "\"type\":\"plan\"") || errOut.Len() != 0 {
		t.Errorf("plan status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestExpressionAndGraphInspection(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := Run([]string{"do", "expr", "-c", "(join [\"a\" \"b\"] \",\")"}, &input{}, &out, &errOut); status != 0 || out.String() != "a,b" {
		t.Errorf("expr status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset(); errOut.Reset()
	if status := Run([]string{"do", "inputs", "-c", "task build : input\n\techo ignored", "build"}, &input{}, &out, &errOut); status != 0 || out.String() != "[\"input\"]\n" {
		t.Errorf("inputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset(); errOut.Reset()
	graphSource := "task root : middle\n\techo root\ntask middle : leaf\n\techo middle\ntask leaf :\n\techo leaf"
	if status := Run([]string{"do", "inputs", "--depth", "2", "-c", graphSource, "root"}, &input{}, &out, &errOut); status != 0 || out.String() != "[\"middle\",\"leaf\"]\n" {
		t.Errorf("recursive inputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset(); errOut.Reset()
	if status := Run([]string{"do", "outputs", "--depth", "-1", "-c", graphSource, "root"}, &input{}, &out, &errOut); status != 0 || out.String() != "[\"root\",\"middle\",\"leaf\"]\n" {
		t.Errorf("recursive outputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset(); errOut.Reset()
	if status := Run([]string{"do", "expr", "-c", "(join @* \",\")", "--", "left", "right"}, &input{}, &out, &errOut); status != 0 || out.String() != "left,right" {
		t.Errorf("expr args status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestRunOptionsValidateAndReachRuntime(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"do", "run", "-n", "--timeout", "100", "--retry=1", "--log-limit", "8", "--env", "LM_TEST=value", "--shell", "/bin/sh", "--shell", "-c", "-c", "task default :\n\techo ignored", "default"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(errOut.String(), "[default] complete") {
		t.Errorf("run options status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	if status := Run([]string{"do", "run", "--env", "invalid", "-c", "task default :", "default"}, &input{}, &out, &errOut); status != 2 {
		t.Errorf("invalid env status=%d", status)
	}
}

func TestHelpAndVersion(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "Usage:"},
		{[]string{"-h"}, "Commands (littlemake do COMMAND):"},
		{[]string{"--version"}, "littlemake 0.1.0\n"},
		{[]string{"-V"}, "littlemake 0.1.0\n"},
		{[]string{"--version", "--help"}, "Usage:"},
	}
	for _, test := range tests {
		var out, errOut bytes.Buffer
		if status := Run(test.args, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), test.want) || errOut.Len() != 0 {
			t.Errorf("args=%q status=%d stdout=%q stderr=%q", test.args, status, out.String(), errOut.String())
		}
	}
}

func TestDoNamespaceHelp(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"do"}, "Commands:"},
		{[]string{"do", "--help"}, "Commands:"},
		{[]string{"do", "help"}, "Commands:"},
		{[]string{"do", "help", "run"}, "Usage: littlemake do run"},
		{[]string{"do", "plan", "--help"}, "Usage: littlemake do plan"},
		{[]string{"do", "expr", "-h"}, "Usage: littlemake do expr"},
	}
	for _, test := range tests {
		var out, errOut bytes.Buffer
		if status := Run(test.args, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), test.want) || errOut.Len() != 0 {
			t.Errorf("args=%q status=%d stdout=%q stderr=%q", test.args, status, out.String(), errOut.String())
		}
	}
}

func TestDoUnknownIncludesHint(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := Run([]string{"do", "bogus"}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "CMD_UNKNOWN") || !strings.Contains(errOut.String(), "littlemake do --help") {
		t.Errorf("unknown do status=%d stderr=%q", status, errOut.String())
	}
}

// The package directory has no build source, so a bare invocation exercises the
// discoverability overview instead of a bare BUILD_NO_SOURCE diagnostic.
func TestBareInvocationShowsOverviewWithoutSource(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := Run([]string{}, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), "Usage:") || errOut.Len() != 0 {
		t.Errorf("bare status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestHelpTokenAfterSeparatorIsNotHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := Run([]string{"--", "--help"}, &input{}, &out, &errOut); status != 1 || !strings.Contains(errOut.String(), "BUILD_NO_SOURCE") || strings.Contains(out.String(), "Usage:") {
		t.Errorf("separator status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestHelpTokenAsOptionValueIsNotHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	Run([]string{"do", "expr", "-c", "--help"}, &input{}, &out, &errOut)
	if strings.Contains(out.String(), "Usage:") || strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("option value requested help: stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestMergeEnvironmentOverridesExistingValue(t *testing.T) {
	values := []string{cloneCommandText("LM_CLI_TEST=old"), cloneCommandText("KEEP=value")}
	values = mergeEnvironment(values, []string{"LM_CLI_TEST=new"})
	defer func() { for i := range values { mem.FreeString(mem.System, values[i]) } }()
	if values[0] != "LM_CLI_TEST=new" || values[1] != "KEEP=value" {
		t.Errorf("environment = %q", values)
	}
}

func TestJSONEventEncodesBinaryChunk(t *testing.T) {
	var out bytes.Buffer
	writeJSONEvent(&out, program.Event{Kind: program.Stdout, Target: "build", Data: []byte{0xff, 0x00}})
	want := "{\"schema\":1,\"type\":\"stdout\",\"target\":\"build\",\"node\":0,\"generation\":0,\"attempt\":0,\"data\":\"/wA=\",\"encoding\":\"base64\"}\n"
	if out.String() != want { t.Errorf("JSON event = %q, want %q", out.String(), want) }
}

func TestEmptyLongOptionValuesAreInvalid(t *testing.T) {
	for _, arg := range []string{"--file=", "--command=", "--jobs=", "--env=", "--timeout="} {
		var out, errOut bytes.Buffer
		if status := Run([]string{arg}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_VALUE_INVALID") {
			t.Errorf("%s status=%d stderr=%q", arg, status, errOut.String())
		}
	}
}

// JSON diagnostics are emitted on stdout; stderr stays unused after parsing.
func TestJSONDiagnosticUsesStdout(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"do", "plan", "--json", "-c", "task build :", "missing"}, &input{}, &out, &errOut)
	if status != 1 || !strings.Contains(out.String(), "\"type\":\"diagnostic\"") || !strings.Contains(out.String(), "\"code\":\"TGT_NO_RULE\"") || errOut.Len() != 0 {
		t.Errorf("json diagnostic status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestUnknownPathTargetSuggestsExplicitPrefix(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"do", "plan", "-c", "task build :", "missing.txt"}, &input{}, &out, &errOut)
	if status != 1 || !strings.Contains(errOut.String(), "note: did you mean ./missing.txt?") {
		t.Errorf("suggestion status=%d stderr=%q", status, errOut.String())
	}
}
