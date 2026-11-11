package clicheck

import (
	"kame/cli"
	"kame/internal/testing"
	"strings"
)

func TestPresentationEveryCommand(t *testing.T) {
	commands := []string{"", "run", "list", "pick", "plan", "cat", "inputs", "outputs", "span", "tools", "parse", "fmt", "render", "cache", "help"}
	for i := range commands {
		inv := cli.Parse(commands[i], []string{"--output=json"})
		if inv.Output != "json" || !inv.JSON {
			t.Error("command did not retain global output selection")
		}
		inv.Free()
		inv = cli.Parse(commands[i], nil)
		if inv.Output != "ansi" || inv.JSON {
			t.Error("ANSI is not the shared default")
		}
		inv.Free()
	}
}

func TestReportPreservesNestedEnvelopeNames(t *testing.T) {
	documents := []string{
		`{"schema":1,"type":"plan","configuration":{"type":"release","schema":"v2"},"arguments":{"type":"arg","schema":"arg-schema"},"tools":{"type":"type-tool","schema":"schema-tool"},"entries":[{"type":"entry","schema":"entry-schema"}]}`,
		`[{"type":"entry","schema":"entry-schema"}]`,
	}
	expected := []string{
		"report\n  configuration\n    type: \"release\"\n    schema: \"v2\"\n  arguments\n    type: \"arg\"\n    schema: \"arg-schema\"\n  tools\n    type: \"type-tool\"\n    schema: \"schema-tool\"\n  entries\n    type: \"entry\"\n    schema: \"entry-schema\"\n",
		"report\n  type: \"entry\"\n  schema: \"entry-schema\"\n",
	}
	for i := range documents {
		var out strings.Builder
		if !cli.WriteReport(&out, "report", documents[i], false) || out.String() != expected[i] {
			t.Error("report suppressed nested or list-entry schema/type keys")
		}
		out.Reset()
	}
}

func TestTargetReportGroupsKindsAndKeepsStatusReadableWithoutColor(t *testing.T) {
	document := `{"schema":1,"type":"list","entries":[{"name":"./out","group":"artifacts","status":"missing"},{"name":"test","group":"tasks","status":"built"},{"name":"build","group":"rules","status":"","summary":"Build project"},{"name":"daemon","group":"services","status":"","summary":"Run daemon"},{"name":"package","group":"tasks","status":"outdated"},{"name":"./bad\u001b[31m","group":"artifacts","status":"unknown"},{"name":"./{name}.o","group":"artifacts","status":""}]}`
	var out strings.Builder
	defer out.Reset()
	if !cli.WriteReport(&out, "list", document, false) || out.String() != "list\n  Rules\n    build  Build project\n  Services\n    daemon  Run daemon\n  Tasks\n    test  ok\n    package  out of date\n  Artifacts\n    ./{name}.o\n    ./out  missing\n    \"./bad\\u001b[31m\"  unknown\n" {
		t.Error("target groups, match ordering, status labels or terminal escaping changed: " + out.String())
	}
}

func TestInspectionBoundariesGroupDistinctAttributesByPath(t *testing.T) {
	document := `{"schema":2,"type":"outputs","depth":1,"resources":[{"id":1,"key":{"kind":"file"},"display":"./result","roles":["artifact"],"status":"missing"}],"producers":[{"id":1,"resource":1}],"dependencies":[],"deferred":[{"producer":1,"reason":"runtime-discovery"},{"resource":1,"producer":1,"reason":"opaque-process-io"},{"resource":1,"producer":1,"reason":"runtime-discovery"}]}`
	var out strings.Builder
	if !cli.WriteReport(&out, "outputs", document, false) || !strings.Contains(out.String(), "  discovery boundaries\n    ./result · runtime-discovery · opaque-process-io\n") {
		t.Error("boundary attributes were not grouped and deduplicated by path")
	}
	out.Reset()
}

func TestDownstreamReportKeepsLogicalImpactRelationships(t *testing.T) {
	document := `{"schema":2,"type":"outputs","depth":-1,"direction":"downstream","resources":[{"id":1,"key":{"kind":"file"},"display":"./seed","root":true,"roles":["input"]},{"id":2,"key":{"kind":"target"},"display":"middle","producer":1,"roles":[]},{"id":3,"key":{"kind":"file"},"display":"./out","producer":2,"roles":["artifact"]},{"id":4,"key":{"kind":"file"},"display":"./context","roles":["input"]}],"producers":[{"id":1,"resource":2,"target":"middle"},{"id":2,"resource":3,"target":"./out"}],"dependencies":[{"producer":1,"resource":1},{"producer":2,"resource":2},{"producer":2,"resource":4}],"deferred":[]}`
	var out strings.Builder
	if !cli.WriteReport(&out, "outputs", document, false) || !strings.Contains(out.String(), "./seed → middle") || !strings.Contains(out.String(), "middle → ./out") || strings.Contains(out.String(), "./context → ./out") {
		t.Error("logical impact chain was omitted or unaffected context was included")
	}
	out.Reset()
}

func TestPlanReportHidesEmptyAndUnknownDetailsUnlessVerbose(t *testing.T) {
	document := `{"schema":2,"type":"plan","depth":-1,"resources":[],"producers":[{"id":1,"resource":1,"target":"./out","kind":"file","outputs":[],"freshness":"unknown","captures":[],"source":"Makefile.kmk","span":{"start":0,"end":710},"rule":{"start":0,"end":710,"text":"install :"},"text":"producer source","tools":{},"reusePolicy":"content-signatures"},{"id":2,"resource":2,"target":"install","kind":"task","source":"Makefile.kmk","reusePolicy":"fingerprint"}],"dependencies":[],"stages":[{"number":1,"producers":[1,2]}],"deferred":[]}`
	var concise, verbose strings.Builder
	defer concise.Reset()
	defer verbose.Reset()
	if !cli.WriteGraphReport(&concise, "plan", document, false, false) {
		t.Error("concise plan report failed to render")
		return
	}
	text := concise.String()
	if strings.Contains(text, "freshness") || strings.Contains(text, "captures") || strings.Contains(text, "tools") || strings.Contains(text, "start: 0") || strings.Contains(text, "producer source") || !strings.Contains(text, "    ./out · file · source: Makefile.kmk · reuse: content-signatures\n") || !strings.Contains(text, "    stage 1 · parallel eligible · ./out · install\n") {
		t.Error("concise plan report was not compact and single-line: " + text)
	}
	if !cli.WriteGraphReport(&verbose, "plan", document, false, true) {
		t.Error("verbose plan report failed to render")
		return
	}
	text = verbose.String()
	if !strings.Contains(text, "freshness: unknown") || !strings.Contains(text, "captures: []") || !strings.Contains(text, "tools: {}") || !strings.Contains(text, "rule: {\"start\":0,\"end\":710,\"text\":\"install :\"}") || !strings.Contains(text, "span: {\"start\":0,\"end\":710}") || strings.Contains(text, "rule\n") {
		t.Error("verbose plan report omitted details or split a producer across lines: " + text)
	}
}

func TestPresentationRespectsValuesAndLiteralTail(t *testing.T) {
	inv := cli.Presentation([]string{"-c", "--json", "--define", "--output", "--indent", "--json", "-o", "text", "--", "--json"})
	defer inv.Free()
	if !inv.OK || inv.Output != "text" || inv.JSON || len(inv.Args) != 8 || inv.Args[1] != "--json" || inv.Args[7] != "--json" {
		t.Error("global extraction inspected option values or literal arguments")
	}
	conflict := cli.Presentation([]string{"--json", "-o", "ansi"})
	defer conflict.Free()
	if conflict.OK || conflict.Error.Code != "OPT_CONFLICT" || !conflict.JSON {
		t.Error("output conflict lost selected JSON diagnostics")
	}
	same := cli.Presentation([]string{"-o", "json", "--json"})
	defer same.Free()
	if !same.OK || !same.JSON {
		t.Error("equivalent output selections conflict")
	}
	early := cli.Presentation([]string{"--color", "bad", "--json"})
	defer early.Free()
	if early.OK || !early.JSON {
		t.Error("global failure ignored later JSON selection")
	}
}

func TestPresentationOptionsStayOutOfSourceSelection(t *testing.T) {
	options := []string{"-o", "--output", "--color", "--diagnostic-format"}
	for i := range options {
		if !cli.OptionTakesValue(options[i]) {
			t.Error("global value option missing from dispatch grammar")
		}
		if cli.AppendsCommands([]string{options[i], "-c", "chosen"}) {
			t.Error("global option value was mistaken for inline source")
		}
		if cli.SelectsRun([]string{options[i], "source.km", "chosen"}) {
			t.Error("global option value was mistaken for a value source")
		}
	}
	inv := cli.Parse("build", []string{"--output", "text", "--color=never", "--diagnostic-format", "plain", "chosen"})
	if !inv.OK || inv.Output != "text" || inv.Color != "never" || inv.DiagnosticFormat != "plain" || inv.RetainBytes != 0 || len(inv.Targets) != 1 {
		t.Error("global controls leaked into build-option assignments")
	}
	inv.Free()
}
