package main

import (
	"solod.dev/so/io"
)

// version is the release reported by -V/--version.
const version = "0.1.0"

// topHelpText is the overview shown by -h/--help and when a bare invocation
// finds no build source. It is static so it can be written without allocation.
const topHelpText = `littlemake - a reactive build engine with a build language

Usage:
  littlemake [OPTIONS] [TARGET...]
  littlemake do COMMAND [OPTIONS] [ARG...]
  littlemake -h | --help
  littlemake -V | --version

Builds TARGET using rules from a LittleMake source. With no TARGET, the
default target is built when defined; otherwise the available targets are
listed. Without -f/--file or -c/--command, source discovery tries
Makefile.lmk, then make.lmk, then src/lmk/main.lmk.

Build options:
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
  -j, --jobs N           maximum concurrent nodes (N > 0, default 1)
  -n, --dry-run          plan and render without executing effects
      --force            ignore freshness and cached-task hits
      --json             emit machine-readable JSON Lines
      --verbose          report cache decisions and warnings
      --shell SHELL      recipe shell executable (repeatable)
      --env NAME=VALUE   add a recipe environment entry (repeatable)
      --timeout MS       per-command timeout in milliseconds
      --retry N          retry failed commands N times
      --log-limit N      maximum captured bytes per step
  -h, --help             show this help
  -V, --version          show the version

Commands (littlemake do COMMAND):
  run      materialize targets and stream all execution events
  plan     print the resolved plan without executing
  cat      materialize one target and print its artifact
  inputs   list declared input paths
  outputs  list declared output paths
  span     show transitive inputs and outputs
  parse    parse a language file and print a JSON AST
  fmt      format source in place or check it
  expr     evaluate a standalone expression

Examples:
  littlemake                          build the default target
  littlemake build test               build several targets
  littlemake -f Build.lmk dist        use a specific build file
  littlemake do plan dist             inspect a target plan
  littlemake do fmt -i Makefile.lmk   format a build file in place
  littlemake do expr -c '(join ["a" "b"] ",")'
  littlemake --help                   show the overview
  littlemake --version                show the version

Run 'littlemake do COMMAND --help' for command-specific help.
`

// doHelpText lists the utility commands of the do namespace.
const doHelpText = `littlemake do COMMAND [OPTIONS] [ARG...]

Utility commands for building, inspecting, and working with LittleMake
sources. Build execution still uses the primary invocation; these commands add
inspection and language tooling.

Commands:
  run      materialize targets and stream all execution events
  plan     print the resolved plan without executing
  cat      materialize one target and print its artifact
  inputs   list declared input paths (--depth N)
  outputs  list declared output paths (--depth N)
  span     show transitive inputs and outputs (--expand, --depth N)
  parse    parse a language file and print a JSON AST (--lang LANG)
  fmt      format source in place (-i) or check it (-n)
  expr     evaluate a standalone expression with capability grants
  help     show this help, or help for one COMMAND

Run 'littlemake do COMMAND --help' for command-specific help.
`

const runHelpText = `Usage: littlemake do run [OPTIONS] [TARGET...]

Materialize targets and stream every execution event. This shares the runtime
path with the primary invocation; it differs in presentation and options only.

Options:
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
  -j, --jobs N           maximum concurrent nodes (N > 0, default 1)
  -n, --dry-run          plan and render without executing effects
      --force            ignore freshness and cached-task hits
      --json             emit machine-readable JSON Lines
      --verbose          report cache decisions and warnings
      --shell SHELL      recipe shell executable (repeatable)
      --env NAME=VALUE   add a recipe environment entry (repeatable)
      --timeout MS       per-command timeout in milliseconds
      --retry N          retry failed commands N times
      --log-limit N      maximum captured bytes per step
  -h, --help             show this help
`

const planHelpText = `Usage: littlemake do plan [OPTIONS] TARGET...

Print the selected rule, captures, declared inputs and outputs, and freshness
for each TARGET, without evaluating effects or running processes. Emits one
JSON object per target.

Options:
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
      --json             emit machine-readable diagnostics
  -h, --help             show this help
`

const catHelpText = `Usage: littlemake do cat [OPTIONS] TARGET

Materialize exactly one TARGET and write its file bytes or definition value to
stdout without a trailing newline. A task without an artifact fails with
NO_ARTIFACT.

Options:
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
      --json             emit machine-readable diagnostics
  -h, --help             show this help
`

const inputsHelpText = `Usage: littlemake do inputs [--depth N] [OPTIONS] TARGET

List the declared input paths of one TARGET as a JSON array. --depth 0 returns
no edges, 1 (the default) returns direct edges, and -1 is unlimited.

Options:
      --depth N          edge depth: -1, 0, or a positive integer
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
  -h, --help             show this help
`

const outputsHelpText = `Usage: littlemake do outputs [--depth N] [OPTIONS] TARGET

List the declared output paths of one TARGET as a JSON array. --depth 0 returns
no edges, 1 (the default) returns direct edges, and -1 is unlimited.

Options:
      --depth N          edge depth: -1, 0, or a positive integer
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
  -h, --help             show this help
`

const spanHelpText = `Usage: littlemake do span [--expand] [--depth N] [OPTIONS] TARGET

Show statically known and evaluation-dependent inputs and outputs for one
TARGET as JSON. --expand evaluates dynamic definitions without running recipes.

Options:
      --expand           evaluate dynamic definitions
      --depth N          edge depth: -1, 0, or a positive integer
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
  -h, --help             show this help
`

const parseHelpText = `Usage: littlemake do parse --lang LANG [FILE]

Parse FILE, or stdin when FILE is omitted, and print a stable JSON AST. Source
spans are included; allocator and pointer details are not.

Options:
      --lang LANG   required: expr | template | rule | script
  -h, --help        show this help
`

const fmtHelpText = `Usage: littlemake do fmt [--lang LANG] [-i | -n] [FILE...]

Format source to stdout, or replace each FILE. With no FILE, read stdin (only
without -i or -n). -n lists files that would change and exits 1 when any differ.

Options:
      --lang LANG   expr | template | rule | script (default script)
  -i                replace files in place
  -n                check for differences without writing
  -h, --help        show this help
`

const exprHelpText = `Usage: littlemake do expr [-c TEXT | FILE] [OPTIONS] [-- ARG...]

Evaluate an expression and print its result. Reads FILE, or stdin when neither
-c nor FILE is given. Arguments after -- are available to the expression as the
args list. Capabilities are denied by default.

Options:
  -c, --command TEXT        expression source text
  -C, --directory DIR       set the working directory
      --allow-read[=ROOTS]  permit file reads
      --allow-write[=ROOTS] permit file writes
      --allow-run           permit shell execution
      --allow-env[=NAMES]   permit environment reads
  -h, --help                show this help
`

func writeTopHelp(out io.Writer) { io.WriteString(out, topHelpText) }
func writeDoHelp(out io.Writer) { io.WriteString(out, doHelpText) }
func writeVersion(out io.Writer) { io.WriteString(out, "littlemake "); io.WriteString(out, version); io.WriteString(out, "\n") }

// writeCommandHelp prints help for one do command and reports whether the
// command is known.
func writeCommandHelp(out io.Writer, command string) bool {
	if command == "run" { io.WriteString(out, runHelpText); return true }
	if command == "plan" { io.WriteString(out, planHelpText); return true }
	if command == "cat" { io.WriteString(out, catHelpText); return true }
	if command == "inputs" { io.WriteString(out, inputsHelpText); return true }
	if command == "outputs" { io.WriteString(out, outputsHelpText); return true }
	if command == "span" { io.WriteString(out, spanHelpText); return true }
	if command == "parse" { io.WriteString(out, parseHelpText); return true }
	if command == "fmt" { io.WriteString(out, fmtHelpText); return true }
	if command == "expr" { io.WriteString(out, exprHelpText); return true }
	return false
}

// runHelpCommand implements 'littlemake do help [COMMAND]'.
func runHelpCommand(args []string, out io.Writer, errOut io.Writer) int {
	if len(args) != 0 && args[0] == "--" { args = args[1:] }
	if len(args) == 0 || args[0] == "help" { writeDoHelp(out); return 0 }
	if writeCommandHelp(out, args[0]) { return 0 }
	cliError(errOut, "CMD_UNKNOWN", "unknown command: "+args[0])
	io.WriteString(errOut, "run 'littlemake do --help' to list commands\n")
	return 2
}

// earlyAction selects an action requested before normal dispatch.
type earlyAction struct {
	Kind    int
	Command string
	Do      bool
}

const (
	actionNone = iota
	actionHelp
	actionVersion
)

// detectEarlyAction scans arguments before a "--" separator for -h/--help or
// -V/--version. It skips the value of every option that takes one, so a help
// or version token used as an option value is not mistaken for a request.
func detectEarlyAction(args []string) earlyAction {
	help := false
	version := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" { break }
		if arg == "-h" || arg == "--help" { help = true; continue }
		if arg == "-V" || arg == "--version" { version = true; continue }
		if optionTakesValue(arg) { i++; continue }
	}
	if help { return earlyAction{Kind: actionHelp, Command: helpTopic(args), Do: len(args) != 0 && args[0] == "do"} }
	if version { return earlyAction{Kind: actionVersion} }
	return earlyAction{}
}

// helpTopic returns the do command a help request refers to, if any.
func helpTopic(args []string) string {
	if len(args) > 1 && args[0] == "do" && args[1] != "help" && len(args[1]) != 0 && args[1][0] != '-' { return args[1] }
	return ""
}

// optionTakesValue reports whether arg is an option that consumes the next
// argument as its value. It covers the union of value options across the CLI.
func optionTakesValue(arg string) bool {
	return arg == "-f" || arg == "--file" ||
		arg == "-c" || arg == "--command" ||
		arg == "-C" || arg == "--directory" ||
		arg == "-j" || arg == "--jobs" ||
		arg == "--shell" || arg == "--env" ||
		arg == "--timeout" || arg == "--retry" || arg == "--log-limit" ||
		arg == "--lang" || arg == "--depth"
}

// handleHelpAndVersion writes help or version output when requested. It reports
// whether it handled the invocation so the caller can skip normal dispatch.
// Help takes precedence over version.
func handleHelpAndVersion(args []string, out io.Writer) (bool, int) {
	action := detectEarlyAction(args)
	if action.Kind == actionVersion { writeVersion(out); return true, 0 }
	if action.Kind == actionHelp {
		if action.Command != "" && writeCommandHelp(out, action.Command) { return true, 0 }
		if action.Do { writeDoHelp(out); return true, 0 }
		writeTopHelp(out); return true, 0
	}
	return false, 0
}
