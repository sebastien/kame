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
		if args[1] == "help" {
			return runHelpCommand(args[2:], out, errOut)
		}
		if args[1] == "parse" {
			return runParse(args[2:], in, out, errOut)
		}
		if args[1] == "fmt" {
			return runFormat(args[2:], in, out, errOut)
		}
		if args[1] == "plan" {
			return runPlan(args[2:], out, errOut)
		}
		if args[1] == "cat" {
			return runCat(args[2:], out, errOut)
		}
		if args[1] == "inputs" {
			return runGraph(args[2:], out, errOut, "inputs")
		}
		if args[1] == "outputs" {
			return runGraph(args[2:], out, errOut, "outputs")
		}
		if args[1] == "span" {
			return runGraph(args[2:], out, errOut, "span")
		}
		if args[1] == "expr" {
			return runExpr(args[2:], in, out, errOut)
		}
		if args[1] == "run" {
			return runBuild(args[2:], out, errOut, true)
		}
		cliError(errOut, "CMD_UNKNOWN", "unknown command: "+args[1])
		io.WriteString(errOut, "run 'littlemake do --help' to list commands\n")
		return 2
	}
	return runBuild(args, out, errOut, false)
}
