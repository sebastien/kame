// Command littlemake provides the native LittleMake command-line interface.
package main

import (
	"littlemake/host/posix"
	"solod.dev/so/io"
	"solod.dev/so/os"
)

func main() { posix.InstallSignals(); os.Exit(Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// Run executes a command with explicit streams so the command behavior remains
// independently testable from process startup.
func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if handled, status := handleHelpAndVersion(args, out); handled {
		return status
	}
	if len(args) != 0 && args[0] == "do" {
		if len(args) == 1 {
			writeDoHelp(out)
			return 0
		}
		if spec := findCommand(args[1]); spec != nil { return runDoCommand(spec.Action, args[2:], in, out, errOut) }
		cliError(errOut, "CMD_UNKNOWN", "unknown command: "+args[1])
		io.WriteString(errOut, "run 'littlemake do --help' to list commands\n")
		return 2
	}
	return runBuild(args, out, errOut, false)
}

func runDoCommand(action commandAction, args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if action == commandRun { return runBuild(args, out, errOut, true) }
	if action == commandPlan { return runPlan(args, out, errOut) }
	if action == commandCat { return runCat(args, out, errOut) }
	if action == commandInputs { return runGraph(args, out, errOut, "inputs") }
	if action == commandOutputs { return runGraph(args, out, errOut, "outputs") }
	if action == commandSpan { return runGraph(args, out, errOut, "span") }
	if action == commandParse { return runParse(args, in, out, errOut) }
	if action == commandFormat { return runFormat(args, in, out, errOut) }
	if action == commandExpr { return runExpr(args, in, out, errOut) }
	return runHelpCommand(args, out, errOut)
}
