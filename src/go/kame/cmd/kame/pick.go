package main

import (
	"bytes"
	"io"
	"kame/cli"
	"kame/core"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/program"

	"strings"
)

func runPick(args []string, out io.Writer, errOut io.Writer) int {
	parsed := cli.Parse("pick", args)
	applyInvocationPresentation(&parsed)
	defer parsed.Free()
	if !parsed.OK {
		cliError(errOut, parsed.Error.Code, parsed.Error.Message)
		return 2
	}
	var catalogue, targets string
	status := pickCatalogue(parsed, errOut, &catalogue, &targets)
	if status != 0 {
		return status
	}
	defer mem.FreeString(mem.System, catalogue)
	defer mem.FreeString(mem.System, targets)
	var selection string
	status = pickChoose(parsed.Picker, catalogue, "kame> ", "targets, then commands", "", &selection)
	if status != 0 {
		return pickFail(errOut, status, parsed.Picker)
	}
	defer mem.FreeString(mem.System, selection)
	return runPickSelection(&parsed, selection, targets, out, errOut)
}

func pickCatalogue(inv cli.Invocation, errOut io.Writer, all *string, targets *string) int {
	session := openBuildSession(inv, errOut, true)
	if session.Status != 0 {
		status := session.Status
		session.Free()
		return status
	}
	ok := collectPickLines(session.Program, all, targets)
	session.Free()
	if !ok {
		mem.FreeString(mem.System, *all)
		mem.FreeString(mem.System, *targets)
		*all = ""
		*targets = ""
		cliError(errOut, "HOST_FAIL", "cannot list targets")
		return 1
	}
	return 0
}

func collectPickLines(p *program.Program, all *string, targets *string) bool {
	var report bytes.Buffer
	report = bytes.Buffer{}
	defer report.Reset()
	p.WriteTargetReport(&report, "")
	var value core.Value
	if !core.ParseJSON(mem.System, report.Bytes(), &value) {
		return false
	}
	defer value.Free(mem.System)
	allLines := strings.Builder{}
	targetLines := strings.Builder{}
	defer allLines.Reset()
	defer targetLines.Reset()
	entries := documentField(value, "entries")
	for i := range entries.List {
		item := entries.List[i]
		name := documentField(item, "name").Text
		group := documentField(item, "group").Text
		status := cli.PickStatus(documentField(item, "status").Text)
		label := name
		if status != "" {
			label = name + "  \x1b[2m" + status + "\x1b[0m"
		}
		kind := cli.PickKind(group)
		shown := status
		if shown == "" {
			shown = "-"
		}
		if !cli.WritePickLine(&allLines, "target", name, label, kind, shown, "build", "-", "kame -- "+name) {
			continue
		}
		cli.WritePickLine(&targetLines, "target", name, label, kind, shown, "build", "-", "kame -- "+name)
	}
	for i := range cli.Commands {
		command := cli.Commands[i]
		action := cli.ClassifyPick("command", command.Name)
		if command.Name == "pick" || action == cli.PickNone {
			continue
		}
		label := "do " + command.Name + "  \x1b[2m" + command.Summary + "\x1b[0m"
		cli.WritePickLine(&allLines, "command", command.Name, label, "command", "-", cli.PickActionText(action), command.Summary, command.Usage)
	}
	*all = cloneCommandText(allLines.String())
	*targets = cloneCommandText(targetLines.String())
	return true
}

func pickChoose(explicit string, input string, prompt string, header string, preview string, selection *string) int {
	name := explicit
	fallback := explicit == ""
	if name == "" {
		name = "fzf"
	}
	args := pickFilterArgs(prompt, header, preview)
	defer mem.FreeSlice(nil, args)
	status := posix.Pick(name, args, input, selection)
	if status == 127 && fallback {
		mem.FreeString(mem.System, *selection)
		*selection = ""
		status = posix.Pick("sk", args, input, selection)
	}
	return status
}

const pickPreview = "printf 'kind: %s\nname: %s\nstatus: %s\naction: %s\nhelp: %s\nusage: %s\n' {4} {2} {5} {6} {7} {8}"

func pickFilterArgs(prompt string, header string, preview string) []string {
	if preview == "" {
		preview = pickPreview
	}
	var args []string
	args = append(args, "--ansi")
	args = append(args, "--delimiter")
	args = append(args, "\x1f")
	args = append(args, "--with-nth")
	args = append(args, "3")
	// Do not also pass --nth. --with-nth already reduces the row to the label,
	// and --nth 3 then searches a field that no longer exists.
	args = append(args, "--prompt")
	args = append(args, prompt)
	args = append(args, "--header")
	args = append(args, header)
	args = append(args, "--preview-window")
	args = append(args, "right,50%,wrap,border-left")
	args = append(args, "--preview")
	args = append(args, preview)
	return args
}

func pickFail(errOut io.Writer, status int, explicit string) int {
	if status == 130 {
		posix.TakeSignal()
		return 130
	}
	if status == -2 {
		cliError(errOut, "FEATURE_UNSUP", "interactive pick requires the native POSIX CLI")
		return 1
	}
	if status == -3 {
		cliError(errOut, "PICK_UNAVAILABLE", "pick requires a terminal")
		return 2
	}
	if status == 127 {
		if explicit != "" {
			cliError(errOut, "PICK_UNAVAILABLE", "interactive picker not found: "+explicit)
		} else {
			cliError(errOut, "PICK_UNAVAILABLE", "interactive picker not found: install fzf or pass --picker NAME")
		}
		return 2
	}
	if status == 1 {
		cliError(errOut, "PICK_UNAVAILABLE", "no match")
		return 1
	}
	cliError(errOut, "HOST_FAIL", "picker failed")
	return 1
}

func runPickSelection(inv *cli.Invocation, selection string, targets string, out io.Writer, errOut io.Writer) int {
	row := cli.ParsePickLine(selection)
	if row.Kind == "" {
		cliError(errOut, "PICK_UNAVAILABLE", "unrecognized selection")
		return 1
	}
	kind, name := row.Kind, row.Name
	action := cli.ClassifyPick(kind, name)
	if action == cli.PickBuild {
		return pickExec(inv, "build", "build", name, out, errOut)
	}
	if action == cli.PickInspect {
		if targets == "" {
			cliError(errOut, "PICK_UNAVAILABLE", "no targets to choose")
			return 1
		}
		prompt := cloneCommandText(name + "> ")
		preview := "printf 'kind: %s\nname: %s\nstatus: %s\naction: choose a target, then run\nhelp: %s\nusage: kame do " + name + " -- %s\n' {4} {2} {5} {7} {2}"
		var chosen string
		status := pickChoose(inv.Picker, targets, prompt, "choose a target", preview, &chosen)
		mem.FreeString(mem.System, prompt)
		if status != 0 {
			mem.FreeString(mem.System, chosen)
			return pickFail(errOut, status, inv.Picker)
		}
		target := cli.ParsePickLine(chosen)
		if target.Kind != "target" || target.Name == "" {
			mem.FreeString(mem.System, chosen)
			cliError(errOut, "PICK_UNAVAILABLE", "unrecognized selection")
			return 1
		}
		kindName := "inspect"
		if name == "cat" {
			kindName = "cat"
		}
		status = pickExec(inv, name, kindName, target.Name, out, errOut)
		mem.FreeString(mem.System, chosen)
		return status
	}
	if action == cli.PickRun {
		if name == "help" {
			writeDoHelp(out)
			return 0
		}
		return pickExec(inv, name, "list", "", out, errOut)
	}
	if action == cli.PickUsage {
		if writeCommandHelp(out, name) {
			return 0
		}
		return 1
	}
	cliError(errOut, "PICK_UNAVAILABLE", "unrecognized selection")
	return 1
}

func pickExec(inv *cli.Invocation, command string, kind string, target string, out io.Writer, errOut io.Writer) int {
	forwarded := cli.PickArgs(inv, kind, target)
	defer cli.FreeArgs(forwarded)
	if command == "build" {
		return runBuild(forwarded, out, errOut)
	}
	if command == "cat" {
		return runCat(forwarded, out, errOut)
	}
	if command == "plan" {
		return runPlan(forwarded, out, errOut)
	}
	if command == "inputs" || command == "outputs" || command == "span" {
		return runGraph(forwarded, out, errOut, command)
	}
	if command == "list" {
		return runList(forwarded, out, errOut)
	}
	if command == "tools" {
		return runTools(forwarded, out, errOut)
	}
	return 1
}
