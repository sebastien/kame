package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	// The partial AST error envelope is checked directly in compiled native
	// code; the public CLI intentionally emits a diagnostic event on failure.
	tests := []struct{ lang, name string }{{"expr", "expr"}, {"template", "template"}, {"rule", "rule"}, {"script", "script"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, readErr := os.ReadFile("testdata/parse/" + test.name + ".km")
			if readErr != nil {
				t.Fatal(readErr)
			}
			want, readErr := os.ReadFile("testdata/parse/" + test.name + ".json")
			if readErr != nil {
				t.Fatal(readErr)
			}
			var out bytes.Buffer
			status := nativeWriteAST(t, &out, test.lang, "testdata/parse/"+test.name+".km", string(text))
			if status != 0 || out.String() != string(want) {
				t.Errorf("parse %s: status=%d output=%q", test.name, status, out.String())
			}
		})
	}
}

func TestParseDiagnosticsAndUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"do", "parse", "--lang", "expr"}, &input{text: "["}, &out, &errOut); status != 1 || !strings.Contains(errOut.String(), "PARSE_ERR") || out.Len() != 0 {
		t.Errorf("invalid source: status=%d output=%q", status, out.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "parse"}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_NO_VALUE") {
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
		if status := runNativeCLI(t, test.args, &input{}, &out, &errOut); status != test.status || !strings.Contains(errOut.String(), test.code) {
			t.Errorf("args=%q status=%d stderr=%q", test.args, status, errOut.String())
		}
	}
}

func TestParseASTPreservesTemplateAndExpressionSpans(t *testing.T) {
	tests := []struct {
		lang, text, want string
	}{
		{"rule", "\"./{x}\" : input", "\"span\":{\"start\":3,\"end\":6}"},
		{"script", "value = \"\"", "\"expression\":{\"kind\":\"string\",\"span\":{\"start\":8,\"end\":10},\"parts\":[]}"},
	}
	for _, test := range tests {
		t.Run(test.lang, func(t *testing.T) {
			var out bytes.Buffer
			if status := nativeWriteAST(t, &out, test.lang, "test.km", test.text); status != 0 || !strings.Contains(out.String(), test.want) {
				t.Errorf("parse %s: status=%d output=%q", test.lang, status, out.String())
			}
		})
	}
}

func TestPrimaryInvocationMaterializesInlineSource(t *testing.T) {
	var out, errOut bytes.Buffer
	status := runNativeCLI(t, []string{"-n", "-l", "kmk", "-c", "task default :\n\techo ignored"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(errOut.String(), "[default]") || !strings.Contains(errOut.String(), " - ✓") {
		t.Errorf("primary invocation status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestPrimaryInvocationUsageAndMissingDefault(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"-j", "0"}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_VALUE_INVALID") {
		t.Errorf("invalid jobs status=%d stderr=%q", status, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"-n", "-l", "kmk", "-c", "task first :\ntask second :"}, &input{}, &out, &errOut); status != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "TGT_NO_DEFAULT") || !strings.Contains(errOut.String(), "available targets: first, second") {
		t.Errorf("missing default status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

// Every target-taking command shares the primary invocation's selection: an
// empty target list becomes "default" when one is defined, and otherwise fails
// with TGT_NO_DEFAULT and the available targets.
func TestTargetTakingCommandsSelectDefault(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"do", "plan", "--json", "-c", "task default : ./dep\n\techo ignored\ntask dep :\n\techo ignored"}, `"target":"default"`},
		{[]string{"do", "cat", "-c", `default = "value"`}, "value"},
		{[]string{"do", "inputs", "--json", "-c", "task default : dep\n\techo ignored\ntask dep :\n\techo ignored"}, `"display":"dep"`},
	}
	for _, test := range tests {
		var out, errOut bytes.Buffer
		if status := runNativeCLI(t, test.args, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), test.want) || errOut.Len() != 0 {
			t.Errorf("args=%q status=%d stdout=%q stderr=%q", test.args, status, out.String(), errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"do", "plan", "--json", "-c", "task first :"}, &input{}, &out, &errOut); status != 1 || errOut.Len() != 0 || !strings.Contains(out.String(), `"code":"TGT_NO_DEFAULT"`) || !strings.Contains(out.String(), `"notes":["available targets: first"]`) {
		t.Errorf("json missing default status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestFormatCheckAndStandardInput(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"do", "fmt"}, &input{text: "value=42\n"}, &out, &errOut); status != 0 || out.String() != "value = 42\n" {
		t.Errorf("format stdin status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "fmt", "--lang", "template", "--comment", "plain"}, &input{text: "hello @if(:true)yes@end world"}, &out, &errOut); status != 0 || out.String() != "hello @if(:true)yes@end(if) world" || errOut.Len() != 0 {
		t.Errorf("template format stdin status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "fmt", "--lang", "template", "--comment", "plain"}, &input{text: "ok\n@end(if)"}, &out, &errOut); status != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "<stdin>:2:1: error TPL_BLOCK:") {
		t.Errorf("template format diagnostic status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestPlanDoesNotMaterializeRecipe(t *testing.T) {
	var out, errOut bytes.Buffer
	status := runNativeCLI(t, []string{"do", "plan", "--json", "-c", "task build :\n\techo should-not-run", "build"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(out.String(), "\"type\":\"plan\"") || errOut.Len() != 0 {
		t.Errorf("plan status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestExpressionAndGraphInspection(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"do", "run", "--lang", "expr", "-c", "(join [\"a\" \"b\"] \",\")"}, &input{}, &out, &errOut); status != 0 || out.String() != "\"a,b\"" {
		t.Errorf("expr status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "inputs", "--json", "-c", "task build : ./input\n\techo ignored", "build"}, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), `"display":"./input"`) {
		t.Errorf("inputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	graphSource := "task root : middle\n\techo root\ntask middle : leaf\n\techo middle\ntask leaf :\n\techo leaf"
	if status := runNativeCLI(t, []string{"do", "inputs", "--json", "--depth", "2", "-c", graphSource, "root"}, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), `"items":[2,3]`) {
		t.Errorf("recursive inputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "outputs", "--json", "--depth", "-1", "-c", graphSource, "root"}, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), `"items":[]`) {
		t.Errorf("recursive outputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "run", "--lang", "expr", "-c", "(join @* \",\")", "--", "left", "right"}, &input{}, &out, &errOut); status != 0 || out.String() != "\"left,right\"" {
		t.Errorf("expr args status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "run", "--lang", "expr", "-c", "(let [a 10] (out! \"hello\" a))"}, &input{}, &out, &errOut); status != 0 || out.String() != "hello10\"hello10\"" || errOut.Len() != 0 {
		t.Errorf("expr out status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "run", "--lang", "expr", "-c", "(err! \"notice\")"}, &input{}, &out, &errOut); status != 0 || out.String() != "\"notice\"" || errOut.String() != "notice" {
		t.Errorf("expr err status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := runNativeCLI(t, []string{"do", "run", "--lang", "expr", "-c", "(yield! \"generated\")"}, &input{}, &out, &errOut); status != 0 || out.String() != "generated:nil" || errOut.Len() != 0 {
		t.Errorf("expr yield status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestToolsCommandListsGloballyReferencedTools(t *testing.T) {
	var out, errOut bytes.Buffer
	source := "task build :\n\t@(x/sh) -c 'true'\n"
	status := runNativeCLI(t, []string{"do", "tools", "--json", "-c", source}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(out.String(), `"name":"sh"`) || !strings.Contains(out.String(), `"path":"`) {
		t.Errorf("tools status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestBuildOptionsValidateAndReachRuntime(t *testing.T) {
	var out, errOut bytes.Buffer
	status := runNativeCLI(t, []string{"-n", "-l", "kmk", "--timeout", "100", "--retry=1", "--log-limit", "8", "--env", "KM_TEST=value", "--shell", "/bin/sh", "--shell", "-c", "-c", "task default :\n\techo ignored", "default"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(errOut.String(), "[default]") || !strings.Contains(errOut.String(), " - ✓") {
		t.Errorf("run options status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	if status := runNativeCLI(t, []string{"--env", "invalid", "-c", "task default :", "default"}, &input{}, &out, &errOut); status != 2 {
		t.Errorf("invalid env status=%d", status)
	}
}

func TestHelpAndVersion(t *testing.T) {
	var metadata, metadataErrors bytes.Buffer
	if status := runNativeCLI(t, []string{"--json", "--version"}, &input{}, &metadata, &metadataErrors); status != 0 || metadataErrors.Len() != 0 {
		t.Fatalf("compiled version metadata: status=%d errors=%q", status, metadataErrors.String())
	}
	var record struct {
		Schema                                                int
		Type, Version, BuildID, BuildTime, BuildMode, Runtime string
	}
	if err := json.Unmarshal(metadata.Bytes(), &record); err != nil || record.Schema != 1 || record.Type != "version" || record.Version != version || record.BuildID != buildID || record.BuildTime != buildTime {
		t.Fatalf("compiled metadata disagrees with generated version: %s (%v)", metadata.String(), err)
	}
	if (record.BuildMode != "debug" && record.BuildMode != "sanitize" && record.BuildMode != "release") || (record.Runtime != "C" && record.Runtime != "APE") {
		t.Fatalf("unexpected compiled build/runtime: %s", metadata.String())
	}
	wantVersion := fmt.Sprintf("kame %s (%s; %s; %s; %s)\n", record.Version, record.BuildID, record.BuildTime, record.BuildMode, record.Runtime)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "Usage:"},
		{[]string{"-h"}, "Commands:"},
		{[]string{"--version"}, wantVersion},
		{[]string{"-V"}, wantVersion},
		{[]string{"--version", "--help"}, "Usage:"},
		{[]string{"--help", "--version"}, "Usage:"},
		{[]string{"-c", "--help", "--version"}, "kame " + version + " ("},
		{[]string{"-c", "--version", "--help"}, "Usage:"},
	}
	for _, test := range tests {
		var out, errOut bytes.Buffer
		if status := runNativeCLI(t, test.args, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), test.want) || errOut.Len() != 0 {
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
		{[]string{"do", "plan", "--help"}, "Usage: kame do plan"},
		{[]string{"do", "run", "--lang", "expr", "-h"}, "Usage: kame do run"},
		{[]string{"do", "bogus", "--help"}, "Commands:"},
	}
	for _, test := range tests {
		var out, errOut bytes.Buffer
		if status := runNativeCLI(t, test.args, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), test.want) || errOut.Len() != 0 {
			t.Errorf("args=%q status=%d stdout=%q stderr=%q", test.args, status, out.String(), errOut.String())
		}
	}
}

func TestDoUnknownIncludesHint(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"do", "bogus"}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "CMD_UNKNOWN") || !strings.Contains(errOut.String(), "kame do --help") {
		t.Errorf("unknown do status=%d stderr=%q", status, errOut.String())
	}
}

// The package directory has no build source, so a bare invocation exercises the
// discoverability overview instead of a bare BUILD_NO_SOURCE diagnostic.
func TestBareInvocationShowsOverviewWithoutSource(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{}, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), "Usage:") || errOut.Len() != 0 {
		t.Errorf("bare status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestHelpTokenAfterSeparatorIsNotHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"--", "--help"}, &input{}, &out, &errOut); status != 1 || !strings.Contains(errOut.String(), "BUILD_NO_SOURCE") || strings.Contains(out.String(), "Usage:") {
		t.Errorf("separator status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestHelpTokenAsOptionValueIsNotHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	runNativeCLI(t, []string{"do", "run", "--lang", "expr", "-c", "--help"}, &input{}, &out, &errOut)
	if strings.Contains(out.String(), "Usage:") || strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("option value requested help: stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestEmptyLongOptionValuesAreInvalid(t *testing.T) {
	for _, arg := range []string{"--file=", "--jobs=", "--env=", "--timeout="} {
		var out, errOut bytes.Buffer
		if status := runNativeCLI(t, []string{arg}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_VALUE_INVALID") {
			t.Errorf("%s status=%d stderr=%q", arg, status, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"--command="}, &input{}, &out, &errOut); status != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("empty runner source status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

// JSON diagnostics are emitted on stdout; stderr stays unused after parsing.
func TestJSONDiagnosticUsesStdout(t *testing.T) {
	var out, errOut bytes.Buffer
	status := runNativeCLI(t, []string{"do", "plan", "--json", "-c", "task build :", "missing"}, &input{}, &out, &errOut)
	if status != 1 || !strings.Contains(out.String(), "\"type\":\"diagnostic\"") || !strings.Contains(out.String(), "\"code\":\"TGT_NO_RULE\"") || errOut.Len() != 0 {
		t.Errorf("json diagnostic status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestSpanExpandReportsExpressionInputs(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	source := "SOURCE = ./input\ntask build : @(SOURCE)\n\ttrue\n"
	if status := runNativeCLI(t, []string{"do", "span", "--json", "--expand", "-c", source, "build"}, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), `"static":{"inputs":[],"outputs":["build"]}`) || !strings.Contains(out.String(), `"dynamic":["./input"]`) || !strings.Contains(out.String(), `"expanded":true`) {
		t.Errorf("expanded span status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestSourceDiagnosticRendersLocationAndExcerpt(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"-c", "value = (\n"}, &input{}, &out, &errOut); status != 1 || !strings.Contains(errOut.String(), "<command:1>:1:9: error PARSE_ERR:") || !strings.Contains(errOut.String(), "value = (") || !strings.Contains(errOut.String(), "        ^") {
		t.Errorf("source diagnostic status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestZeroWidthSourceDiagnosticRendersEOFLocation(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	if status := runNativeCLI(t, []string{"-c", "\n\nvalue ="}, &input{}, &out, &errOut); status != 1 || !strings.Contains(errOut.String(), "<command:1>:3:8: error PARSE_ERR:") || !strings.Contains(errOut.String(), "value =") {
		t.Errorf("zero-width source diagnostic status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestInspectionPathOperandAcceptsExternalFile(t *testing.T) {
	var out, errOut bytes.Buffer
	status := runNativeCLI(t, []string{"do", "plan", "-c", "task build :", "missing.txt"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(out.String(), "./missing.txt") || errOut.Len() != 0 {
		t.Errorf("external file status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestColorAlwaysIsExplicitAndColorNeverStaysPlain(t *testing.T) {
	var out, errOut bytes.Buffer
	status := runNativeCLI(t, []string{"--diagnostic-format", "human", "--color", "always", "-c", "task build :", "missing"}, &input{}, &out, &errOut)
	if status != 1 || !strings.Contains(errOut.String(), "\x1b[") {
		t.Errorf("always color status=%d stderr=%q", status, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	status = runNativeCLI(t, []string{"--color", "never", "-c", "task build :", "missing"}, &input{}, &out, &errOut)
	if status != 1 || strings.Contains(errOut.String(), "\x1b[") {
		t.Errorf("never color status=%d stderr=%q", status, errOut.String())
	}
}

func TestDiagnosticPresentationEnvironmentAndJSONEarlyErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	status := runNativeCLI(t, []string{"--json", "--unknown"}, &input{}, &out, &errOut)
	if status != 2 || errOut.Len() != 0 || !strings.Contains(out.String(), `"code":"OPT_UNKNOWN"`) {
		t.Errorf("json early option failure status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	status = runNativeCLI(t, []string{"--json", "--color", "wrong"}, &input{}, &out, &errOut)
	if status != 2 || errOut.Len() != 0 || !strings.Contains(out.String(), `"code":"OPT_VALUE_INVALID"`) {
		t.Errorf("json global option failure status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

// Repeated black-box calls preserve command-result coverage. The compiled
// native lifecycle gate separately repeats Run in one process with allocation
// tracking and sanitizers; no Solod code executes in this Go driver.
func TestRepeatedNativeCLICommandResults(t *testing.T) {
	tests := []struct {
		args   []string
		status int
	}{
		{[]string{"-c", "task first :\ntask second :"}, 1},
		{[]string{"do", "run", "--lang", "expr", "--allow-read=/tmp", "--allow-env=HOME", "-c", "(nop 42)", "--", "left", "right"}, 0},
		{[]string{"do", "plan", "-c", "task build :\n\techo ignored", "build"}, 0},
		{[]string{"do", "span", "--expand", "-c", "SOURCE = ./input\ntask build : @(SOURCE)\n\ttrue", "build"}, 0},
		{[]string{"do", "fmt", "--lang", "expr"}, 0},
		{[]string{"--shell", "/bin/sh", "--env", "KM_TEST=value", "--unknown"}, 2},
		{[]string{"do", "run", "--lang", "expr", "--allow-read=/tmp", "--allow-env=HOME", "--unknown"}, 2},
		{[]string{"do", "fmt", "-i", "-n", "source.kmk"}, 2},
		{[]string{"do", "inputs", "build", "--depth", "invalid"}, 2},
	}
	for iteration := 0; iteration < 64; iteration++ {
		for _, test := range tests {
			var out, errOut bytes.Buffer
			if status := runNativeCLI(t, test.args, &input{text: "42\n"}, &out, &errOut); status != test.status {
				t.Fatalf("iteration %d args=%q status=%d want=%d stdout=%q stderr=%q", iteration, test.args, status, test.status, out.String(), errOut.String())
			}
		}
	}
}
