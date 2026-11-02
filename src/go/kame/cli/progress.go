package cli

import (
	"kame/core"
	"kame/lang/source"
	"solod.dev/so/bytes"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/unicode/utf8"
)

// Progress is an established snapshot, not an estimate based on process counts.
type Progress struct {
	Active    int
	Completed int
	Failed    int
	Cancelled int
}

type ProgressWorkerView struct {
	Target    string
	Program   string
	State     string
	ElapsedMS int64
	Ready     bool
}

// ProgressView borrows all strings and workers for one render. The caller owns
// terminal detection, clocks, frame publication, and sink backpressure.
type ProgressView struct {
	Command   string
	Subject   string
	DryRun    bool
	Idle      bool
	Cycle     int
	ElapsedMS int64
	Width     int
	Height    int
	Color     bool
	Counts    Progress
	Workers   []ProgressWorkerView
}

type displayMeasure struct {
	End   int
	Cells int
}

func progressLine(frame string, index int) string {
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

// WriteFrameUpdate changes only owned rows. Both hosts keep the previous frame
// and publish this update in their terminal transaction.
func WriteFrameUpdate(out io.Writer, frame string, previous string, previousRows int, rows int) {
	var update = bytes.NewBuffer(mem.System, nil)
	if previousRows == rows {
		for i := 0; i < rows; i++ {
			line := progressLine(frame, i)
			if line == progressLine(previous, i) {
				continue
			}
			fmt.Fprintf(&update, "\x1b[%dA\r", rows-i)
			io.WriteString(&update, line)
			io.WriteString(&update, "\x1b[K")
			fmt.Fprintf(&update, "\x1b[%dB\r", rows-i)
		}
	} else if previousRows != 0 {
		fmt.Fprintf(&update, "\x1b[%dA\r", previousRows)
		count := rows
		if previousRows > count {
			count = previousRows
		}
		for i := 0; i < count; i++ {
			io.WriteString(&update, progressLine(frame, i))
			io.WriteString(&update, "\x1b[K\n")
		}
		if previousRows > rows {
			fmt.Fprintf(&update, "\x1b[%dA\r", previousRows-rows)
		}
	} else {
		io.WriteString(&update, frame)
	}
	out.Write(update.Bytes())
	update.Free()
}

func measureDisplayText(text string, width int) displayMeasure {
	end, cells := 0, 0
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if size == 0 {
			break
		}
		clusterEnd, clusterWidth := end+size, source.DisplayWidth(r)
		regional := r >= 0x1f1e6 && r <= 0x1f1ff
		join := false
		for clusterEnd < len(text) {
			next, nextSize := utf8.DecodeRuneInString(text[clusterEnd:])
			if nextSize == 0 {
				break
			}
			combining := source.DisplayWidth(next) == 0 || next == 0x200d || (next >= 0x1f3fb && next <= 0x1f3ff)
			if !combining && !join && !(regional && next >= 0x1f1e6 && next <= 0x1f1ff) {
				break
			}
			if next == 0xfe0f || join || regional {
				clusterWidth = 2
			}
			join = next == 0x200d
			regional = false
			clusterEnd += nextSize
		}
		if cells+clusterWidth > width {
			break
		}
		cells += clusterWidth
		end = clusterEnd
	}
	return displayMeasure{End: end, Cells: cells}
}

func WriteProgressField(out io.Writer, text string, width int) {
	for i := range text {
		if text[i] < 32 || text[i] == 127 {
			io.WriteString(out, "(see log)")
			return
		}
	}
	end := measureDisplayText(text, width).End
	if end < len(text) {
		end = measureDisplayText(text, width-1).End
	}
	io.WriteString(out, text[:end])
	if end < len(text) {
		io.WriteString(out, "…")
	}
}

func WriteProgressPadded(out io.Writer, text string, width int, token string, color bool) {
	var field = bytes.NewBuffer(mem.System, nil)
	WriteProgressField(&field, text, width)
	Style(out, token, field.String(), color)
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

func writeProgressTarget(out io.Writer, target string, width int) {
	for i := range target {
		if target[i] < 32 || target[i] == 127 {
			WriteProgressField(out, target, width)
			return
		}
	}
	start := targetBasenameStart(target)
	if !targetIsPath(target) || start == 0 || width < 3 || measureDisplayText(target, len(target)*2).Cells <= width {
		WriteProgressField(out, target, width)
		return
	}
	basename := target[start:]
	baseWidth := measureDisplayText(basename, len(basename)*2).Cells
	if baseWidth < width-2 {
		WriteProgressField(out, target[:start-1], width-baseWidth-1)
		io.WriteString(out, target[start-1:start])
		io.WriteString(out, basename)
	} else {
		io.WriteString(out, "…/")
		WriteProgressField(out, basename, width-2)
	}
}

func writeProgressSubject(out io.Writer, subject string, color bool) {
	Style(out, "structure", "[", color)
	target := subject[1 : len(subject)-1]
	if targetIsPath(target) || (len(target) >= 4 && target[:4] == "…/") {
		start := targetBasenameStart(target)
		Style(out, "path.directory", target[:start], color)
		Style(out, "path.basename", target[start:], color)
	} else {
		Style(out, "target.task", target, color)
	}
	Style(out, "structure", "]", color)
}

func WriteDuration(out io.Writer, elapsedMS int64) {
	if elapsedMS < 0 {
		elapsedMS = 0
	}
	fmt.Fprintf(out, "%.1fs", float64(elapsedMS)/1000)
}

func writeProgressStatus(out io.Writer, field string, token string, color bool) {
	start := 0
	for i := 0; i+2 < len(field); i++ {
		if field[i:i+3] == " - " {
			start = i + 3
		}
	}
	io.WriteString(out, field[:start])
	Style(out, token, field[start:], color)
}

func writeOutcomeRow(out io.Writer, target string, status string, token string, width int, color bool) {
	statusWidth := measureDisplayText(status, len(status)*2).Cells
	var subject = bytes.NewBuffer(mem.System, nil)
	io.WriteString(&subject, "[")
	if width >= 20 {
		writeProgressTarget(&subject, target, width-statusWidth-4)
	} else {
		io.WriteString(&subject, target)
	}
	io.WriteString(&subject, "]")
	writeProgressSubject(out, subject.String(), color)
	padding := width - measureDisplayText(subject.String(), len(subject.String())*2).Cells - statusWidth
	if padding < 1 {
		padding = 1
	}
	for i := 0; i < padding; i++ {
		io.WriteString(out, " ")
	}
	writeProgressStatus(out, status, token, color)
	io.WriteString(out, "\n")
	subject.Free()
}

func WriteHumanOutcome(out io.Writer, target string, outcome string, ansi bool, elapsedMS int64, hasTiming bool, width int, color bool) {
	label, status, token := "done", "✓", "status.success"
	if outcome == "failed" {
		label, status, token = "error", "failed", "status.failed"
	}
	if outcome == "cancelled" {
		label, status, token = "cancelled", "cancelled", "status.cancelled"
	}
	if ansi {
		var timing = bytes.NewBuffer(mem.System, nil)
		if hasTiming {
			WriteDuration(&timing, elapsedMS)
			io.WriteString(&timing, " - ")
		}
		io.WriteString(&timing, status)
		writeOutcomeRow(out, target, timing.String(), token, width, color)
		timing.Free()
		return
	}
	Style(out, token, label+" ["+target+"] "+outcome, color)
	io.WriteString(out, "\n")
}

func WriteHumanSummary(out io.Writer, command string, status int, elapsedMS int64, counts Progress, ansi bool, hadDiagnostic bool, width int, color bool) {
	label, token := "done", "status.success"
	if status >= 128 || (counts.Cancelled != 0 && counts.Failed == 0 && status != 0) {
		label, token = "cancelled", "status.cancelled"
	} else if status != 0 && (command != "fmt" || hadDiagnostic) {
		label, token = "error", "status.failed"
	}
	if ansi {
		word := "✓"
		if label == "cancelled" {
			word = "cancelled"
		} else if label == "error" {
			word = "failed"
		}
		writeProgressTally(out, counts, 0, elapsedMS, word, token, width, color)
		return
	}
	Style(out, token, label+" ", color)
	unit := "targets"
	if command == "fmt" {
		unit = "files"
	}
	fmt.Fprintf(out, "%s · %d %s complete · %d failed · %d cancelled · %dms\n", command, counts.Completed, unit, counts.Failed, counts.Cancelled, elapsedMS)
}

func writeProgressTally(out io.Writer, counts Progress, running int, elapsedMS int64, status string, token string, width int, color bool) {
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
	WriteDuration(&elapsed, elapsedMS)
	io.WriteString(&elapsed, " - "+status)
	rightWidth := measureDisplayText(elapsed.String(), len(elapsed.String())*2).Cells
	if width >= rightWidth+3 {
		WriteProgressPadded(out, tally.String(), width-rightWidth-2, "", color)
	} else {
		io.WriteString(out, tally.String())
	}
	io.WriteString(out, "  ")
	writeProgressStatus(out, elapsed.String(), token, color)
	io.WriteString(out, "\n")
	elapsed.Free()
	tally.Free()
}

// WriteDashboard writes a complete frame without terminal controls. Publication
// is separate so hosts can batch it with pending log bytes and diff old rows.
func WriteDashboard(out io.Writer, view ProgressView) int {
	width, height := view.Width, view.Height
	if width < 40 || height < 4 {
		return 0
	}
	running, ready, hidden := 0, 0, 0
	for i := range view.Workers {
		if view.Workers[i].Target == "" {
			continue
		}
		if view.Workers[i].Ready {
			ready++
		} else {
			running++
		}
	}
	var heading = bytes.NewBuffer(mem.System, nil)
	io.WriteString(&heading, view.Command)
	if view.DryRun {
		io.WriteString(&heading, " dry-run")
	}
	if view.Cycle != 0 {
		fmt.Fprintf(&heading, " · cycle %d", view.Cycle)
	}
	fmt.Fprintf(&heading, " · workers: %d", running)
	var bounded = bytes.NewBuffer(mem.System, nil)
	WriteProgressField(&bounded, heading.String(), width-5)
	io.WriteString(out, "── ")
	Style(out, "text.muted", bounded.String(), view.Color)
	io.WriteString(out, "\n")
	bounded.Free()
	heading.Free()
	rows := 1
	capacity := height - 3
	if view.Subject != "" {
		capacity--
	}
	if running > capacity || ready != 0 {
		capacity--
	}
	visible := 0
	for i := range view.Workers {
		worker := view.Workers[i]
		if worker.Target == "" || worker.Ready {
			continue
		}
		if visible >= capacity {
			hidden++
			continue
		}
		visible++
		var slot = bytes.NewBuffer(mem.System, nil)
		fmt.Fprintf(&slot, "  %d  ", i+1)
		Style(out, "text.muted", slot.String(), view.Color)
		var target = bytes.NewBuffer(mem.System, nil)
		io.WriteString(&target, "[")
		writeProgressTarget(&target, worker.Target, width/2-10)
		io.WriteString(&target, "]")
		writeProgressSubject(out, target.String(), view.Color)
		for j := measureDisplayText(target.String(), len(target.String())*2).Cells; j < width/2-8; j++ {
			io.WriteString(out, " ")
		}
		target.Free()
		state := worker.State
		if state == "" {
			state = "running"
		}
		io.WriteString(out, "  ")
		WriteProgressPadded(out, state, 11, "status.running", view.Color)
		used := len(slot.String()) + (width/2 - 8) + 2 + 11
		if width >= 80 {
			io.WriteString(out, "  ")
			tool := worker.Program
			if tool == "" {
				tool = "process"
			}
			WriteProgressPadded(out, tool, width/4-8, "worker.tool", view.Color)
			used += 2 + (width/4 - 8)
		}
		var elapsed = bytes.NewBuffer(mem.System, nil)
		WriteDuration(&elapsed, worker.ElapsedMS)
		remaining := width - 2 - used
		for j := len(elapsed.String()); j < remaining; j++ {
			io.WriteString(out, " ")
		}
		WriteProgressField(out, elapsed.String(), remaining)
		elapsed.Free()
		slot.Free()
		io.WriteString(out, "\n")
		rows++
	}
	if hidden != 0 || ready != 0 {
		var extra = bytes.NewBuffer(mem.System, nil)
		fmt.Fprintf(&extra, "%d hidden active · %d ready services", hidden, ready)
		WriteProgressField(out, extra.String(), width-2)
		io.WriteString(out, "\n")
		rows++
		extra.Free()
	}
	if view.Subject != "" {
		Style(out, "text.muted", "target ", view.Color)
		var subject = bytes.NewBuffer(mem.System, nil)
		io.WriteString(&subject, "[")
		writeProgressTarget(&subject, view.Subject, width-11)
		io.WriteString(&subject, "]")
		writeProgressSubject(out, subject.String(), view.Color)
		io.WriteString(out, "\n")
		rows++
		subject.Free()
	}
	status := "building"
	if view.Command != "build" {
		status = "running"
	}
	if view.DryRun {
		status = "planning"
	}
	if view.Idle {
		status = "watching"
	}
	writeProgressTally(out, view.Counts, running, view.ElapsedMS, status, "", width-2, view.Color)
	return rows + 1
}
