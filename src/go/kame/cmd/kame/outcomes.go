package main

import (
	"kame/cli"
	"kame/host/posix"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
)

type buildProgress struct {
	Active    int
	Completed int
	Failed    int
	Cancelled int
}

var outcomes []cli.Outcome

func freeOutcomes() {
	state := cli.ProgressState{Outcomes: outcomes}
	state.FreeOutcomes(mem.System)
	outcomes = state.Outcomes
}

func observeOutcome(event program.Event, progress *buildProgress) {
	state := cli.ProgressState{Outcomes: outcomes, Counts: cli.Progress{Active: progress.Active, Completed: progress.Completed, Failed: progress.Failed, Cancelled: progress.Cancelled}}
	state.ObserveOutcome(mem.System, event, presentationNow())
	outcomes = state.Outcomes
	*progress = buildProgress{Active: state.Counts.Active, Completed: state.Counts.Completed, Failed: state.Counts.Failed, Cancelled: state.Counts.Cancelled}
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
	cli.WriteHumanSummary(out, command, status, elapsedMS, cli.Progress{Active: counts.Active, Completed: counts.Completed, Failed: counts.Failed, Cancelled: counts.Cancelled}, selectedOutput == "ansi", invocationHadDiagnostic, width, diagnosticColor == "always")
}

func writeDuration(out io.Writer, elapsedMS int64) {
	cli.WriteDuration(out, elapsedMS)
}
