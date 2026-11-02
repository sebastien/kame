package cli

import (
	"kame/core"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// WriteProgressDocument adapts the WASM CLI's borrowed snapshot to the same
// renderer used directly by native. No terminal or execution policy lives here.
func WriteProgressDocument(out io.Writer, value core.Value) bool {
	return WriteTrackedProgressDocument(out, value, nil, 0)
}

// WriteTrackedProgressDocument queries invocation state without consuming it.
// A nil tracker retains the original explicit-snapshot rendering API.
func WriteTrackedProgressDocument(out io.Writer, value core.Value, tracker *ProgressState, nowNS int64) bool {
	if value.Kind != core.Record {
		return false
	}
	if !validProgressFields(value) {
		return false
	}
	op := reportField(value, "op").Text
	if op == "paint" {
		previousRows, rows := reportField(value, "previousRows").Int, reportField(value, "rows").Int
		if previousRows < 0 || previousRows > 4096 || rows < 0 || rows > 4096 {
			return false
		}
		WriteFrameUpdate(out, reportField(value, "frame").Text, reportField(value, "previous").Text, int(previousRows), int(rows))
		return true
	}
	width := reportField(value, "width").Int
	height := reportField(value, "height").Int
	if width < 0 || width > 4096 || height < 0 || height > 4096 {
		return false
	}
	color := reportField(value, "color").Bool
	ansi := reportField(value, "ansi").Bool
	elapsed := reportField(value, "elapsedMS").Int
	c := reportField(value, "counts")
	if c.Kind != core.Nil && (c.Kind != core.Record || !validProgressFields(c)) {
		return false
	}
	counts := Progress{Active: int(reportField(c, "active").Int), Completed: int(reportField(c, "completed").Int), Failed: int(reportField(c, "failed").Int), Cancelled: int(reportField(c, "cancelled").Int)}
	tracked := reportField(value, "tracked").Bool
	if tracked && tracker == nil {
		return false
	}
	if tracked {
		counts = tracker.Counts
	}
	if op == "outcome" {
		outcome := reportField(value, "outcome").Text
		if outcome != "complete" && outcome != "failed" && outcome != "cancelled" {
			return false
		}
		timed := reportField(value, "hasTiming").Bool
		if tracked {
			observation := ParseProgressObservation(reportField(value, "event"))
			if !observation.OK {
				return false
			}
			timing := tracker.Timing(observation.Event)
			elapsed, timed = timing.ElapsedMS, timing.HasTiming
		}
		WriteHumanOutcome(out, reportField(value, "target").Text, outcome, ansi, elapsed, timed, int(width), color)
		return true
	}
	if op == "summary" {
		WriteHumanSummary(out, reportField(value, "command").Text, int(reportField(value, "status").Int), elapsed, counts, ansi, reportField(value, "hadDiagnostic").Bool, int(width), color)
		return true
	}
	if op != "frame" {
		return false
	}
	entries := reportField(value, "workers")
	if !tracked && (entries.Kind != core.List || len(entries.List) > 4096) {
		return false
	}
	var workers []ProgressWorkerView
	if tracked {
		workers = tracker.WorkerView(mem.System, nowNS)
	} else {
		workers = slices.Make[ProgressWorkerView](mem.System, len(entries.List))
		for i := range entries.List {
			w := entries.List[i]
			if w.Kind != core.Record || !validProgressFields(w) {
				slices.Free(mem.System, workers)
				return false
			}
			workers[i] = ProgressWorkerView{Target: reportField(w, "target").Text, Program: reportField(w, "program").Text, State: reportField(w, "state").Text, Ready: reportField(w, "ready").Bool, ElapsedMS: reportField(w, "elapsedMS").Int}
		}
	}
	WriteDashboard(out, ProgressView{Command: reportField(value, "command").Text, Subject: reportField(value, "subject").Text, DryRun: reportField(value, "dryRun").Bool, Idle: reportField(value, "idle").Bool, Cycle: int(reportField(value, "cycle").Int), ElapsedMS: elapsed, Width: int(width), Height: int(height), Color: color, Counts: counts, Workers: workers})
	slices.Free(mem.System, workers)
	return true
}

func validProgressFields(value core.Value) bool {
	for i := range value.Record {
		field := value.Record[i]
		switch field.Key {
		case "op", "command", "subject", "target", "outcome", "frame", "previous", "program", "state":
			if field.Value.Kind != core.String {
				return false
			}
		case "ansi", "color", "hasTiming", "hadDiagnostic", "dryRun", "idle", "ready", "tracked":
			if field.Value.Kind != core.Bool {
				return false
			}
		case "width", "height", "status", "cycle", "previousRows", "rows", "active", "completed", "failed", "cancelled":
			if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 2147483647 {
				return false
			}
		case "elapsedMS":
			if field.Value.Kind != core.Int || field.Value.Int < 0 {
				return false
			}
		}
	}
	return true
}
