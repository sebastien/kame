package main

import (
	"solod.dev/so/io"
)

// version is the release reported by -V/--version.
const version = "0.1.0"

// topHelpBeforeCommands and topHelpAfterCommands bracket the command list,
// which is rendered from doCommands so dispatch and help share one registry.
const topHelpBeforeCommands = `kame - a modern build system in the spirit of GNU Make, with a Lisp-like language and a streaming incremental engine that should cover all your needs.

Usage:
  kame [OPTIONS] [TARGET...]
  kame do COMMAND [OPTIONS] [ARG...]
  kame -h | --help
  kame -V | --version

Builds TARGET using rules from a Kame source. With no TARGET, the
default target is built when defined; otherwise the available targets are
listed. Without -f/--file or -c/--command, source discovery tries
Makefile.kmk, then make.kmk, then src/kmk/main.kmk.

Build options:
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
  -j, --jobs N           maximum concurrent nodes (N > 0, default 1)
  -n, --dry-run          plan and render without executing effects
      --force            ignore freshness and cached-task hits
       --json             emit machine-readable JSON Lines
	      --color MODE       diagnostic colour: auto, always, or never
	      --diagnostic-format FORMAT  diagnostic presentation: human or plain
      --verbose          report cache decisions and warnings
      --shell SHELL      recipe shell executable (repeatable)
      --env NAME=VALUE   add a recipe environment entry (repeatable)
      --timeout MS       per-command timeout in milliseconds
      --retry N          retry failed commands N times
      --log-limit N      maximum captured bytes per step
  -h, --help             show this help
  -V, --version          show the version

Commands (kame do COMMAND):

`

const topHelpAfterCommands = `

Examples:
  kame                          build the default target
  kame build test               build several targets
  kame -f Build.kmk dist        use a specific build file
  kame do plan dist             inspect a target plan
  kame do fmt -i Makefile.kmk   format a build file in place
  kame do expr -c '(join ["a" "b"] ",")'
  kame --help                   show the overview
  kame --version                show the version

Run 'kame do COMMAND --help' for command-specific help.
`

const doHelpBeforeCommands = `kame do COMMAND [OPTIONS] [ARG...]

Utility commands for building, inspecting, and working with Kame
sources. Build execution still uses the primary invocation; these commands add
inspection and language tooling.

Commands:

`

const doHelpAfterCommands = `

Run 'kame do COMMAND --help' for command-specific help.
`

const runHelpText = `Usage: kame do run [OPTIONS] [TARGET...]

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
	      --color MODE       diagnostic colour: auto, always, or never
	      --diagnostic-format FORMAT  diagnostic presentation: human or plain
      --verbose          report cache decisions and warnings
      --shell SHELL      recipe shell executable (repeatable)
      --env NAME=VALUE   add a recipe environment entry (repeatable)
      --timeout MS       per-command timeout in milliseconds
      --retry N          retry failed commands N times
      --log-limit N      maximum captured bytes per step
  -h, --help             show this help
`

const planHelpText = `Usage: kame do plan [OPTIONS] TARGET...

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

const catHelpText = `Usage: kame do cat [OPTIONS] TARGET

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

const inputsHelpText = `Usage: kame do inputs [--depth N] [OPTIONS] TARGET

List the declared input paths of one TARGET as a JSON array. --depth 0 returns
no edges, 1 (the default) returns direct edges, and -1 is unlimited.

Options:
      --depth N          edge depth: -1, 0, or a positive integer
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
  -h, --help             show this help
`

const outputsHelpText = `Usage: kame do outputs [--depth N] [OPTIONS] TARGET

List the declared output paths of one TARGET as a JSON array. --depth 0 returns
no edges, 1 (the default) returns direct edges, and -1 is unlimited.

Options:
      --depth N          edge depth: -1, 0, or a positive integer
  -f, --file FILE        use one build file
  -c, --command TEXT     use inline build source
  -C, --directory DIR    set the working directory
  -h, --help             show this help
`

const spanHelpText = `Usage: kame do span [--expand] [--depth N] [OPTIONS] TARGET

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

const parseHelpText = `Usage: kame do parse --lang LANG [FILE]

Parse FILE, or stdin when FILE is omitted, and print a stable JSON AST. Source
spans are included; allocator and pointer details are not.

Options:
      --lang LANG   required: expr | template | rule | script
  -h, --help        show this help
`

const fmtHelpText = `Usage: kame do fmt [--lang LANG] [-i | -n] [FILE...]

Format source to stdout, or replace each FILE. With no FILE, read stdin (only
without -i or -n). -n lists files that would change and exits 1 when any differ.

Options:
      --lang LANG   expr | template | rule | script (default script)
  -i                replace files in place
  -n                check for differences without writing
  -h, --help        show this help
`

const exprHelpText = `Usage: kame do expr [-c TEXT | FILE] [OPTIONS] [-- ARG...]

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

type commandAction int

const (
	commandRun commandAction = iota
	commandPlan
	commandCat
	commandInputs
	commandOutputs
	commandSpan
	commandParse
	commandFormat
	commandExpr
	commandHelp
)

type commandSpec struct {
	Name       string
	TopSummary string
	DoSummary  string
	Help       string
	Action     commandAction
}

var doCommands = []commandSpec{
	{Name: "run", TopSummary: "materialize targets and stream all execution events", DoSummary: "materialize targets and stream all execution events", Help: runHelpText, Action: commandRun},
	{Name: "plan", TopSummary: "print the resolved plan without executing", DoSummary: "print the resolved plan without executing", Help: planHelpText, Action: commandPlan},
	{Name: "cat", TopSummary: "materialize one target and print its artifact", DoSummary: "materialize one target and print its artifact", Help: catHelpText, Action: commandCat},
	{Name: "inputs", TopSummary: "list declared input paths", DoSummary: "list declared input paths (--depth N)", Help: inputsHelpText, Action: commandInputs},
	{Name: "outputs", TopSummary: "list declared output paths", DoSummary: "list declared output paths (--depth N)", Help: outputsHelpText, Action: commandOutputs},
	{Name: "span", TopSummary: "show transitive inputs and outputs", DoSummary: "show transitive inputs and outputs (--expand, --depth N)", Help: spanHelpText, Action: commandSpan},
	{Name: "parse", TopSummary: "parse a language file and print a JSON AST", DoSummary: "parse a language file and print a JSON AST (--lang LANG)", Help: parseHelpText, Action: commandParse},
	{Name: "fmt", TopSummary: "format source in place or check it", DoSummary: "format source in place (-i) or check it (-n)", Help: fmtHelpText, Action: commandFormat},
	{Name: "expr", TopSummary: "evaluate a standalone expression", DoSummary: "evaluate a standalone expression with capability grants", Help: exprHelpText, Action: commandExpr},
	{Name: "help", DoSummary: "show this help, or help for one COMMAND", Action: commandHelp},
}

func writeCommandList(out io.Writer, top bool) {
	for i := range doCommands {
		summary := doCommands[i].DoSummary
		if top {
			summary = doCommands[i].TopSummary
		}
		if summary == "" {
			continue
		}
		io.WriteString(out, "  ")
		io.WriteString(out, doCommands[i].Name)
		for n := len(doCommands[i].Name); n < 8; n++ {
			io.WriteString(out, " ")
		}
		io.WriteString(out, " ")
		io.WriteString(out, summary)
		io.WriteString(out, "\n")
	}
}

func writeTopHelp(out io.Writer) {
	io.WriteString(out, topHelpBeforeCommands)
	writeCommandList(out, true)
	io.WriteString(out, topHelpAfterCommands)
}
func writeDoHelp(out io.Writer) {
	io.WriteString(out, doHelpBeforeCommands)
	writeCommandList(out, false)
	io.WriteString(out, doHelpAfterCommands)
}
func writeVersion(out io.Writer) {
	io.WriteString(out, "kame ")
	io.WriteString(out, version)
	io.WriteString(out, "\n")
}

func findCommand(name string) *commandSpec {
	for i := range doCommands {
		if doCommands[i].Name == name {
			return &doCommands[i]
		}
	}
	return nil
}

// writeCommandHelp prints help for one do command and reports whether the
// command is known.
func writeCommandHelp(out io.Writer, command string) bool {
	spec := findCommand(command)
	if spec == nil {
		return false
	}
	if spec.Name == "help" {
		writeDoHelp(out)
		return true
	}
	io.WriteString(out, spec.Help)
	return true
}

// runHelpCommand implements 'kame do help [COMMAND]'.
func runHelpCommand(args []string, out io.Writer, errOut io.Writer) int {
	if len(args) != 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 || args[0] == "help" {
		writeDoHelp(out)
		return 0
	}
	if writeCommandHelp(out, args[0]) {
		return 0
	}
	cliError(errOut, "CMD_UNKNOWN", "unknown command: "+args[0])
	io.WriteString(errOut, "run 'kame do --help' to list commands\n")
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
		if arg == "--" {
			break
		}
		if arg == "-h" || arg == "--help" {
			help = true
			continue
		}
		if arg == "-V" || arg == "--version" {
			version = true
			continue
		}
		if optionTakesValue(arg) {
			i++
			continue
		}
	}
	if help {
		return earlyAction{Kind: actionHelp, Command: helpTopic(args), Do: len(args) != 0 && args[0] == "do"}
	}
	if version {
		return earlyAction{Kind: actionVersion}
	}
	return earlyAction{}
}

// helpTopic returns the do command a help request refers to, if any.
func helpTopic(args []string) string {
	if len(args) > 1 && args[0] == "do" && args[1] != "help" && len(args[1]) != 0 && args[1][0] != '-' {
		return args[1]
	}
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
	if action.Kind == actionVersion {
		writeVersion(out)
		return true, 0
	}
	if action.Kind == actionHelp {
		if action.Command != "" && writeCommandHelp(out, action.Command) {
			return true, 0
		}
		if action.Do {
			writeDoHelp(out)
			return true, 0
		}
		writeTopHelp(out)
		return true, 0
	}
	return false, 0
}
