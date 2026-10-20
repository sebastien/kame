package cli

import (
	"kame/lang/definition"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

// PickAction is what Enter does with one picker row.
type PickAction int

const (
	PickNone PickAction = iota
	PickBuild
	PickInspect
	PickRun
	PickUsage
)

// ClassifyPick maps a picker row to the action Enter should take.
// kind is "target" or "command". pick itself is not an action.
func ClassifyPick(kind string, name string) PickAction {
	if kind == "target" && name != "" {
		return PickBuild
	}
	if kind != "command" {
		return PickNone
	}
	if name == "plan" || name == "inputs" || name == "outputs" || name == "span" || name == "cat" {
		return PickInspect
	}
	if name == "list" || name == "tools" || name == "help" {
		return PickRun
	}
	if name == "run" || name == "parse" || name == "fmt" || name == "render" || name == "clean" || name == "cache" {
		return PickUsage
	}
	return PickNone
}

// PickKind is the preview name for a do list group. Bare rules are targets.
// Value symbols are not in the catalogue.
func PickKind(group string) string {
	if group == "services" {
		return "service"
	}
	if group == "tasks" {
		return "task"
	}
	if group == "artifacts" {
		return "artifact"
	}
	return "target"
}

// PickActionText is the preview label for what Enter will do.
func PickActionText(action PickAction) string {
	if action == PickBuild {
		return "build"
	}
	if action == PickInspect {
		return "choose a target, then run"
	}
	if action == PickRun {
		return "run"
	}
	if action == PickUsage {
		return "print usage"
	}
	return "-"
}

// PickStatus is the short label do list uses for a catalogue status.
func PickStatus(status string) string {
	if status == "built" {
		return "ok"
	}
	if status == "outdated" {
		return "out of date"
	}
	if status == "missing" || status == "unknown" {
		return status
	}
	return ""
}

func pickFieldOK(text string) bool {
	return strings.IndexByte(text, '\x1f') < 0 && strings.IndexByte(text, '\n') < 0 && strings.IndexByte(text, '\r') < 0
}

func pickIdentityOK(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] < ' ' || text[i] == '\x7f' {
			return false
		}
	}
	return true
}

// WritePickLine appends one picker row. The hidden fields are kind and the
// exact name; the visible field is label. Later fields are the preview record.
// Newlines in those fields become " / " so each key stays one line. A name
// containing control bytes cannot round-trip through --ansi and is skipped.
func WritePickLine(b *strings.Builder, kind string, name string, label string, fields ...string) bool {
	if kind == "" || name == "" || label == "" {
		return false
	}
	if !pickIdentityOK(kind) || !pickIdentityOK(name) || !pickFieldOK(label) {
		return false
	}
	for i := range fields {
		if strings.IndexByte(fields[i], '\x1f') >= 0 {
			return false
		}
	}
	b.WriteString(kind)
	b.WriteByte('\x1f')
	b.WriteString(name)
	b.WriteByte('\x1f')
	b.WriteString(label)
	for i := range fields {
		b.WriteByte('\x1f')
		writeFlatField(b, fields[i])
	}
	b.WriteByte('\n')
	return true
}

func writeFlatField(b *strings.Builder, text string) {
	if text == "" {
		b.WriteByte('-')
		return
	}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' || text[i] == '\r' {
			if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			b.WriteString(" / ")
			continue
		}
		b.WriteByte(text[i])
	}
}

// PickRow is one accepted picker line. Kind and Name alias the input line.
type PickRow struct {
	Kind string
	Name string
}

// ParsePickLine reads the kind and exact name from a picker row.
func ParsePickLine(line string) PickRow {
	if len(line) != 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) != 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	sep := strings.IndexByte(line, '\x1f')
	if sep <= 0 {
		return PickRow{}
	}
	kind := line[:sep]
	rest := line[sep+1:]
	sep = strings.IndexByte(rest, '\x1f')
	if sep <= 0 {
		return PickRow{}
	}
	return PickRow{Kind: kind, Name: rest[:sep]}
}

func parsePick(inv *Invocation, args []string) {
	var remaining []string
	defer slices.Free(mem.System, remaining)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for ; i < len(args); i++ {
				remaining = slices.Append(mem.System, remaining, args[i])
			}
			break
		}
		if arg == "--picker" {
			if i+1 == len(args) {
				inv.fail("OPT_NO_VALUE", "missing value for --picker")
				return
			}
			i++
			if args[i] == "" {
				inv.fail("OPT_VALUE_INVALID", "empty value for --picker")
				return
			}
			inv.Picker = args[i]
			continue
		}
		if len(arg) >= 9 && arg[:9] == "--picker=" {
			if len(arg) == 9 {
				inv.fail("OPT_VALUE_INVALID", "empty value for --picker")
				return
			}
			inv.Picker = arg[9:]
			continue
		}
		remaining = slices.Append(mem.System, remaining, arg)
	}
	parseBuild(inv, remaining)
	if !inv.OK {
		return
	}
	// parseBuild keeps a bare NAME=VALUE as both a parameter and a target.
	// Only a target that is not that assignment is a picker operand.
	for i := range inv.Targets {
		if !pickParameter(inv, inv.Targets[i]) {
			inv.fail("OPT_VALUE_INVALID", "pick does not accept targets")
			return
		}
	}
}

func pickParameter(inv *Invocation, arg string) bool {
	for i := range inv.Parameters {
		if inv.Parameters[i] == arg {
			return true
		}
	}
	return false
}

func appendOwned(args []string, text string) []string {
	return slices.Append(mem.System, args, cloneText(text))
}

func appendNumber(args []string, flag string, number int64) []string {
	var digits [strconv.MaxIntBase10Len]byte
	args = appendOwned(args, flag)
	return appendOwned(args, strconv.FormatInt(digits[:], number, 10))
}

func appendGrantArgs(args []string, grant eval.Grant) []string {
	flag := "--allow-read"
	if grant.Capability == eval.Write {
		flag = "--allow-write"
	}
	if grant.Capability == eval.Run {
		flag = "--allow-run"
	}
	if grant.Capability == eval.Env {
		flag = "--allow-env"
	}
	if len(grant.Names) == 0 {
		return appendOwned(args, flag)
	}
	for i := range grant.Names {
		args = appendOwned(args, flag+"="+grant.Names[i])
	}
	return args
}

// PickArgs builds an owned argv for the command Enter selected. kind is
// "build", "cat", "inspect", or "list". Free it with FreeArgs.
func PickArgs(inv *Invocation, kind string, target string) []string {
	var args []string
	if inv.Directory != "" && inv.Directory != "." {
		args = appendOwned(args, "-C")
		args = appendOwned(args, inv.Directory)
	}
	if inv.File != "" {
		args = appendOwned(args, "-f")
		args = appendOwned(args, inv.File)
	}
	if inv.Command != "" {
		args = appendOwned(args, "-c")
		args = appendOwned(args, inv.Command)
	}
	for i := range inv.Defines {
		override := definition.ParseOverride(inv.Defines[i])
		if override.Typed {
			args = appendOwned(args, "--set")
			args = appendOwned(args, override.Name+"="+override.Text)
		} else {
			args = appendOwned(args, "--define")
			args = appendOwned(args, inv.Defines[i])
		}
	}
	for i := range inv.ToolOverrides {
		args = appendOwned(args, "--tool")
		args = appendOwned(args, inv.ToolOverrides[i])
	}
	for i := range inv.Grants {
		args = appendGrantArgs(args, inv.Grants[i])
	}
	execute := kind == "build" || kind == "cat"
	if execute {
		if inv.DryRun && kind != "cat" {
			args = appendOwned(args, "-n")
		}
		if inv.Force {
			args = appendOwned(args, "--force")
		}
		if inv.Verbose {
			args = appendOwned(args, "--verbose")
		}
		if inv.Jobs != 1 {
			args = appendNumber(args, "-j", int64(inv.Jobs))
		}
		for i := range inv.Shell {
			args = appendOwned(args, "--shell")
			args = appendOwned(args, inv.Shell[i])
		}
		for i := range inv.Environment {
			args = appendOwned(args, "--env")
			args = appendOwned(args, inv.Environment[i])
		}
		if inv.TimeoutMS != 0 {
			args = appendNumber(args, "--timeout", inv.TimeoutMS)
		}
		if inv.RetryCount != 0 {
			args = appendNumber(args, "--retry", int64(inv.RetryCount))
		}
		if inv.RetainBytes != 0 {
			args = appendNumber(args, "--log-limit", int64(inv.RetainBytes))
		}
		if inv.CaptureLimit != 0 {
			args = appendNumber(args, "--capture-limit", int64(inv.CaptureLimit))
		}
		if inv.Watch && kind == "build" {
			args = appendOwned(args, "--watch")
		}
	}
	if kind != "list" {
		for i := range inv.Parameters {
			args = appendOwned(args, inv.Parameters[i])
		}
	}
	if target != "" {
		args = appendOwned(args, "--")
		args = appendOwned(args, target)
	}
	return args
}

// FreeArgs releases an argv from PickArgs.
func FreeArgs(args []string) {
	for i := range args {
		mem.FreeString(mem.System, args[i])
	}
	slices.Free(mem.System, args)
}
