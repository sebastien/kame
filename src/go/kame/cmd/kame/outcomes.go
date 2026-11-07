package main

import (
	"kame/cli"
	"kame/host/posix"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
)

type buildProgress = cli.Progress

var outcomes []cli.Outcome

func freeOutcomes() {
	state := cli.ProgressState{Outcomes: outcomes}
	state.FreeOutcomes(mem.System)
	outcomes = state.Outcomes
}

func observeOutcome(event program.Event, progress *buildProgress) {
	state := cli.ProgressState{Outcomes: outcomes, Counts: *progress}
	state.ObserveOutcome(mem.System, event, presentationNow())
	outcomes = state.Outcomes
	*progress = state.Counts
}

func writeTargetOutcome(out io.Writer, event program.Event) {
	clearDashboard(out)
	outcome := "complete"
	if event.Kind == program.TargetCompleted {
		outcome = "complete"
	} else if event.Kind == program.TargetFailed {
		outcome = "failed"
	} else {
		outcome = "cancelled"
	}
	state := cli.ProgressState{Outcomes: outcomes}
	timing := state.Timing(event)
	width := 0
	if posix.StderrIsTerminal() {
		width = posix.StderrWidth() - 2
	}
	cli.WriteHumanOutcome(out, event.Target, outcome, selectedOutput == "ansi", timing.ElapsedMS, timing.HasTiming, width, diagnosticColor == "always")
}

func writeHumanSummary(out io.Writer, command string, status int, elapsedMS int64, counts buildProgress) {
	clearDashboard(out)
	width := 0
	if posix.StderrIsTerminal() {
		width = posix.StderrWidth() - 2
	}
	cli.WriteHumanSummary(out, command, status, elapsedMS, counts, selectedOutput == "ansi", invocationHadDiagnostic, width, diagnosticColor == "always")
}
