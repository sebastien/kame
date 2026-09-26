// Command littlemake provides the native LittleMake command-line interface.
package main

import (
	"littlemake/host/posix"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
)

func main() { posix.InstallSignals(); os.Exit(Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// Run executes a command with explicit streams so the command behavior remains
// independently testable from process startup.
func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	requestedDiagnosticColor = "auto"
	requestedDiagnosticFormat = "plain"
	cliDiagnosticOut, cliDiagnosticJSON = out, requestsJSONDiagnostics(args)
	configureDiagnosticPresentation(buildArguments{})
	presentation := extractDiagnosticPresentation(args, errOut)
	if !presentation.OK {
		return 2
	}
	defer freePresentationArgs(presentation.Args)
	args = presentation.Args
	if handled, status := handleHelpAndVersion(args, out); handled {
		return status
	}
	if len(args) != 0 && args[0] == "do" {
		if len(args) == 1 {
			writeDoHelp(out)
			return 0
		}
		if spec := findCommand(args[1]); spec != nil {
			return runDoCommand(spec.Action, args[2:], in, out, errOut)
		}
		cliError(errOut, "CMD_UNKNOWN", "unknown command: "+args[1])
		io.WriteString(errOut, "run 'littlemake do --help' to list commands\n")
		return 2
	}
	return runBuild(args, out, errOut, false)
}

// extractDiagnosticPresentation accepts presentation controls for every
// command, leaving command parsers focused on execution options. Tokens after
// -- remain positional data.
type presentationArguments struct {
	Args []string
	OK   bool
}

func extractDiagnosticPresentation(args []string, errOut io.Writer) presentationArguments {
	var clean []string
	afterOptions := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterOptions {
			clean = slices.Append(mem.System, clean, arg)
			continue
		}
		if arg == "--" {
			afterOptions = true
			clean = slices.Append(mem.System, clean, arg)
			continue
		}
		name, value, hasValue := "", "", false
		if arg == "--color" || arg == "--diagnostic-format" {
			if i+1 == len(args) {
				cliError(errOut, "OPT_NO_VALUE", "missing value for "+arg)
				freePresentationArgs(clean)
				return presentationArguments{}
			}
			name, i, value, hasValue = arg, i+1, args[i+1], true
		} else if len(arg) >= 8 && arg[:8] == "--color=" {
			name, value, hasValue = "--color", arg[8:], true
		} else if len(arg) >= 20 && arg[:20] == "--diagnostic-format=" {
			name, value, hasValue = "--diagnostic-format", arg[20:], true
		}
		if !hasValue {
			clean = slices.Append(mem.System, clean, arg)
			continue
		}
		if name == "--color" {
			if value != "auto" && value != "always" && value != "never" {
				cliError(errOut, "OPT_VALUE_INVALID", "color must be auto, always, or never")
				freePresentationArgs(clean)
				return presentationArguments{}
			}
			setDiagnosticColor(value)
		} else if value != "human" && value != "plain" {
			cliError(errOut, "OPT_VALUE_INVALID", "diagnostic format must be human or plain")
			freePresentationArgs(clean)
			return presentationArguments{}
		} else {
			setDiagnosticFormat(value)
		}
	}
	configureDiagnosticPresentation(buildArguments{})
	return presentationArguments{Args: clean, OK: true}
}

func requestsJSONDiagnostics(args []string) bool {
	for i := range args {
		if args[i] == "--" {
			return false
		}
		if args[i] == "--json" {
			return true
		}
	}
	return false
}

func freePresentationArgs(args []string) {
	if len(args) != 0 {
		slices.Free(mem.System, args)
	}
}

func runDoCommand(action commandAction, args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if action == commandRun {
		return runBuild(args, out, errOut, true)
	}
	if action == commandPlan {
		return runPlan(args, out, errOut)
	}
	if action == commandCat {
		return runCat(args, out, errOut)
	}
	if action == commandInputs {
		return runGraph(args, out, errOut, "inputs")
	}
	if action == commandOutputs {
		return runGraph(args, out, errOut, "outputs")
	}
	if action == commandSpan {
		return runGraph(args, out, errOut, "span")
	}
	if action == commandParse {
		return runParse(args, in, out, errOut)
	}
	if action == commandFormat {
		return runFormat(args, in, out, errOut)
	}
	if action == commandExpr {
		return runExpr(args, in, out, errOut)
	}
	return runHelpCommand(args, out, errOut)
}
