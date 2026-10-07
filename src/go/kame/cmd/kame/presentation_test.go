package main

import (
	"bytes"
	"encoding/json"
	"kame/core"
	"kame/program"
	"strings"
	"testing"
)

func TestDashboardLayoutAndGraphemeBounds(t *testing.T) {
	var out bytes.Buffer
	resetDashboard(&out)
	defer freeDashboard(&out)
	diagnosticColor, dashboardCommand = "never", "build"
	observeWorker(program.Event{Kind: program.ProcessStarted, Target: "./build/main.o", Program: "cc", RequestID: 7, Generation: 1})
	workers[0].Started = 0
	renderDashboard(&out, &buildProgress{Completed: 8}, 80, 12, 1200000000)
	want := "── build · workers: 1\n  1  [./build/main.o]                  running      cc" + strings.Repeat(" ", 20) + "1.2s\n8 complete · 0 failed · 1 running" + strings.Repeat(" ", 30) + "1.2s - building\n"
	if out.String() != want {
		t.Fatalf("layout:\n%s", out.String())
	}
	workerLine := strings.Split(out.String(), "\n")[1]
	if measureDashboardText(workerLine, 1000).Cells != 78 || !strings.HasSuffix(workerLine, "1.2s") {
		t.Fatalf("worker duration not right-aligned: %q", workerLine)
	}
	clearDashboard(&out)
	out.Reset()
	renderDashboard(&out, &buildProgress{Completed: 8}, 40, 4, 1200000000)
	if !strings.Contains(out.String(), "main.o]") || strings.Contains(out.String(), "cc") || !strings.HasSuffix(out.String(), "1.2s - building\n") {
		t.Fatalf("collapsed layout: %q", out.String())
	}
	workerLine = strings.Split(out.String(), "\n")[1]
	if measureDashboardText(workerLine, 1000).Cells != 38 || !strings.HasSuffix(workerLine, "1.2s") {
		t.Fatalf("compact worker duration not right-aligned: %q", workerLine)
	}
	for _, text := range []string{"e\u0301", "界", "👩‍💻", "🇫🇷"} {
		out.Reset()
		writeDashboardField(&out, text+"xx", 3)
		if !strings.HasPrefix(out.String(), text) {
			t.Errorf("split grapheme: %q", out.String())
		}
	}
	observeWorker(program.Event{Kind: program.ProcessExited, Target: "./build/main.o", RequestID: 8, Generation: 1})
	if workers[0].Target == "" {
		t.Fatal("unrelated process released worker")
	}
	observeWorker(program.Event{Kind: program.ProcessExited, Target: "./build/main.o", RequestID: 7, Generation: 0})
	if workers[0].Target == "" {
		t.Fatal("late generation released worker")
	}
	observeWorker(program.Event{Kind: program.TargetCompleted, NodeID: 8, Target: "./build/main.o", Generation: 1, Attempt: 2})
	if workers[0].Target == "" {
		t.Fatal("different node released worker")
	}
	observeWorker(program.Event{Kind: program.TargetCompleted, Target: "./build/main.o", Generation: 1, Attempt: 2})
	if workers[0].Target != "" {
		t.Fatal("terminal resumption did not release worker")
	}
	out.Reset()
	renderDashboard(&out, &buildProgress{Completed: 9}, 80, 12, 1300000000)
	if dashboardRows != 2 || strings.Contains(out.String(), "idle") || !strings.HasSuffix(out.String(), "1.3s - building\n") {
		t.Fatalf("completion retained idle rows: %q", out.String())
	}
	out.Reset()
	setDashboardSubject(strings.Repeat("long", 50))
	renderDashboard(&out, &buildProgress{}, 40, 4, 9999999999999)
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if len([]rune(line)) > 38 {
			t.Fatalf("row wraps in narrow terminal: %q", line)
		}
	}
}

func TestActiveWorkersSurviveHistoricalSlots(t *testing.T) {
	var out bytes.Buffer
	resetDashboard(&out)
	defer freeDashboard(&out)
	diagnosticColor, dashboardCommand = "never", "build"
	for i := 0; i < 37; i++ {
		// Distinct live requests grow the slot table before all complete.
		observeWorker(program.Event{Kind: program.ProcessStarted, NodeID: int64(i + 1), Target: "old", Program: "cc", RequestID: int64(i + 1), Generation: 1})
	}
	observeWorker(program.Event{Kind: program.ProcessStarted, NodeID: 38, Target: "current", RequestID: 38, Generation: 1})
	for i := 0; i < 37; i++ {
		observeWorker(program.Event{Kind: program.ProcessExited, NodeID: int64(i + 1), Target: "old", RequestID: int64(i + 1), Generation: 1})
	}
	renderDashboard(&out, &buildProgress{Active: 30}, 80, 12, 1000000000)
	if !strings.Contains(out.String(), "[current]") || !strings.Contains(out.String(), "process") || strings.Contains(out.String(), "idle") || strings.Contains(out.String(), "waiting") || dashboardRows != 3 {
		t.Fatalf("active work hidden by historical slots: %q", out.String())
	}
	out.Reset()
	observeWorker(program.Event{Kind: program.ProcessExited, NodeID: 38, Target: "current", RequestID: 38, Generation: 1})
	renderDashboard(&out, &buildProgress{Active: 30}, 80, 12, 2000000000)
	if strings.Contains(out.String(), "0 running") || strings.Contains(out.String(), "waiting") || !strings.HasSuffix(out.String(), "2.0s - building\n") {
		t.Fatalf("graph interest misreported as waiting: %q", out.String())
	}
}

func TestDashboardEndTargetAtBottom(t *testing.T) {
	var out bytes.Buffer
	resetDashboard(&out)
	defer freeDashboard(&out)
	diagnosticColor, dashboardCommand = "never", "build"
	setDashboardSubject("default")
	observeWorker(program.Event{Kind: program.ProcessStarted, Target: "./build/main.o", Program: "cc", RequestID: 1})
	renderDashboard(&out, &buildProgress{}, 80, 12, 1000000000)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 4 || lines[0] != "── build · workers: 1" || !strings.Contains(lines[1], "main.o") || lines[2] != "target [default]" || !strings.HasSuffix(lines[3], "1.0s - building") {
		t.Fatal(out.String())
	}
	out.Reset()
	renderDashboard(&out, &buildProgress{}, 40, 4, 1000000000)
	if dashboardRows > 4 || !strings.Contains(out.String(), "target [default]\n") {
		t.Fatal(out.String())
	}
}

func TestDashboardUpdatesOnlyChangedRows(t *testing.T) {
	var out bytes.Buffer
	resetDashboard(&out)
	defer freeDashboard(&out)
	first := "workers\n  1 [compile] cc\n8 complete · 0 failed\n"
	paintDashboard(&out, first, 0, 3)
	out.Reset()
	paintDashboard(&out, first, 3, 3)
	if out.Len() != 0 {
		t.Fatalf("unchanged frame repainted: %q", out.String())
	}
	paintDashboard(&out, "workers\n  1 [compile] cc\n9 complete · 0 failed\n", 3, 3)
	if out.String() != "\x1b[1A\r9 complete · 0 failed\x1b[K\x1b[1B\r" {
		t.Fatalf("changed summary erased workers: %q", out.String())
	}
}

func TestOutcomeColumnsAndRestrainedColor(t *testing.T) {
	var out bytes.Buffer
	diagnosticColor = "never"
	for _, target := range []string{"short", strings.Repeat("long", 40), "界👩‍💻e\u0301"} {
		out.Reset()
		writeOutcomeRow(&out, target, "0.1s - ✓", "status.success", 38)
		line := strings.TrimSuffix(out.String(), "\n")
		if measureDashboardText(line, 1000).Cells != 38 || !strings.HasSuffix(line, "0.1s - ✓") || !strings.HasPrefix(line, "[") {
			t.Fatalf("misaligned outcome: %q", line)
		}
	}
	out.Reset()
	diagnosticColor = "always"
	writeOutcomeRow(&out, "target", "failed", "status.failed", 0)
	if out.String() != "\x1b[2m[\x1b[0m\x1b[35mtarget\x1b[0m\x1b[2m]\x1b[0m \x1b[1;31mfailed\x1b[0m\n" {
		t.Fatal(out.String())
	}
	diagnosticColor = "never"
}

func TestTargetDurationUsesLifecycleClock(t *testing.T) {
	var out bytes.Buffer
	resetDashboard(&out)
	defer freeDashboard(&out)
	selectedOutput, diagnosticColor = "ansi", "never"
	progress := buildProgress{}
	event := program.Event{Kind: program.TargetStarted, Target: "target", Key: core.ResourceKey{Name: "target"}, NodeID: 7, Generation: 1, HasMonotonic: true, MonotonicNS: 1000000000}
	observeOutcome(event, &progress)
	event.Kind, event.MonotonicNS = program.TargetCompleted, 1123000000
	observeOutcome(event, &progress)
	writeTargetOutcome(&out, event, "done", "status.success")
	if out.String() != "[target] 0.1s - ✓\n" {
		t.Fatal(out.String())
	}
	event.Generation, event.Kind, event.MonotonicNS = 2, program.TargetStarted, 2000000000
	observeOutcome(event, &progress)
	event.Kind, event.MonotonicNS = program.TargetCompleted, 2045000000
	observeOutcome(event, &progress)
	out.Reset()
	writeTargetOutcome(&out, event, "done", "status.success")
	if out.String() != "[target] 0.0s - ✓\n" {
		t.Fatal(out.String())
	}
}

func TestDurationAndPathTheme(t *testing.T) {
	var out bytes.Buffer
	diagnosticColor = "never"
	writeDuration(&out, 40840)
	if out.String() != "40.8s" {
		t.Fatal(out.String())
	}
	out.Reset()
	writeOutcomeRow(&out, "default", "40.8s - ✓", "status.success", 78)
	if !strings.HasSuffix(out.String(), "40.8s - ✓\n") || measureDashboardText(strings.TrimSuffix(out.String(), "\n"), 1000).Cells != 78 {
		t.Fatal(out.String())
	}
	out.Reset()
	writeBuildTally(&out, buildProgress{Completed: 122}, 0, 40840, "✓", "status.success", 78)
	if !strings.HasSuffix(out.String(), "40.8s - ✓\n") || measureDashboardText(strings.TrimSuffix(out.String(), "\n"), 1000).Cells != 78 {
		t.Fatal(out.String())
	}
	out.Reset()
	diagnosticColor = "always"
	writeOutcomeRow(&out, "./dist/www/page.html", "40.8s - ✓", "status.success", 0)
	if !strings.Contains(out.String(), "\x1b[2m./dist/www/\x1b[0m\x1b[1mpage.html\x1b[0m") || !strings.Contains(out.String(), "40.8s - \x1b[32m✓\x1b[0m") {
		t.Fatal(out.String())
	}
	out.Reset()
	diagnosticColor = "never"
	writeTargetField(&out, "./very/long/directory/page.html", 16)
	if !strings.HasSuffix(out.String(), "/page.html") || measureDashboardText(out.String(), 1000).Cells > 16 {
		t.Fatal(out.String())
	}
}

func TestOutcomeIdentityCountsOnce(t *testing.T) {
	var out bytes.Buffer
	resetDashboard(&out)
	defer freeDashboard(&out)
	progress := buildProgress{}
	event := program.Event{Kind: program.TargetStarted, Target: "default", NodeID: 3, Generation: 1, Key: core.ResourceKey{Name: "default"}}
	observeOutcome(event, &progress)
	observeOutcome(event, &progress)
	event.Kind = program.TargetCompleted
	observeOutcome(event, &progress)
	observeOutcome(event, &progress)
	if progress.Active != 0 || progress.Completed != 1 {
		t.Fatal(progress)
	}
	event.Generation = 2
	observeOutcome(event, &progress)
	if progress.Completed != 2 {
		t.Fatal(progress)
	}
}

func TestFailureSummaryDoesNotBecomeCancellation(t *testing.T) {
	var out bytes.Buffer
	program.WriteSummary(&out, "build", 1, false, 24, 8, 1, 2)
	if !strings.Contains(out.String(), `"status":"failure"`) {
		t.Fatal(out.String())
	}
}

func TestEarlyPresentationDoesNotEmitStreamRecords(t *testing.T) {
	for _, test := range []struct {
		args  []string
		kind  string
		topic string
	}{
		{[]string{"do", "run", "--version", "--help"}, "help", "run"},
		{[]string{"do", "fmt", "--help", "--version"}, "help", "fmt"},
		{[]string{"do", "run", "-c", "--help", "--version"}, "version", ""},
		{[]string{"do", "bogus", "--help"}, "help", "do"},
	} {
		var out, errOut bytes.Buffer
		args := append([]string{"--json"}, test.args...)
		if status := Run(args, &input{}, &out, &errOut); status != 0 || errOut.Len() != 0 {
			t.Fatalf("args=%q status=%d stderr=%q", args, status, errOut.String())
		}
		var record map[string]any
		if err := json.Unmarshal(out.Bytes(), &record); err != nil || record["type"] != test.kind {
			t.Fatalf("args=%q output=%q error=%v", args, out.String(), err)
		}
		if test.kind == "help" && record["topic"] != test.topic {
			t.Fatalf("args=%q topic=%v", args, record["topic"])
		}
	}
}

func TestOutcomeCleanupAllowsSameIdentityInNextCycle(t *testing.T) {
	var out bytes.Buffer
	resetDashboard(&out)
	defer freeDashboard(&out)
	event := program.Event{Kind: program.TargetCompleted, NodeID: 3, Generation: 1, Key: core.ResourceKey{Name: "default"}}
	progress := buildProgress{}
	observeOutcome(event, &progress)
	freeOutcomes()
	freeOutcomes()
	if outcomes != nil {
		t.Fatal("outcomes retained after cleanup")
	}
	progress = buildProgress{}
	observeOutcome(event, &progress)
	if progress.Completed != 1 {
		t.Fatal(progress)
	}
	freeDashboard(&out)
	if outcomes != nil {
		t.Fatal("dashboard retained outcomes")
	}
}

func TestPresentationDefaultAndStructuredResults(t *testing.T) {
	for _, mode := range []string{"", "ansi", "text", "json"} {
		var out, errOut bytes.Buffer
		args := []string{"do", "inputs", "-c", "task default :"}
		if mode != "" {
			args = append([]string{"-o", mode}, args...)
		}
		if status := Run(args, &input{}, &out, &errOut); status != 0 || errOut.Len() != 0 {
			t.Fatalf("%s: status=%d err=%q", mode, status, errOut.String())
		}
		if mode == "json" {
			if out.String() != "[]\n" {
				t.Fatal(out.String())
			}
		} else if !strings.Contains(out.String(), "no inputs") || strings.Contains(out.String(), "\x1b[") {
			t.Fatal(out.String())
		}
	}
	var out, errOut bytes.Buffer
	if status := Run([]string{"--json", "do", "render", "-c", "hello"}, &input{}, &out, &errOut); status != 0 || errOut.Len() != 0 {
		t.Fatalf("render: %d %s", status, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatal(out.String())
	}
	var record map[string]any
	if json.Unmarshal([]byte(lines[1]), &record) != nil || record["type"] != "render-result" || record["data"] != "hello" {
		t.Fatal(out.String())
	}
	if json.Unmarshal([]byte(lines[2]), &record) != nil || record["type"] != "summary" || record["status"] != "success" {
		t.Fatal(out.String())
	}
}
