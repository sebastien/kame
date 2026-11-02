package main

import (
	"kame/cli"
	"kame/core"
	"kame/host/posix"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

var dashboardCommand string
var dashboardDryRun bool
var dashboardIdle bool
var dashboardSubject string
var dashboardCycle int
var dashboardCycleElapsed int64

var workers []cli.ProgressWorkerState
var dashboardRows int
var dashboardWidth int
var dashboardHeight int
var dashboardLive bool
var dashboardUnsafe bool
var dashboardPending bool
var dashboardStarted int64
var dashboardLast int64
var dashboardFrame string
var dashboardOutput io.Writer

func terminalDashboardAvailable() bool {
	environment := posix.Environment(mem.System)
	dumb := environmentValue(environment, "TERM") == "dumb"
	posix.FreeEnvironment(mem.System, environment)
	return posix.StderrIsTerminal() && !dumb && posix.StderrWidth() >= 40 && posix.StderrHeight() >= 4
}

func dashboardDimensions() (int, int) { return posix.StderrWidth(), posix.StderrHeight() }

// Observe actual dispatched target work, not every started dependency parent.
// A synchronous render can block inside Tick, so draw before dispatch begins.
func observeNativeWork(node *core.Node, target string, started bool) {
	if started && (!dashboardCanDraw() || dashboardOutput == nil) {
		return
	}
	state := cli.ProgressState{Workers: workers}
	generation, attempt := node.Generation, node.Attempt
	if !started {
		// Dispatch advances the live node's attempt (and may change generation).
		// Retire the identity captured before that synchronous call, not its result.
		for i := range workers {
			if workers[i].Internal && workers[i].Node == node.ID {
				generation, attempt = workers[i].Generation, workers[i].Attempt
				break
			}
		}
	}
	state.ObserveWork(mem.System, node.ID, generation, attempt, target, started, presentationNow())
	workers = state.Workers
	if !started {
		return
	}
	var frame = bytes.NewBuffer(mem.System, nil)
	// Activity changes cannot wait for a timer: dispatch itself may be a long,
	// synchronous render with no opportunity to refresh until it returns.
	dashboardLast = presentationNow() - 100000000
	redrawDashboard(&frame, &invocationCounts)
	publishDashboard(dashboardOutput, frame.Bytes())
	frame.Free()
	flushCLIOutput(dashboardOutput)
}

func clearDashboard(out io.Writer) {
	if dashboardRows != 0 && dashboardLive {
		width, height := dashboardDimensions()
		if width != dashboardWidth || height != dashboardHeight {
			// Reflow may have moved these rows into scrollback; never erase it.
			dashboardRows, dashboardUnsafe = 0, true
			io.WriteString(out, "\n")
			return
		}
	}
	for i := 0; i < dashboardRows; i++ {
		io.WriteString(out, "\x1b[1A\r\x1b[2K")
	}
	dashboardRows = 0
	mem.FreeString(mem.System, dashboardFrame)
	dashboardFrame = ""
}

func setDashboardSubject(subject string) {
	mem.FreeString(mem.System, dashboardSubject)
	dashboardSubject = cloneCommandText(subject)
}

func dashboardCanDraw() bool {
	width, height := dashboardDimensions()
	return dashboardLive && !dashboardUnsafe && !dashboardPending && width >= 40 && height >= 4
}

func observeWorker(event program.Event) {
	state := cli.ProgressState{Workers: workers, Outcomes: outcomes}
	state.ObserveWorker(mem.System, event, presentationNow())
	workers = state.Workers
}

func dashboardRaw(out io.Writer, data []byte, terminal bool) {
	if !terminal {
		return
	}
	clearDashboard(out)
	for i := range data {
		if data[i] == 27 || data[i] == '\r' {
			dashboardUnsafe = true
		}
	}
	if len(data) != 0 {
		dashboardPending = data[len(data)-1] != '\n'
	}
}

func redrawDashboard(out io.Writer, progress *buildProgress) {
	if dashboardRows != 0 {
		width, height := dashboardDimensions()
		if dashboardLive && (width != dashboardWidth || height != dashboardHeight) {
			clearDashboard(out)
			return
		}
	}
	if !dashboardCanDraw() {
		clearDashboard(out)
		return
	}
	if dashboardIdle && dashboardRows != 0 {
		return
	}
	width, height := dashboardDimensions()
	if width < 40 || height < 4 {
		clearDashboard(out)
		return
	}
	now := presentationNow()
	if dashboardRows != 0 && now-dashboardLast < 100000000 {
		return
	}
	dashboardLast = now
	var frame = bytes.NewBuffer(mem.System, nil)
	previousRows := dashboardRows
	renderDashboard(&frame, progress, width, height, now)
	paintDashboard(out, frame.String(), previousRows, dashboardRows)
	frame.Free()
}

// Cursor rests below the footer. Only changed rows are overwritten; unchanged
// worker assignments and separators never disappear between timer updates.
func paintDashboard(out io.Writer, frame string, previousRows int, rows int) {
	cli.WriteFrameUpdate(out, frame, dashboardFrame, previousRows, rows)
	mem.FreeString(mem.System, dashboardFrame)
	dashboardFrame = cloneCommandText(frame)
}

func publishDashboard(out io.Writer, data []byte) {
	if len(data) == 0 {
		return
	}
	if dashboardLive && !dashboardUnsafe {
		// Terminals supporting synchronized updates present the transaction in one
		// frame. Others still receive a single write, with no timed blank interval.
		var batch = bytes.NewBuffer(mem.System, nil)
		io.WriteString(&batch, "\x1b[?2026h")
		batch.Write(data)
		io.WriteString(&batch, "\x1b[?2026l")
		out.Write(batch.Bytes())
		batch.Free()
	} else {
		out.Write(data)
	}
}

// Layout takes explicit dimensions and time so golden tests need no terminal.
func renderDashboard(out io.Writer, progress *buildProgress, width int, height int, now int64) {
	dashboardWidth, dashboardHeight = width, height
	view := cli.ProgressView{Command: dashboardCommand, Subject: dashboardSubject, DryRun: dashboardDryRun, Idle: dashboardIdle, Cycle: dashboardCycle, Width: width, Height: height, Color: diagnosticColor == "always", Counts: cli.Progress{Active: progress.Active, Completed: progress.Completed, Failed: progress.Failed, Cancelled: progress.Cancelled}, ElapsedMS: (now - dashboardStarted) / 1000000}
	if dashboardIdle {
		view.ElapsedMS = dashboardCycleElapsed
	}
	state := cli.ProgressState{Workers: workers}
	view.Workers = state.WorkerView(mem.System, now)
	dashboardRows = cli.WriteDashboard(out, view)
	slices.Free(mem.System, view.Workers)
}

func writeStyledPadded(out io.Writer, text string, width int, token string) {
	cli.WriteProgressPadded(out, text, width, token, diagnosticColor == "always")
}

func writeDashboardField(out io.Writer, text string, width int) {
	cli.WriteProgressField(out, text, width)
}
