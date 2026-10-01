package cli_test

import (
	"kame/cli"
	"kame/lang/eval"
	"solod.dev/so/testing"
)

func TestRunInputsRetainOrderAndForwardLanguage(t *testing.T) {
	args := []string{"values.km", "answer", "-c", "(out answer)", "-l", "kash", "-c", "echo hello", "--lang=kmk", "-f", "script.mk", "build", "--entry=check", "--lang", "km", "functions.km", "-c", "(report answer)", "--", "-l", "--help", "argument.kash"}
	inv := cli.ParseRun(args)
	defer inv.Free()
	if !inv.OK || len(inv.Inputs) != 6 { t.Error("ordered runner input parse failed"); return }
	if inv.Inputs[0].Lang != "km" || inv.Inputs[0].Entries[0] != "answer" || inv.Inputs[1].Kind != "command" || inv.Inputs[1].Lang != "km" || inv.Inputs[2].Lang != "kash" { t.Error("language override applied retroactively or lost inline default") }
	if inv.Inputs[3].Lang != "kmk" || inv.Inputs[3].Value != "script.mk" || len(inv.Inputs[3].Entries) != 2 || inv.Inputs[3].Entries[1] != "check" { t.Error("file override or per-fragment entries lost") }
	if inv.Inputs[4].Lang != "km" || inv.Inputs[5].Lang != "km" || len(inv.Args) != 3 || inv.Args[0] != "-l" || inv.Args[1] != "--help" { t.Error("forward override or literal argument frame lost") }
	if !inv.NoDefaultGrants { t.Error("later process source broadened value invocation policy") }
}

func TestRunInferredLanguagesAndInlineAuthority(t *testing.T) {
	inv := cli.ParseRun([]string{"process.ksh", "-c", "name = 1", "process.kash", "rules.kmk", "values.km"})
	if !inv.OK || len(inv.Inputs) != 5 || inv.Inputs[0].Lang != "kash" || inv.Inputs[1].Lang != "km" || inv.Inputs[2].Lang != "kash" || inv.Inputs[3].Lang != "kmk" || inv.Inputs[4].Lang != "km" { t.Error("file suffix inferred retroactively for inline text") }
	inv.Free()
	inv = cli.ParseRun([]string{"-c", "", "--lang", "kash", "-c", "echo hello"})
	if !inv.OK || len(inv.Inputs) != 2 || inv.Inputs[0].Value != "" || !inv.NoDefaultGrants { t.Error("empty inline input or first-fragment authority lost") }
	inv.Free()
	inv = cli.ParseRun([]string{"-l", "kash", "-c", "echo hello", "--lang=km", "-c", "42"})
	if !inv.OK || inv.NoDefaultGrants { t.Error("process-led inline session lost its invocation defaults") }
	inv.Free()
	inv = cli.ParseRun([]string{"--allow-run=/bin/printf", "values.km", "--allow-read=./inputs", "--capture-limit=42", "--timeout", "100", "-C", "work"})
	if !inv.OK || !inv.NoDefaultGrants || len(inv.Grants) != 2 || inv.Grants[0].Capability != eval.Run || inv.Grants[0].Names[0] != "/bin/printf" || inv.CaptureLimit != 42 || inv.TimeoutMS != 100 || inv.Directory != "work" { t.Error("shared invocation policy did not survive normalization") }
	inv.Free()
}

func TestRunStdinAndExplicitUnknownSuffix(t *testing.T) {
	inv := cli.ParseRun([]string{"--lang=expr", "-", "--lang", "kmk", "-f", "Makefile", "target", "--", "-"})
	defer inv.Free()
	if !inv.OK || len(inv.Inputs) != 2 || inv.Inputs[0].Kind != "stdin" || inv.Inputs[0].Lang != "expr" || inv.Inputs[1].Value != "Makefile" || inv.Inputs[1].Entries[0] != "target" || inv.Args[0] != "-" { t.Error("stdin, explicit suffix override or argument '-' lost") }
}

func TestRunUsageErrors(t *testing.T) {
	invalid := [][]string{
		{}, {"--", "argument"}, {"--lang", "shell", "-c", "echo hello"}, {"-l"}, {"-c"}, {"--entry", "name", "values.km"}, {"script.mk"}, {"-f", "unknown"}, {"-", "-"}, {"--file=-", "-"}, {"process.kash", "unexpected-entry"}, {"--lang=expr", "-c", "42", "entry"}, {"values.km", "--entry="}, {"--allow-run=", "-c", "42"}, {"--capture-limit=0", "-c", "42"}, {"--unknown", "-c", "42"}, {"--jobs=1", "values.km"}, {"--force", "process.kash"}, {"--env=KEY=value", "values.km"},
	}
	for i := range invalid {
		inv := cli.ParseRun(invalid[i])
		if inv.OK || inv.Error.Code == "" { t.Error("invalid runner invocation accepted") }
		inv.Free()
	}
}

func TestLanguageShortAliasForInspection(t *testing.T) {
	commands := []string{"parse", "fmt"}
	for i := range commands {
		inv := cli.Parse(commands[i], []string{"-l", "kash", "source.kash"})
		if !inv.OK || inv.Lang != "kash" { t.Error("short language option was not normalized") }
		inv.Free()
		inv = cli.Parse(commands[i], []string{"-l"})
		if inv.OK || inv.Error.Code != "OPT_NO_VALUE" { t.Error("missing short language value was not rejected") }
		inv.Free()
	}
}

func TestPrimaryRunSelectionDoesNotProbeFilesystem(t *testing.T) {
	runner := [][]string{{"missing.kash"}, {"-l", "expr", "source.txt"}, {"--lang=km", "-"}, {"-c", "42"}, {"-f", "missing.kmk"}, {"-l"}}
	for i := range runner { if !cli.SelectsRun(runner[i]) { t.Error("explicit runner source was sent to build discovery") } }
	build := [][]string{{}, {"-C", "work", "target"}, {"target", "-c", "rule text"}, {"--", "literal.kash"}}
	for i := range build { if cli.SelectsRun(build[i]) { t.Error("ordinary build operand was reinterpreted as a runner source") } }
}
