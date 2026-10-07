package cli_test

import (
	"kame/cli"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestPresentationEveryCommand(t *testing.T) {
	commands := []string{"", "run", "plan", "cat", "inputs", "outputs", "span", "tools", "parse", "fmt", "render", "cache", "help"}
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
		out.Free()
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
