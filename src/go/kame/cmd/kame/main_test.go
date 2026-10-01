package main

import (
	"bytes"
	"kame/core"
	"kame/diagnostic"
	"kame/lang/source"
	"kame/program"
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

// fixedWriter has fixed storage so the emergency renderer test does not rely
// on an allocator-backed bytes.Buffer.
type fixedWriter struct {
	data [256]byte
	n    int
}

func (w *fixedWriter) Write(data []byte) (int, error) {
	n := copy(w.data[w.n:], data)
	w.n += n
	return n, nil
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
			text, readErr := os.ReadFile("testdata/parse/" + test.name + ".km")
			if readErr != nil {
				t.Fatal(readErr)
			}
			want, readErr := os.ReadFile("testdata/parse/" + test.name + ".json")
			if readErr != nil {
				t.Fatal(readErr)
			}
			var out bytes.Buffer
			status := writeAST(&out, test.lang, "testdata/parse/"+test.name+".km", string(text))
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
			if status := writeAST(&out, test.lang, "test.km", test.text); status != 0 || !strings.Contains(out.String(), test.want) {
				t.Errorf("parse %s: status=%d output=%q", test.lang, status, out.String())
			}
		})
	}
}

func TestPrimaryInvocationMaterializesInlineSource(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"-n", "-l", "kmk", "-c", "task default :\n\techo ignored"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(errOut.String(), "[default] complete") {
		t.Errorf("primary invocation status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestPrimaryInvocationUsageAndMissingDefault(t *testing.T) {
	var out, errOut bytes.Buffer
	if status := Run([]string{"-j", "0"}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_VALUE_INVALID") {
		t.Errorf("invalid jobs status=%d stderr=%q", status, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := Run([]string{"-n", "-l", "kmk", "-c", "task first :\ntask second :"}, &input{}, &out, &errOut); status != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "TGT_NO_DEFAULT") || !strings.Contains(errOut.String(), "available targets: first, second") {
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
		{[]string{"do", "plan", "-c", "task default : ./dep\n\techo ignored\ntask dep :\n\techo ignored"}, `"target":"default"`},
		{[]string{"do", "cat", "-c", `default = "value"`}, "value"},
		{[]string{"do", "inputs", "-c", "task default : dep\n\techo ignored\ntask dep :\n\techo ignored"}, `["dep"]`},
	}
	for _, test := range tests {
		var out, errOut bytes.Buffer
		if status := Run(test.args, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), test.want) || errOut.Len() != 0 {
			t.Errorf("args=%q status=%d stdout=%q stderr=%q", test.args, status, out.String(), errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if status := Run([]string{"do", "plan", "--json", "-c", "task first :"}, &input{}, &out, &errOut); status != 1 || errOut.Len() != 0 || !strings.Contains(out.String(), `"code":"TGT_NO_DEFAULT"`) || !strings.Contains(out.String(), `"notes":["available targets: first"]`) {
		t.Errorf("json missing default status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
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
	if status := Run([]string{"do", "expr", "-c", "(join [\"a\" \"b\"] \",\")"}, &input{}, &out, &errOut); status != 0 || out.String() != "\"a,b\"" {
		t.Errorf("expr status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := Run([]string{"do", "inputs", "-c", "task build : input\n\techo ignored", "build"}, &input{}, &out, &errOut); status != 0 || out.String() != "[\"input\"]\n" {
		t.Errorf("inputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	graphSource := "task root : middle\n\techo root\ntask middle : leaf\n\techo middle\ntask leaf :\n\techo leaf"
	if status := Run([]string{"do", "inputs", "--depth", "2", "-c", graphSource, "root"}, &input{}, &out, &errOut); status != 0 || out.String() != "[\"middle\",\"leaf\"]\n" {
		t.Errorf("recursive inputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := Run([]string{"do", "outputs", "--depth", "-1", "-c", graphSource, "root"}, &input{}, &out, &errOut); status != 0 || out.String() != "[\"root\",\"middle\",\"leaf\"]\n" {
		t.Errorf("recursive outputs status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := Run([]string{"do", "expr", "-c", "(join @* \",\")", "--", "left", "right"}, &input{}, &out, &errOut); status != 0 || out.String() != "\"left,right\"" {
		t.Errorf("expr args status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := Run([]string{"do", "expr", "-c", "(let [a 10] (out \"hello\" a))"}, &input{}, &out, &errOut); status != 0 || out.String() != "hello10\"hello10\"" || errOut.Len() != 0 {
		t.Errorf("expr out status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := Run([]string{"do", "expr", "-c", "(err \"notice\")"}, &input{}, &out, &errOut); status != 0 || out.String() != "\"notice\"" || errOut.String() != "notice" {
		t.Errorf("expr err status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if status := Run([]string{"do", "expr", "-c", "(yield \"generated\")"}, &input{}, &out, &errOut); status != 0 || out.String() != "generated:nil" || errOut.Len() != 0 {
		t.Errorf("expr yield status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestToolsCommandListsGloballyReferencedTools(t *testing.T) {
	var out, errOut bytes.Buffer
	source := "task build :\n\t@(x/sh) -c 'true'\n"
	status := Run([]string{"do", "tools", "-c", source}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(out.String(), `"name":"sh"`) || !strings.Contains(out.String(), `"path":"`) {
		t.Errorf("tools status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestBuildOptionsValidateAndReachRuntime(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"-n", "-l", "kmk", "--timeout", "100", "--retry=1", "--log-limit", "8", "--env", "KM_TEST=value", "--shell", "/bin/sh", "--shell", "-c", "-c", "task default :\n\techo ignored", "default"}, &input{}, &out, &errOut)
	if status != 0 || !strings.Contains(errOut.String(), "[default] complete") {
		t.Errorf("run options status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	if status := Run([]string{"--env", "invalid", "-c", "task default :", "default"}, &input{}, &out, &errOut); status != 2 {
		t.Errorf("invalid env status=%d", status)
	}
}

func TestHelpAndVersion(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "Usage:"},
		{[]string{"-h"}, "Commands (kame do COMMAND):"},
		{[]string{"--version"}, "kame " + version + " (" + buildID + "; " + buildTime + "; " + buildMode + ")\n"},
		{[]string{"-V"}, "kame " + version + " (" + buildID + "; " + buildTime + "; " + buildMode + ")\n"},
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
		{[]string{"do", "plan", "--help"}, "Usage: kame do plan"},
		{[]string{"do", "expr", "-h"}, "Usage: kame do expr"},
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
	if status := Run([]string{"do", "bogus"}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "CMD_UNKNOWN") || !strings.Contains(errOut.String(), "kame do --help") {
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
	values := []string{cloneCommandText("KM_CLI_TEST=old"), cloneCommandText("KEEP=value")}
	values = mergeEnvironment(values, []string{"KM_CLI_TEST=new"})
	defer func() {
		for i := range values {
			mem.FreeString(mem.System, values[i])
		}
	}()
	if values[0] != "KM_CLI_TEST=new" || values[1] != "KEEP=value" {
		t.Errorf("environment = %q", values)
	}
}

func TestJSONEventEncodesBinaryChunk(t *testing.T) {
	var out bytes.Buffer
	writeJSONEvent(&out, program.Event{Kind: program.Stdout, Target: "build", Data: []byte{0xff, 0x00}})
	want := "{\"schema\":1,\"type\":\"stdout\",\"target\":\"build\",\"node\":0,\"generation\":0,\"attempt\":0,\"data\":\"/wA=\",\"encoding\":\"base64\"}\n"
	if out.String() != want {
		t.Errorf("JSON event = %q, want %q", out.String(), want)
	}
}

func TestJSONEventIncludesStructuredRuntimeContext(t *testing.T) {
	var out bytes.Buffer
	writeJSONEvent(&out, program.Event{Kind: program.DependencyDiscovered, Target: "build", Key: core.ResourceKey{Kind: core.ResourceTask, Name: "build"}, DependencyID: 9, DependencyKey: core.ResourceKey{Kind: core.ResourceFile, Name: "./input"}})
	text := out.String()
	for _, field := range []string{`"resource":{"kind":"task","name":"build"}`, `"dependency":{"node":9,"resource":{"kind":"file","name":"./input"}}`} {
		if !strings.Contains(text, field) { t.Errorf("JSON event missing %s: %s", field, text) }
	}
	out.Reset()
	writeJSONEvent(&out, program.Event{Kind: program.TargetValue, Target: "result", Value: core.Value{Kind: core.String, Text: "value"}})
	if !strings.Contains(out.String(), `"value":{"kind":"string","data":"value"}`) { t.Errorf("JSON target value = %s", out.String()) }
}

func TestEmptyLongOptionValuesAreInvalid(t *testing.T) {
	for _, arg := range []string{"--file=", "--jobs=", "--env=", "--timeout="} {
		var out, errOut bytes.Buffer
		if status := Run([]string{arg}, &input{}, &out, &errOut); status != 2 || !strings.Contains(errOut.String(), "OPT_VALUE_INVALID") {
			t.Errorf("%s status=%d stderr=%q", arg, status, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if status := Run([]string{"--command="}, &input{}, &out, &errOut); status != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("empty runner source status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
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

func TestJSONDiagnosticRetainsNotesFramesAndSeverity(t *testing.T) {
	var out bytes.Buffer
	writeJSONDiagnostic(&out, diagnostic.Diagnostic{Code: "CACHE_UNUSABLE", Severity: diagnostic.Warning, Message: "cache record ignored", Notes: []string{"rebuild scheduled"}, Frames: []diagnostic.Frame{{Label: "rule", Span: diagnostic.Span{Start: 3, End: 7}}}})
	text := out.String()
	if !strings.Contains(text, `"severity":"warning"`) || !strings.Contains(text, `"notes":["rebuild scheduled"]`) || !strings.Contains(text, `"frames":[{"label":"rule"}]`) {
		t.Errorf("JSON diagnostic omitted metadata: %s", text)
	}
}

func TestSpanExpandReportsExpressionInputs(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	source := "SOURCE = ./input\ntask build : @(SOURCE)\n\ttrue\n"
	if status := Run([]string{"do", "span", "--expand", "-c", source, "build"}, &input{}, &out, &errOut); status != 0 || !strings.Contains(out.String(), `"static":{"inputs":[],"outputs":["build"]}`) || !strings.Contains(out.String(), `"dynamic":["./input"]`) || !strings.Contains(out.String(), `"expanded":true`) {
		t.Errorf("expanded span status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestSourceDiagnosticRendersLocationAndExcerpt(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	if status := Run([]string{"-c", "value = (\n"}, &input{}, &out, &errOut); status != 1 || !strings.Contains(errOut.String(), "<command:1>:1:9: error PARSE_ERR:") || !strings.Contains(errOut.String(), "value = (") || !strings.Contains(errOut.String(), "        ^") {
		t.Errorf("source diagnostic status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestZeroWidthSourceDiagnosticRendersEOFLocation(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	if status := Run([]string{"-c", "\n\nvalue ="}, &input{}, &out, &errOut); status != 1 || !strings.Contains(errOut.String(), "<command:1>:3:8: error PARSE_ERR:") || !strings.Contains(errOut.String(), "value =") {
		t.Errorf("zero-width source diagnostic status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestUnknownPathTargetSuggestsExplicitPrefix(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"do", "plan", "-c", "task build :", "missing.txt"}, &input{}, &out, &errOut)
	if status != 1 || !strings.Contains(errOut.String(), "note: did you mean ./missing.txt?") {
		t.Errorf("suggestion status=%d stderr=%q", status, errOut.String())
	}
}

func TestDiagnosticJSONCarriesStructuredContext(t *testing.T) {
	var out bytes.Buffer
	d := diagnostic.Diagnostic{Code: "RECIPE_FAIL", Severity: diagnostic.Error, Message: "recipe exited unsuccessfully", Source: "Makefile.kmk", Span: diagnostic.Span{Start: 4, End: 7}, Related: []diagnostic.Related{{Message: "declared here", Source: "Makefile.kmk", Span: diagnostic.Span{Start: 0, End: 3}}}, Frames: []diagnostic.Frame{{Kind: "rule", Label: "build", Source: "Makefile.kmk", Span: diagnostic.Span{Start: 0, End: 7}}}, Target: "build", TargetStack: []string{"all", "build"}, Tips: []string{"check the compiler"}, Cause: diagnostic.Cause{Kind: "process", Message: "shell exited", Program: "sh", Status: 127, HasStatus: true, StderrTruncated: true, StderrLimit: 64}}
	writeJSONDiagnostic(&out, d)
	text := out.String()
	for _, field := range []string{`"related"`, `"kind":"rule"`, `"targetStack":["all","build"]`, `"tips":["check the compiler"]`, `"cause":{"kind":"process"`, `"status":127`, `"stderrTruncated":true`, `"stderrLimit":64`} {
		if !strings.Contains(text, field) {
			t.Errorf("JSON diagnostic missing %s: %s", field, text)
		}
	}
}

func TestDiagnosticRendererUsesDisplayCellsAndNoSyntheticLocation(t *testing.T) {
	src := source.New(mem.System, "Makefile.kmk", "\t界e\u0301 bad\n")
	defer src.Free(mem.System)
	var out bytes.Buffer
	cliDiagnosticWithSource(&out, diagnostic.Diagnostic{Source: "Makefile.kmk", Code: "PARSE_ERR", Severity: diagnostic.Error, Message: "bad token", Span: diagnostic.Span{Start: 4, End: 8}}, src)
	if !strings.Contains(out.String(), "Makefile.kmk:1:11:") || !strings.Contains(out.String(), "^~") {
		t.Errorf("unicode diagnostic = %q", out.String())
	}
	out.Reset()
	cliDiagnosticWithSource(&out, diagnostic.Diagnostic{Code: "OPT_UNKNOWN", Severity: diagnostic.Error, Message: "unknown option"}, src)
	if strings.Contains(out.String(), "<command>") || !strings.Contains(out.String(), "error OPT_UNKNOWN") {
		t.Errorf("synthetic location = %q", out.String())
	}
}

func TestColorAlwaysIsExplicitAndColorNeverStaysPlain(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"--diagnostic-format", "human", "--color", "always", "-c", "task build :", "missing"}, &input{}, &out, &errOut)
	if status != 1 || !strings.Contains(errOut.String(), "\x1b[") {
		t.Errorf("always color status=%d stderr=%q", status, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	status = Run([]string{"--color", "never", "-c", "task build :", "missing"}, &input{}, &out, &errOut)
	if status != 1 || strings.Contains(errOut.String(), "\x1b[") {
		t.Errorf("never color status=%d stderr=%q", status, errOut.String())
	}
}

func TestDiagnosticPresentationEnvironmentAndJSONEarlyErrors(t *testing.T) {
	if resolveDiagnosticColor("auto", "human", true, []string{"NO_COLOR=1"}) != "never" || resolveDiagnosticColor("auto", "human", false, []string{"CLICOLOR_FORCE=1"}) != "always" || resolveDiagnosticColor("always", "plain", true, nil) != "never" {
		t.Error("color environment resolution was not deterministic")
	}
	var out, errOut bytes.Buffer
	status := Run([]string{"--json", "--unknown"}, &input{}, &out, &errOut)
	if status != 2 || errOut.Len() != 0 || !strings.Contains(out.String(), `"code":"OPT_UNKNOWN"`) {
		t.Errorf("json early option failure status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	status = Run([]string{"--json", "--color", "wrong"}, &input{}, &out, &errOut)
	if status != 2 || errOut.Len() != 0 || !strings.Contains(out.String(), `"code":"OPT_VALUE_INVALID"`) {
		t.Errorf("json global option failure status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestDiagnosticRendererWrapsSourceAtExplicitWidth(t *testing.T) {
	src := source.New(mem.System, "inline.kmk", "01234567890123456789x")
	defer src.Free(mem.System)
	var out bytes.Buffer
	cliDiagnosticWithSourceWidth(&out, diagnostic.Diagnostic{Source: "inline.kmk", Code: "PARSE_ERR", Severity: diagnostic.Error, Message: "bad token", Span: diagnostic.Span{Start: 20, End: 21}}, src, 20)
	if !strings.Contains(out.String(), "01234567890123456789\nx\n^\n") {
		t.Errorf("wrapped diagnostic = %q", out.String())
	}
}

func TestEmergencyDiagnosticNeedsNoAllocator(t *testing.T) {
	oldOut, oldJSON := cliDiagnosticOut, cliDiagnosticJSON
	defer func() { cliDiagnosticOut, cliDiagnosticJSON = oldOut, oldJSON }()
	var plain fixedWriter
	cliDiagnosticOut, cliDiagnosticJSON = nil, false
	writeEmergencyDiagnostic(&plain, false)
	if string(plain.data[:plain.n]) != "fatal NO_MEMORY: memory exhausted\n" {
		t.Errorf("plain emergency diagnostic = %q", string(plain.data[:plain.n]))
	}
	var json fixedWriter
	cliDiagnosticOut, cliDiagnosticJSON = &json, true
	writeEmergencyDiagnostic(&plain, true)
	if !strings.Contains(string(json.data[:json.n]), `"code":"NO_MEMORY"`) {
		t.Errorf("json emergency diagnostic = %q", string(json.data[:json.n]))
	}
}

// Repeating parser and session paths in one process keeps transient argument,
// source, session, and result state under sustained churn. The test-leaks CLI
// gate complements this by running the compiled executable under LeakSanitizer.
func TestRepeatedCLICommandLifecycleReleasesTransientState(t *testing.T) {
	tests := []struct {
		args   []string
		status int
	}{
		{[]string{"-c", "task first :\ntask second :"}, 1},
		{[]string{"do", "expr", "--allow-read=/tmp", "--allow-env=HOME", "-c", "(nop 42)", "--", "left", "right"}, 0},
		{[]string{"do", "plan", "-c", "task build :\n\techo ignored", "build"}, 0},
		{[]string{"do", "span", "--expand", "-c", "SOURCE = ./input\ntask build : @(SOURCE)\n\ttrue", "build"}, 0},
		{[]string{"do", "fmt", "--lang", "expr"}, 0},
		{[]string{"--shell", "/bin/sh", "--env", "KM_TEST=value", "--unknown"}, 2},
		{[]string{"do", "expr", "--allow-read=/tmp", "--allow-env=HOME", "--unknown"}, 2},
		{[]string{"do", "fmt", "-i", "-n", "source.kmk"}, 2},
		{[]string{"do", "inputs", "build", "--depth", "invalid"}, 2},
	}
	for iteration := 0; iteration < 64; iteration++ {
		for _, test := range tests {
			var out, errOut bytes.Buffer
			if status := Run(test.args, &input{text: "42\n"}, &out, &errOut); status != test.status {
				t.Fatalf("iteration %d args=%q status=%d want=%d stdout=%q stderr=%q", iteration, test.args, status, test.status, out.String(), errOut.String())
			}
		}
	}
}
