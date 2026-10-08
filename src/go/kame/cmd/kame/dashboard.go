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

var dashboardCommand string
var dashboardDryRun bool
var dashboardIdle bool
var dashboardSubject string
var dashboardCycle int
var dashboardCycleStatus string
var dashboardCycleElapsed int64

type cliWorker struct {
	Node       int64
	Target     string
	Program    string
	Request    int64
	Generation int64
	Attempt    int64
	Started    int64
	Ready      bool
	State      string
	Internal   bool
}

var workers []cliWorker
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
	if !started {
		for i := range workers {
			if workers[i].Internal && workers[i].Node == node.ID {
				mem.FreeString(mem.System, workers[i].Target)
				mem.FreeString(mem.System, workers[i].Program)
				workers[i] = cliWorker{}
			}
		}
		return
	}
	if !dashboardCanDraw() || dashboardOutput == nil {
		return
	}
	for i := range workers {
		if workers[i].Target != "" && workers[i].Node == node.ID {
			return
		}
	}
	observeWorker(program.Event{Kind: program.ProcessStarted, NodeID: node.ID, Target: target, Program: "kame", Generation: node.Generation, Attempt: node.Attempt})
	for j := range workers {
		if workers[j].Node == node.ID && workers[j].Target != "" {
			workers[j].Internal, workers[j].State = true, "evaluating"
		}
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
	if event.Kind == program.ProcessStarted && event.Target != "" {
		for i := range workers {
			if workers[i].Node == event.NodeID && workers[i].Target == event.Target && workers[i].Request == event.RequestID && workers[i].Generation == event.Generation && workers[i].Attempt == event.Attempt && workers[i].Program == event.Program {
				return
			}
		}
		for i := range workers {
			if workers[i].Target != "" {
				continue
			}
			workers[i] = cliWorker{Node: event.NodeID, Target: cloneCommandText(event.Target), Program: cloneCommandText(event.Program), Request: event.RequestID, Generation: event.Generation, Attempt: event.Attempt, Started: presentationNow()}
			return
		}
		workers = slices.Append(mem.System, workers, cliWorker{Node: event.NodeID, Target: cloneCommandText(event.Target), Program: cloneCommandText(event.Program), Request: event.RequestID, Generation: event.Generation, Attempt: event.Attempt, Started: presentationNow()})
	}
	for i := range workers {
		worker := &workers[i]
		if worker.Target == "" || worker.Node != event.NodeID || worker.Target != event.Target || worker.Generation != event.Generation || event.Attempt < worker.Attempt {
			continue
		}
		if event.Kind == program.ServiceState {
			worker.Ready = event.State == "ready" || event.State == "checking-health"
			worker.State = ""
			if event.State == "stopping" {
				worker.State = "stopping"
			} else if event.State == "restarting" {
				worker.State = "retrying"
			}
		}
		if (event.Kind == program.ProcessExited && (event.RequestID == 0 || worker.Request == event.RequestID)) || event.Kind == program.TargetCompleted || event.Kind == program.TargetFailed || event.Kind == program.TargetCancelled {
			mem.FreeString(mem.System, worker.Target)
			mem.FreeString(mem.System, worker.Program)
			*worker = cliWorker{}
		}
	}
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
	var update = bytes.NewBuffer(mem.System, nil)
	if previousRows == rows {
		for i := 0; i < rows; i++ {
			line := dashboardLine(frame, i)
			if line == dashboardLine(dashboardFrame, i) {
				continue
			}
			fmt.Fprintf(&update, "\x1b[%dA\r", rows-i)
			io.WriteString(&update, line)
			io.WriteString(&update, "\x1b[K")
			fmt.Fprintf(&update, "\x1b[%dB\r", rows-i)
		}
	} else {
		if previousRows != 0 {
			fmt.Fprintf(&update, "\x1b[%dA\r", previousRows)
			count := rows
			if previousRows > count {
				count = previousRows
			}
			for i := 0; i < count; i++ {
				io.WriteString(&update, dashboardLine(frame, i))
				io.WriteString(&update, "\x1b[K\n")
			}
			if previousRows > rows {
				fmt.Fprintf(&update, "\x1b[%dA\r", previousRows-rows)
			}
		} else {
			io.WriteString(&update, frame)
		}
	}
	mem.FreeString(mem.System, dashboardFrame)
	dashboardFrame = cloneCommandText(frame)
	out.Write(update.Bytes())
	update.Free()
}

func dashboardLine(frame string, index int) string {
	start := 0
	for i := range frame {
		if frame[i] != '\n' {
			continue
		}
		if index == 0 {
			return frame[start:i]
		}
		index--
		start = i + 1
	}
	return ""
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
	dashboardRows = 0
	dashboardWidth, dashboardHeight = width, height
	running, ready, hidden := 0, 0, 0
	for i := range workers {
		if workers[i].Target == "" {
			continue
		}
		if workers[i].Ready {
			ready++
		} else {
			running++
		}
	}
	var heading = bytes.NewBuffer(mem.System, nil)
	io.WriteString(&heading, dashboardCommand)
	if dashboardDryRun {
		io.WriteString(&heading, " dry-run")
	}
	if dashboardCycle != 0 {
		fmt.Fprintf(&heading, " · cycle %d", dashboardCycle)
	}
	fmt.Fprintf(&heading, " · workers: %d", running)
	var bounded = bytes.NewBuffer(mem.System, nil)
	writeDashboardField(&bounded, heading.String(), width-5)
	io.WriteString(out, "── ")
	cli.Style(out, "text.muted", bounded.String(), diagnosticColor == "always")
	io.WriteString(out, "\n")
	bounded.Free()
	heading.Free()
	dashboardRows++
	capacity := height - 3
	if dashboardSubject != "" {
		capacity--
	}
	if running > capacity || ready != 0 {
		capacity--
	}
	visible := 0
	for i := range workers {
		worker := workers[i]
		// Empty historical slots must not consume space ahead of active work.
		if worker.Target == "" || worker.Ready {
			continue
		}
		if visible >= capacity {
			hidden++
			continue
		}
		visible++
		// Bounded row text never drives the terminal's automatic line wrapping.
		var slot = bytes.NewBuffer(mem.System, nil)
		fmt.Fprintf(&slot, "  %d  ", i+1)
		cli.Style(out, "text.muted", slot.String(), diagnosticColor == "always")
		var target = bytes.NewBuffer(mem.System, nil)
		io.WriteString(&target, "[")
		writeTargetField(&target, worker.Target, width/2-10)
		io.WriteString(&target, "]")
		writeStyledSubject(out, target.String())
		for j := measureDisplayText(target.String(), len(target.String())*2).Cells; j < width/2-8; j++ {
			io.WriteString(out, " ")
		}
		target.Free()
		state := "running"
		if worker.State != "" {
			state = worker.State
		}
		io.WriteString(out, "  ")
		writeStyledPadded(out, state, 11, "status.running")
		used := len(slot.String()) + (width/2 - 8) + 2 + 11
		if width >= 80 {
			io.WriteString(out, "  ")
			program := worker.Program
			if program == "" {
				program = "process"
			}
			writeStyledPadded(out, program, width/4-8, "worker.tool")
			used += 2 + (width/4 - 8)
			// Engine attempts also count evaluator resumption, not just retries.
		}
		var elapsed = bytes.NewBuffer(mem.System, nil)
		writeDuration(&elapsed, (now-worker.Started)/1000000)
		remaining := width - 2 - used
		for j := len(elapsed.String()); j < remaining; j++ {
			io.WriteString(out, " ")
		}
		writeDashboardField(out, elapsed.String(), remaining)
		elapsed.Free()
		slot.Free()
		io.WriteString(out, "\n")
		dashboardRows++
	}
	if hidden != 0 || ready != 0 {
		var extra = bytes.NewBuffer(mem.System, nil)
		fmt.Fprintf(&extra, "%d hidden active · %d ready services", hidden, ready)
		writeDashboardField(out, extra.String(), width-2)
		io.WriteString(out, "\n")
		extra.Free()
		dashboardRows++
	}
	elapsedMS := (now - dashboardStarted) / 1000000
	status := "building"
	if dashboardCommand != "build" {
		status = "running"
	}
	if dashboardDryRun {
		status = "planning"
	}
	if dashboardIdle {
		elapsedMS, status = dashboardCycleElapsed, "watching"
	}
	if dashboardSubject != "" {
		cli.Style(out, "text.muted", "target ", diagnosticColor == "always")
		var subject = bytes.NewBuffer(mem.System, nil)
		io.WriteString(&subject, "[")
		writeTargetField(&subject, dashboardSubject, width-11)
		io.WriteString(&subject, "]")
		writeStyledSubject(out, subject.String())
		io.WriteString(out, "\n")
		subject.Free()
		dashboardRows++
	}
	writeBuildTally(out, *progress, running, elapsedMS, status, "", width-2)
	dashboardRows++
}

func writeDashboardPadded(out io.Writer, text string, width int) {
	writeStyledPadded(out, text, width, "")
}

func writeStyledPadded(out io.Writer, text string, width int, token string) {
	var field = bytes.NewBuffer(mem.System, nil)
	writeDashboardField(&field, text, width)
	cli.Style(out, token, field.String(), token != "" && diagnosticColor == "always")
	for i := measureDisplayText(field.String(), len(field.String())*2).Cells; i < width; i++ {
		io.WriteString(out, " ")
	}
	field.Free()
}

func targetIsPath(target string) bool {
	return len(target) > 0 && (target[0] == '/' || target[0] == '\\' || (len(target) >= 2 && target[0] == '.' && (target[1] == '/' || target[1] == '\\')) || (len(target) >= 3 && target[:3] == "../") || (len(target) >= 3 && target[1] == ':' && (target[2] == '/' || target[2] == '\\')) || core.IsResourceURIName(target))
}

func targetBasenameStart(target string) int {
	start := 0
	for i := range target {
		if target[i] == '/' || target[i] == '\\' {
			start = i + 1
		}
	}
	return start
}

func writeTargetField(out io.Writer, target string, width int) {
	for i := range target {
		if target[i] < 32 || target[i] == 127 {
			writeDashboardField(out, target, width)
			return
		}
	}
	start := targetBasenameStart(target)
	if !targetIsPath(target) || start == 0 || width < 3 || measureDisplayText(target, len(target)*2).Cells <= width {
		writeDashboardField(out, target, width)
		return
	}
	basename := target[start:]
	baseWidth := measureDisplayText(basename, len(basename)*2).Cells
	if baseWidth < width-2 {
		writeDashboardField(out, target[:start-1], width-baseWidth-1)
		io.WriteString(out, target[start-1:start])
		io.WriteString(out, basename)
	} else {
		io.WriteString(out, "…/")
		writeDashboardField(out, basename, width-2)
	}
}

func writeStyledSubject(out io.Writer, subject string) {
	styled := diagnosticColor == "always"
	cli.Style(out, "structure", "[", styled)
	target := subject[1 : len(subject)-1]
	if targetIsPath(target) || (len(target) >= 4 && target[:4] == "…/") {
		start := targetBasenameStart(target)
		cli.Style(out, "path.directory", target[:start], styled)
		cli.Style(out, "path.basename", target[start:], styled)
	} else {
		cli.Style(out, "target.task", target, styled)
	}
	cli.Style(out, "structure", "]", styled)
}

func writeDashboardField(out io.Writer, text string, width int) {
	// Control-containing subjects are quoted in reports; disable live layout rather
	// than letting untrusted labels move the cursor.
	for i := range text {
		if text[i] < 32 || text[i] == 127 {
			io.WriteString(out, "(see log)")
			return
		}
	}
	end := dashboardTextEnd(text, width)
	if end < len(text) {
		end = dashboardTextEnd(text, width-1)
	}
	io.WriteString(out, text[:end])
	if end < len(text) {
		io.WriteString(out, "…")
	}
}

func dashboardTextEnd(text string, width int) int {
	return measureDisplayText(text, width).End
}
