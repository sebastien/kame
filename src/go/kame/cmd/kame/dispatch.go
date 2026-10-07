// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/cli"
	"kame/host/posix"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/time"
)

func main() { posix.InstallSignals(); os.Exit(Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// Run executes a command with explicit streams so the command behavior remains
// independently testable from process startup.
func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	presentation := cli.Presentation(args)
	selectedOutput = presentation.Output
	requestedDiagnosticColor, requestedDiagnosticFormat = presentation.Color, presentation.DiagnosticFormat
	cliDiagnosticOut, cliDiagnosticJSON = out, presentation.JSON
	configureDiagnosticPresentation(buildArguments{})
	defer presentation.Free()
	if !presentation.OK {
		cliError(errOut, presentation.Error.Code, presentation.Error.Message)
		return 2
	}
	if handled, status := handleHelpAndVersion(presentation, out); handled {
		return status
	}
	args = presentation.Args
	invocationCounts = buildProgress{}
	resetDashboard(errOut)
	defer freeDashboard(errOut)
	startedAt := time.Now()
	command := streamCommand(args)
	dashboardCommand = command
	dryRun := (command == "build" || command == "run") && hasDryRun(args)
	dashboardDryRun = dryRun
	if command != "" && cliDiagnosticJSON {
		program.WriteInvocation(out, command, dryRun)
	} else if dryRun {
		io.WriteString(errOut, "info "+command+": dry-run · no effects or processes\n")
	}
	document := mem.Alloc[bytes.Buffer](mem.System)
	*document = bytes.NewBuffer(mem.System, nil)
	defer mem.Free(mem.System, document)
	defer document.Free()
	static := cliDiagnosticJSON && command == "" && len(args) > 1 && args[0] == "do" && args[1] != "plan"
	destination := out
	if static {
		destination = document
		cliDiagnosticOut = destination
	}
	status := runCommand(args, in, destination, errOut)
	if static {
		writeStaticDocument(out, document.String(), status != 0)
		cliDiagnosticOut = out
	}
	clearDashboard(errOut)
	if command != "" && cliDiagnosticJSON {
		count := -1
		if command == "build" || command == "run" || command == "fmt" {
			count = invocationCounts.Completed
		}
		program.WriteSummary(out, command, status, command == "fmt" && status == 1 && !invocationHadDiagnostic, int64(time.Since(startedAt))/1000000, count, invocationCounts.Failed, invocationCounts.Cancelled)
	}
	if !cliDiagnosticJSON && command != "" && (command != "fmt" || formatHasResults) && (invocationCounts.Completed+invocationCounts.Failed+invocationCounts.Cancelled != 0) {
		writeHumanSummary(errOut, command, status, int64(time.Since(startedAt))/1000000, invocationCounts)
	}
	return status
}

func writeStaticDocument(out io.Writer, text string, failed bool) {
	lines := 0
	for i := range text {
		if text[i] == '\n' {
			lines++
		}
	}
	if !failed || lines <= 1 {
		io.WriteString(out, text)
		return
	}
	io.WriteString(out, "[")
	start, count := 0, 0
	for i := range text {
		if text[i] != '\n' {
			continue
		}
		if i > start {
			if count != 0 {
				io.WriteString(out, ",")
			}
			io.WriteString(out, text[start:i])
			count++
		}
		start = i + 1
	}
	io.WriteString(out, "]\n")
}

func runCommand(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if len(args) != 0 && args[0] == "do" {
		if len(args) == 1 {
			writeDoHelp(out)
			return 0
		}
		if spec := findCommand(args[1]); spec != nil {
			return runDoCommand(spec.Action, args[2:], in, out, errOut)
		}
		message := cli.RemovedCommandMessage(args[1])
		if message == "" {
			message = "unknown command: " + args[1]
		}
		cliError(errOut, "CMD_UNKNOWN", message)
		if !cliDiagnosticJSON {
			io.WriteString(errOut, "help: run 'kame do --help' to list commands\n")
		}
		return 2
	}
	if cli.SelectsRun(args) || cli.AppendsCommands(args) {
		return runPrimarySession(args, in, out, errOut)
	}
	return runBuild(args, out, errOut, false)
}

func runDoCommand(action commandAction, args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if action == commandRender {
		return runRender(args, in, out, errOut)
	}
	if action == commandRun {
		return runSession(args, in, out, errOut)
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
	if action == commandTools {
		return runTools(args, out, errOut)
	}
	if action == commandCache {
		return runCache(args, out, errOut)
	}
	if action == commandParse {
		return runParse(args, in, out, errOut)
	}
	if action == commandFormat {
		return runFormat(args, in, out, errOut)
	}
	return runHelpCommand(args, out, errOut)
}

func runRender(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	inv := cli.Parse("render", args)
	applyInvocationPresentation(&inv)
	defer inv.Free()
	return runParsedSession(inv, in, out, errOut)
}
