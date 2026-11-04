package main

import (
	"kame/cli"
	"kame/host/posix"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
)

var taskbarEnabled bool
var taskbarState int
var taskbarPercent int

func startTaskbar(out io.Writer) {
	environment := posix.Environment(mem.System)
	dumb := environmentValue(environment, "TERM") == "dumb"
	posix.FreeEnvironment(mem.System, environment)
	taskbarEnabled = !cliDiagnosticJSON && selectedOutput == "ansi" && !dashboardDryRun && (dashboardCommand == "build" || dashboardCommand == "run") && posix.StderrIsTerminal() && !dumb
	taskbarState, taskbarPercent = -1, 0
	if taskbarEnabled {
		emitTaskbar(out, 3, 0)
	}
}

func emitTaskbar(out io.Writer, state int, percent int) {
	if !taskbarEnabled || (taskbarState == state && taskbarPercent == percent) {
		return
	}
	fmt.Fprintf(out, "\x1b]9;4;%d;%d\x1b\\", state, percent)
	taskbarState, taskbarPercent = state, percent
}

func updateTaskbar(out io.Writer) {
	if !taskbarEnabled {
		return
	}
	if dashboardIdle {
		emitTaskbar(out, 0, 0)
		return
	}
	state := cli.ProgressState{Outcomes: outcomes}
	estimate := state.Estimate(presentationNow())
	percent := taskbarPercent
	if estimate.Known && estimate.Percent > percent {
		percent = estimate.Percent
	}
	if invocationCounts.Failed > 0 {
		emitTaskbar(out, 2, percent)
	} else if estimate.Known {
		emitTaskbar(out, 1, percent)
	} else {
		emitTaskbar(out, 3, percent)
	}
}

func finishTaskbar(out io.Writer, status int) {
	if taskbarEnabled && taskbarState != 0 {
		if status == 0 {
			emitTaskbar(out, 1, 100)
		} else if status >= 128 || (invocationCounts.Cancelled > 0 && invocationCounts.Failed == 0) {
			emitTaskbar(out, 4, taskbarPercent)
		} else {
			emitTaskbar(out, 2, taskbarPercent)
		}
		emitTaskbar(out, 0, 0)
	}
}
