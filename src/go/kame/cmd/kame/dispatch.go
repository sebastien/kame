// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/cli"
	"kame/host/posix"
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
	previousSources := diagnosticSources
	diagnosticSources = nil
	defer restoreDiagnosticSources(previousSources)
	outputState := mem.Alloc[nativeOutputState](mem.System)
	defer mem.Free(mem.System, outputState)
	previousOutput := activeNativeOutput
	activeNativeOutput = outputState
	defer restoreNativeOutput(previousOutput)
	stdout := mem.Alloc[nativeOutputWriter](mem.System)
	stderr := mem.Alloc[nativeOutputWriter](mem.System)
	defer mem.Free(mem.System, stdout)
	defer mem.Free(mem.System, stderr)
	stdout.State, stdout.FD = outputState, nativeOutputFD(out)
	stderr.State, stderr.FD = outputState, nativeOutputFD(errOut)
	if stdout.FD != 0 {
		out = stdout
	}
	if stderr.FD != 0 {
		errOut = stderr
	}
	previousDiagnosticOut := cliDiagnosticOut
	defer restoreDiagnosticOut(previousDiagnosticOut)
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
	resetInvocationPresentation()
	defer freeInvocationPresentation(errOut)
	startedAt := time.Now()
	command := streamCommand(args)
	dashboardCommand = command
	lockOut, lockErr, lockWaiting, lockLastNotice = out, errOut, false, 0
	ownerCommand := lockCommand(args, command)
	posix.ObserveLocks(observeLock, ownerCommand)
	mem.FreeString(mem.System, ownerCommand)
	defer finishLockPresentation()
	dryRun := (command == "build" || command == "run" || command == "cache") && hasDryRun(args)
	dashboardDryRun = dryRun
	if command != "" && cliDiagnosticJSON {
		cli.WriteInvocation(out, command, dryRun)
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
	// A signal can arrive inside a bounded write after the evaluator's last
	// signal check. Cleanup remains shielded, but the invocation keeps its exit.
	if outputState.Bounded {
		if signal := posix.TakeSignal(); signal != 0 {
			outputState.Signal = signal
		}
		if outputState.Signal < 0 {
			status = 128 - outputState.Signal
		} else if outputState.Signal != 0 {
			status = 128 + outputState.Signal
		}
	}
	if status == 0 && outputState.FailureCode != "" {
		cliError(errOut, outputState.FailureCode, "cleanup output could not be delivered before scope completion")
		status = 1
	}
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
		cli.WriteSummary(out, command, status, command == "fmt" && status == 1 && !invocationHadDiagnostic, int64(time.Since(startedAt))/1000000, count, invocationCounts.Failed, invocationCounts.Cancelled)
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
		if isDoCommand(args[1]) {
			if !validateCommandDirectory(args[1], args[2:], errOut) {
				return 1
			}
			return runDoCommand(args[1], args[2:], in, out, errOut)
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
	if !validateCommandDirectory("", args, errOut) {
		return 1
	}
	if cli.SelectsRun(args) || cli.AppendsCommands(args) {
		return runPrimarySession(args, in, out, errOut)
	}
	return runBuild(args, out, errOut)
}

// Check directory selection once for every native command, before source reads
// or effects. Leave syntax diagnostics to the command's normal parser/handler.
func validateCommandDirectory(command string, args []string, errOut io.Writer) bool {
	inv := cli.Parse(command, args)
	defer inv.Free()
	if !inv.OK || command == "help" {
		return true
	}
	info, err := os.Stat(inv.Directory)
	if err != nil || !info.IsDir() {
		cliError(errOut, "FS_ERR", "cannot use working directory: "+inv.Directory)
		return false
	}
	return true
}

func runDoCommand(command string, args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if command == "render" {
		return runRender(args, in, out, errOut)
	}
	if command == "run" {
		return runSession(args, in, out, errOut)
	}
	if command == "plan" {
		return runPlan(args, out, errOut)
	}
	if command == "list" {
		return runList(args, out, errOut)
	}
	if command == "pick" {
		return runPick(args, out, errOut)
	}
	if command == "cat" {
		return runCat(args, out, errOut)
	}
	if command == "inputs" {
		return runGraph(args, out, errOut, "inputs")
	}
	if command == "outputs" {
		return runGraph(args, out, errOut, "outputs")
	}
	if command == "span" {
		return runGraph(args, out, errOut, "span")
	}
	if command == "tools" {
		return runTools(args, out, errOut)
	}
	if command == "cache" {
		return runCache(args, out, errOut)
	}
	if command == "clean" {
		return runClean(args, out, errOut)
	}
	if command == "parse" {
		return runParse(args, in, out, errOut)
	}
	if command == "fmt" {
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
