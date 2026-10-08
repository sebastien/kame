package main

import (
	"kame/cli"
	"kame/core"
	"kame/host/posix"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type buildProgress struct {
	Active    int
	Completed int
	Failed    int
	Cancelled int
}

type cliOutcome struct {
	Node       int64
	Generation int64
	Kind       core.ResourceKind
	Name       string
	Started    bool
	Terminal   bool
	StartedNS  int64
	ElapsedMS  int64
	HasTiming  bool
	HostClock  bool
}

var outcomes []cliOutcome

func freeOutcomes() {
	for i := range outcomes {
		mem.FreeString(mem.System, outcomes[i].Name)
	}
	slices.Free(mem.System, outcomes)
	outcomes = nil
}

func observeOutcome(event program.Event, progress *buildProgress) {
	if event.Kind != program.TargetStarted && event.Kind != program.TargetCompleted && event.Kind != program.TargetFailed && event.Kind != program.TargetCancelled {
		return
	}
	index := -1
	for i := range outcomes {
		if outcomes[i].Node == event.NodeID && outcomes[i].Generation == event.Generation && outcomes[i].Kind == event.Key.Kind && outcomes[i].Name == event.Key.Name {
			index = i
			break
		}
	}
	if index < 0 {
		outcomes = slices.Append(mem.System, outcomes, cliOutcome{Node: event.NodeID, Generation: event.Generation, Kind: event.Key.Kind, Name: cloneCommandText(event.Key.Name), Started: event.Kind == program.TargetStarted})
		index = len(outcomes) - 1
		if event.Kind == program.TargetStarted {
			outcomes[index].StartedNS = presentationNow()
			outcomes[index].HostClock = event.HasMonotonic
			if event.HasMonotonic {
				outcomes[index].StartedNS = event.MonotonicNS
			}
			progress.Active++
			return
		}
	}
	if event.Kind == program.TargetStarted || outcomes[index].Terminal {
		return
	}
	outcomes[index].Terminal = true
	finished := presentationNow()
	if event.HasMonotonic {
		finished = event.MonotonicNS
	}
	if outcomes[index].Started && outcomes[index].HostClock == event.HasMonotonic && finished >= outcomes[index].StartedNS {
		outcomes[index].ElapsedMS = (finished - outcomes[index].StartedNS) / 1000000
		outcomes[index].HasTiming = true
	}
	if outcomes[index].Started && progress.Active != 0 {
		progress.Active--
	}
	if event.Kind == program.TargetCompleted {
		progress.Completed++
	}
	if event.Kind == program.TargetFailed {
		progress.Failed++
	}
	if event.Kind == program.TargetCancelled {
		progress.Cancelled++
	}
}

func writeTargetOutcome(out io.Writer, event program.Event, label string, token string) {
	clearDashboard(out)
	if selectedOutput == "ansi" {
		status := "✓"
		var timing = bytes.NewBuffer(mem.System, nil)
		if event.Kind == program.TargetFailed {
			status = "failed"
		} else if event.Kind == program.TargetCancelled {
			status = "cancelled"
		}
		for i := range outcomes {
			if outcomes[i].Node == event.NodeID && outcomes[i].Generation == event.Generation && outcomes[i].Kind == event.Key.Kind && outcomes[i].Name == event.Key.Name && outcomes[i].HasTiming {
				writeDuration(&timing, outcomes[i].ElapsedMS)
				io.WriteString(&timing, " - ")
				break
			}
		}
		io.WriteString(&timing, status)
		width := 0
		if posix.StderrIsTerminal() {
			width = posix.StderrWidth() - 2
		}
		writeOutcomeRow(out, event.Target, timing.String(), token, width)
		timing.Free()
		if event.Diagnostic.Code != "" {
			fmt.Fprintf(out, "  %s: %s", event.Diagnostic.Code, event.Diagnostic.Message)
			if event.Diagnostic.Cause.HasStatus {
				fmt.Fprintf(out, " (status %d)", event.Diagnostic.Cause.Status)
			}
			if event.Diagnostic.Cause.HasSignal {
				fmt.Fprintf(out, " (signal %d)", event.Diagnostic.Cause.Signal)
			}
			io.WriteString(out, "\n")
		}
		return
	}
	var line = bytes.NewBuffer(mem.System, nil)
	fmt.Fprintf(&line, "%s [%s]", label, event.Target)
	if event.Kind == program.TargetCompleted {
		io.WriteString(&line, " complete")
	} else if event.Kind == program.TargetFailed {
		io.WriteString(&line, " failed")
	} else {
		io.WriteString(&line, " cancelled")
	}
	if event.Diagnostic.Code != "" {
		fmt.Fprintf(&line, " %s: %s", event.Diagnostic.Code, event.Diagnostic.Message)
		if event.Diagnostic.Cause.HasStatus {
			fmt.Fprintf(&line, " (status %d)", event.Diagnostic.Cause.Status)
		}
		if event.Diagnostic.Cause.HasSignal {
			fmt.Fprintf(&line, " (signal %d)", event.Diagnostic.Cause.Signal)
		}
	}
	cli.Style(out, token, line.String(), diagnosticColor == "always")
	io.WriteString(out, "\n")
	line.Free()
}

func writeOutcomeRow(out io.Writer, target string, status string, token string, width int) {
	statusWidth := measureDisplayText(status, len(status)*2).Cells
	var subject = bytes.NewBuffer(mem.System, nil)
	io.WriteString(&subject, "[")
	if width >= 20 {
		writeTargetField(&subject, target, width-statusWidth-4)
	} else {
		io.WriteString(&subject, target)
	}
	io.WriteString(&subject, "]")
	writeStyledSubject(out, subject.String())
	cells := measureDisplayText(subject.String(), len(subject.String())*2).Cells
	padding := width - cells - statusWidth
	if padding < 1 {
		padding = 1
	}
	for i := 0; i < padding; i++ {
		io.WriteString(out, " ")
	}
	writeStatusField(out, status, token)
	io.WriteString(out, "\n")
	subject.Free()
}

func writeHumanSummary(out io.Writer, command string, status int, elapsedMS int64, counts buildProgress) {
	clearDashboard(out)
	if selectedOutput == "ansi" {
		label, token := "✓", "status.success"
		if status >= 128 || (counts.Cancelled != 0 && counts.Failed == 0 && status != 0) {
			label, token = "cancelled", "status.cancelled"
		} else if status != 0 && (command != "fmt" || invocationHadDiagnostic) {
			label, token = "failed", "status.failed"
		}
		width := 0
		if posix.StderrIsTerminal() {
			width = posix.StderrWidth() - 2
		}
		writeBuildTally(out, counts, 0, elapsedMS, label, token, width)
		return
	}
	label, token := "done ", "status.success"
	if status >= 128 || (counts.Cancelled != 0 && counts.Failed == 0 && status != 0) {
		label, token = "cancelled ", "status.cancelled"
	} else if status != 0 && (command != "fmt" || invocationHadDiagnostic) {
		label, token = "error ", "status.failed"
	}
	cli.Style(out, token, label, diagnosticColor == "always")
	unit := "targets"
	if command == "fmt" {
		unit = "files"
	}
	fmt.Fprintf(out, "%s · %d %s complete · %d failed · %d cancelled · %dms\n", command, counts.Completed, unit, counts.Failed, counts.Cancelled, elapsedMS)
}

func writeBuildTally(out io.Writer, counts buildProgress, running int, elapsedMS int64, status string, token string, width int) {
	var tally = bytes.NewBuffer(mem.System, nil)
	if width != 0 && width < 60 {
		fmt.Fprintf(&tally, "%d ok · %d failed", counts.Completed, counts.Failed)
	} else {
		fmt.Fprintf(&tally, "%d complete · %d failed", counts.Completed, counts.Failed)
		if running != 0 {
			fmt.Fprintf(&tally, " · %d running", running)
		}
		if counts.Cancelled != 0 {
			fmt.Fprintf(&tally, " · %d cancelled", counts.Cancelled)
		}
	}
	var elapsed = bytes.NewBuffer(mem.System, nil)
	writeDuration(&elapsed, elapsedMS)
	io.WriteString(&elapsed, " - "+status)
	rightWidth := measureDisplayText(elapsed.String(), len(elapsed.String())*2).Cells
	if width >= rightWidth+3 {
		writeDashboardPadded(out, tally.String(), width-rightWidth-2)
	} else {
		io.WriteString(out, tally.String())
	}
	io.WriteString(out, "  ")
	writeStatusField(out, elapsed.String(), token)
	io.WriteString(out, "\n")
	elapsed.Free()
	tally.Free()
}

func writeDuration(out io.Writer, elapsedMS int64) {
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	fmt.Fprintf(out, "%.1fs", float64(elapsedMS)/1000)
}

func writeStatusField(out io.Writer, field string, token string) {
	start := 0
	for i := 0; i+2 < len(field); i++ {
		if field[i:i+3] == " - " {
			start = i + 3
		}
	}
	io.WriteString(out, field[:start])
	cli.Style(out, token, field[start:], token != "" && diagnosticColor == "always")
}
