package clicheck

import (
	"kame/cli"
	"kame/internal/mem"
	"kame/internal/testing"
	"strings"
)

func joined(args []string) string {
	return strings.Join(args, " ")
}

func TestPickClassifiesEveryCommand(t *testing.T) {
	if cli.ClassifyPick("target", "./build/app") != cli.PickBuild || cli.ClassifyPick("target", "") != cli.PickNone {
		t.Error("target selection was not a build")
	}
	inspect := []string{"plan", "inputs", "outputs", "span", "cat"}
	run := []string{"list", "tools", "help"}
	usage := []string{"run", "kash", "parse", "fmt", "render", "clean", "cache"}
	for i := range inspect {
		if cli.ClassifyPick("command", inspect[i]) != cli.PickInspect {
			t.Error("inspect command was reclassified")
		}
	}
	for i := range run {
		if cli.ClassifyPick("command", run[i]) != cli.PickRun {
			t.Error("immediate command was reclassified")
		}
	}
	for i := range usage {
		if cli.ClassifyPick("command", usage[i]) != cli.PickUsage {
			t.Error("usage command was reclassified")
		}
	}
	for i := range cli.Commands {
		name := cli.Commands[i].Name
		included := cli.ClassifyPick("command", name) != cli.PickNone
		if name == "pick" {
			if included {
				t.Error("pick offers itself")
			}
		} else if !included {
			t.Error("command has no pick action: " + name)
		}
	}
}

func TestPickPreviewUsesStableKindsAndOneLineFields(t *testing.T) {
	if cli.PickKind("artifacts") != "artifact" || cli.PickKind("tasks") != "task" || cli.PickKind("services") != "service" || cli.PickKind("rules") != "target" || cli.PickKind("") != "target" {
		t.Error("preview kind labels changed")
	}
	if cli.PickActionText(cli.PickInspect) != "choose a target, then run" || cli.PickActionText(cli.PickUsage) != "print usage" {
		t.Error("preview action labels changed")
	}
	b := strings.Builder{}
	defer b.Reset()
	if !cli.WritePickLine(&b, "command", "clean", "do clean", "command", "-", "print usage", "clean outputs", "kame do clean [-C DIR]\nkame do clean --keep PATH...") {
		t.Error("multi-line usage was rejected")
	}
	text := b.String()
	if strings.Count(text, "\n") != 1 || !strings.Contains(text, " / ") {
		t.Error("preview usage did not stay one field: " + text)
	}
}

func TestPickLineRoundTripsTheExactName(t *testing.T) {
	b := strings.Builder{}
	defer b.Reset()
	if !cli.WritePickLine(&b, "target", "./my app", "./my app  missing", "artifacts missing") {
		t.Error("ordinary target row was rejected")
	}
	if !cli.WritePickLine(&b, "command", "plan", "do plan", "inspect prerequisites") {
		t.Error("command row was rejected")
	}
	text := b.String()
	newline := strings.IndexByte(text, '\n')
	if newline < 0 {
		t.Error("picker row was not a line")
		return
	}
	row := cli.ParsePickLine(text[:newline])
	if row.Kind != "target" || row.Name != "./my app" {
		t.Error("target name did not round-trip")
	}
	row = cli.ParsePickLine(text[newline+1:])
	if row.Kind != "command" || row.Name != "plan" {
		t.Error("command name did not round-trip")
	}
	if cli.WritePickLine(&b, "target", "a\x1fb", "a", "b") || cli.WritePickLine(&b, "target", "a\nb", "a", "b") {
		t.Error("delimiter or newline in a name was accepted")
	}
}

func TestPickRejectsControlBytesWithoutChangingANSIIdentity(t *testing.T) {
	b := strings.Builder{}
	defer b.Reset()
	for i := 0; i < 33; i++ {
		control := byte(i)
		if i == 32 {
			control = '\x7f'
		}
		name := "./a" + string(control) + "b"
		if cli.WritePickLine(&b, "target", name, "label") || b.Len() != 0 {
			t.Error("control byte in hidden identity was accepted")
		}
	}
	if cli.WritePickLine(&b, "target", "./a\x1b[31mb", "label") {
		t.Error("ANSI sequence in hidden name was accepted")
	}
	if !cli.WritePickLine(&b, "target", "./my app", "./my app  \x1b[2mmissing\x1b[0m") {
		t.Error("ANSI styling in visible label was rejected")
	}
	row := cli.ParsePickLine(b.String())
	if row.Name != "./my app" || row.Kind != "target" {
		t.Error("visible ANSI styling changed hidden identity")
	}
}

func TestPickGrammarForwardsExecutionFlagsAndRejectsTargets(t *testing.T) {
	inv := cli.Parse("pick", []string{"-C", "work", "-f", "Build.kmk", "-j", "3", "-n", "--force", "--picker", "fzy", "--define", "MODE=release", "--set", "FLAG=:false", "name=value"})
	if !inv.OK || inv.Directory != "work" || inv.File != "Build.kmk" || inv.Jobs != 3 || !inv.DryRun || !inv.Force || inv.Picker != "fzy" || len(inv.Defines) != 2 || len(inv.Parameters) != 1 {
		t.Error("pick did not keep source, execution, or picker options: " + inv.Error.Code + " " + inv.Error.Message)
	}
	build := cli.PickArgs(&inv, "build", "./build/app")
	text := joined(build)
	if text != "-C work -f Build.kmk --define MODE=release --set FLAG=:false -n --force -j 3 name=value -- ./build/app" {
		t.Error("build forwarding changed: " + text)
	}
	mem.FreeString(mem.System, text)
	cli.FreeArgs(build)
	cat := cli.PickArgs(&inv, "cat", "app")
	text = joined(cat)
	if text != "-C work -f Build.kmk --define MODE=release --set FLAG=:false --force -j 3 name=value -- app" {
		t.Error("cat forwarding kept dry-run or dropped execution flags: " + text)
	}
	mem.FreeString(mem.System, text)
	cli.FreeArgs(cat)
	inspect := cli.PickArgs(&inv, "inspect", "app")
	text = joined(inspect)
	if text != "-C work -f Build.kmk --define MODE=release --set FLAG=:false name=value -- app" {
		t.Error("inspection forwarding included execution flags: " + text)
	}
	mem.FreeString(mem.System, text)
	cli.FreeArgs(inspect)
	list := cli.PickArgs(&inv, "list", "")
	text = joined(list)
	if text != "-C work -f Build.kmk --define MODE=release --set FLAG=:false" {
		t.Error("list forwarding included a parameter or target: " + text)
	}
	mem.FreeString(mem.System, text)
	cli.FreeArgs(list)
	inv.Free()
	invalid := [][]string{{"default"}, {"--picker"}, {"--picker="}, {"--watch", "app"}}
	for i := range invalid {
		rejected := cli.Parse("pick", invalid[i])
		if rejected.OK {
			t.Error("invalid pick invocation was accepted")
		}
		rejected.Free()
	}
}

func TestPickForwardsWatchSettingsToBuild(t *testing.T) {
	inv := cli.Parse("pick", []string{"--watch=250", "--cancel"})
	defer inv.Free()
	if !inv.OK || !inv.Watch || inv.WatchDebounceMS != 250 || !inv.CancelOnChange {
		t.Fatal("pick watch options were not parsed")
	}
	args := cli.PickArgs(&inv, "build", "app")
	text := joined(args)
	if !strings.Contains(text, "--watch=250 --cancel") {
		t.Error("pick dropped watch settings: " + text)
	}
	mem.FreeString(mem.System, text)
	cli.FreeArgs(args)
}
