package clicheck

import (
	"kame/cli"
	"kame/internal/testing"
)

func TestTypedInspectionOptionsCannotBecomeBuildTargets(t *testing.T) {
	invalid := [][]string{
		{"--set", "NAME=1", "do", "plan"},
		{"-C", "/project", "--set=NAME=1", "do", "plan"},
		{"--set", "NAME=1", "--set=NAME=2", "do", "plan"},
		{"--set=NAME=1", "do", "-j", "2", "plan"},
		{"-C", "/project", "do", "plan", "--set=NAME=1"},
		{"--set=NAME=1", "do", "plan", "--"},
	}
	for i := range invalid {
		inv := cli.Parse("", invalid[i])
		if inv.OK || inv.Error.Code != "OPT_CONFLICT" {
			t.Error("misplaced typed inspection options selected build targets")
		}
		inv.Free()
	}
	inv := cli.Parse("", []string{"--set=NAME=1", "--", "do", "plan"})
	if !inv.OK || len(inv.Targets) != 2 {
		t.Error("explicit literal targets were rejected")
	}
	inv.Free()
	inv = cli.Parse("plan", []string{"--set=NAME=1", "do", "plan"})
	if !inv.OK || len(inv.Targets) != 2 {
		t.Error("correctly placed inspection options were rejected")
	}
	inv.Free()
}

func TestEmptyOptionValuesHaveConsistentDiagnostics(t *testing.T) {
	commands := []string{"parse", "fmt", "run"}
	for i := range commands {
		separated := cli.Parse(commands[i], []string{"--lang", ""})
		equals := cli.Parse(commands[i], []string{"--lang="})
		if separated.OK || equals.OK || separated.Error.Code != "OPT_VALUE_INVALID" || equals.Error.Code != separated.Error.Code {
			t.Error("empty language must have the same diagnostic for both spellings")
		}
		separated.Free()
		equals.Free()
	}
	commands = []string{"plan", "inputs", "outputs", "span"}
	for i := range commands {
		inv := cli.Parse(commands[i], []string{"--depth="})
		if inv.OK || inv.Error.Code != "OPT_VALUE_INVALID" {
			t.Error("empty depth must not be an unknown option")
		}
		inv.Free()
	}
}

func TestFormattingOptionsMustApplyToSelectedLanguage(t *testing.T) {
	invalid := [][]string{
		{"--lang=expr", "--indent=spaces"}, {"--lang=template", "--indent=tabs"},
		{"--lang=km", "--indent=spaces"}, {"--indent-width=2"}, {"--lang=expr", "--comment=plain"},
	}
	for i := range invalid {
		inv := cli.Parse("fmt", invalid[i])
		if inv.OK || inv.Error.Code != "OPT_CONFLICT" {
			t.Error("irrelevant formatting option accepted")
		}
		inv.Free()
	}
	languages := []string{"rule", "script", "kmk", "kash"}
	for i := range languages {
		inv := cli.Parse("fmt", []string{"--lang", languages[i], "--indent-width=2", "--indent=spaces"})
		if !inv.OK {
			t.Error("meaningful indentation options rejected")
		}
		inv.Free()
	}
}

func TestParseSourceCardinalityKeepsEstablishedDiagnostic(t *testing.T) {
	inv := cli.Parse("parse", []string{"--lang", "expr", "first.km", "second.km"})
	defer inv.Free()
	if inv.OK || inv.Error.Code != "OPT_VALUE_INVALID" {
		t.Error("extra parse sources changed the established usage diagnostic")
	}
}

func TestWatchOptions(t *testing.T) {
	inv := cli.Parse("", []string{"--watch=200", "--cancel"})
	if !inv.OK || !inv.Watch || inv.WatchDebounceMS != 200 || !inv.CancelOnChange {
		t.Error("watch debounce or cancel option was not parsed")
	}
	inv.Free()

	invalid := [][]string{{"--watch="}, {"--watch=-1"}, {"--watch=fast"}, {"--cancel"}}
	for i := range invalid {
		inv = cli.Parse("", invalid[i])
		if inv.OK {
			t.Error("invalid watch option was accepted")
		}
		inv.Free()
	}

	inv = cli.Parse("", []string{"--watch"})
	if !inv.OK || inv.WatchDebounceMS != 100 {
		t.Error("bare --watch did not retain the 100 ms default")
	}
	inv.Free()
}
