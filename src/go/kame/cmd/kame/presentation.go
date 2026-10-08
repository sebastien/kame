package main

import (
	"kame/cli"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/time"
)

// Invocation policy and lifecycle shared by the native command handlers.
var selectedOutput = "ansi"
var invocationCounts buildProgress
var invocationHadDiagnostic bool
var stdoutColor bool
var formatHasResults bool
var presentationClock time.Time

func presentationNow() int64 { return int64(time.Since(presentationClock)) }

func applyInvocationPresentation(inv *cli.Invocation) {
	inv.Output, inv.JSON = selectedOutput, cliDiagnosticJSON
	inv.Color, inv.DiagnosticFormat = requestedDiagnosticColor, requestedDiagnosticFormat
}

func streamCommand(args []string) string {
	if len(args) > 0 && args[0] == "do" {
		if len(args) < 2 {
			return ""
		}
		command := args[1]
		if command == "run" || command == "render" || command == "cat" || command == "fmt" {
			return command
		}
		if len(args) > 2 && ((command == "cache" && args[2] == "clean") || (command == "tools" && args[2] == "check")) {
			return command
		}
		return ""
	}
	if cli.SelectsRun(args) || cli.AppendsCommands(args) {
		return "run"
	}
	if len(args) == 0 {
		candidates := []string{"Makefile.kmk", "make.kmk", "src/kmk/main.kmk"}
		found := false
		for i := range candidates {
			_, err := os.Stat(candidates[i])
			if err == nil {
				found = true
				break
			}
		}
		if !found {
			return ""
		}
	}
	return "build"
}

func hasDryRun(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] == "-n" || args[i] == "--dry-run" {
			return true
		}
		if cli.OptionTakesValue(args[i]) {
			i++
		}
	}
	return false
}

func tickCLIProgram(p *program.Program, wait int, errOut io.Writer, json bool) {
	if !json && dashboardCanDraw() {
		dashboardOutput, p.ObserveWork = errOut, observeNativeWork
	}
	p.Tick(wait)
	p.ObserveWork, dashboardOutput = nil, nil
}

func resetInvocationPresentation(out io.Writer) {
	_ = out
	invocationHadDiagnostic = false
	dashboardRows, dashboardLast = 0, 0
	mem.FreeString(mem.System, dashboardFrame)
	dashboardFrame = ""
	dashboardUnsafe, dashboardPending = false, false
	dashboardIdle = false
	dashboardDryRun = false
	dashboardCycle, dashboardCycleElapsed, dashboardCycleStatus = 0, 0, ""
	formatHasResults = false
	presentationClock = time.Now()
	dashboardStarted = 0
	dashboardLive = selectedOutput == "ansi" && !cliDiagnosticJSON && terminalDashboardAvailable()
}

func freeInvocationPresentation(out io.Writer) {
	clearDashboard(out)
	for i := range workers {
		mem.FreeString(mem.System, workers[i].Target)
		mem.FreeString(mem.System, workers[i].Program)
	}
	slices.Free(mem.System, workers)
	workers = nil
	mem.FreeString(mem.System, dashboardSubject)
	dashboardSubject = ""
	freeOutcomes()
}
