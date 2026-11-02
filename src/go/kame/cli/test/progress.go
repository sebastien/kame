package cli_test

import (
	"kame/cli"
	"solod.dev/so/bytes"
	"solod.dev/so/testing"
)

func TestPortableDashboard(t *testing.T) {
	var out = bytes.NewBuffer(t.Allocator(), nil)
	defer out.Free()
	workers := []cli.ProgressWorkerView{{Target: "./build/main.o", Program: "cc", ElapsedMS: 1200}}
	rows := cli.WriteDashboard(&out, cli.ProgressView{Command: "build", Width: 80, Height: 12, ElapsedMS: 1200, Counts: cli.Progress{Completed: 8}, Workers: workers})
	want := "── build · workers: 1\n  1  [./build/main.o]                  running      cc                    1.2s\n8 complete · 0 failed · 1 running                              1.2s - building\n"
	if rows != 3 || out.String() != want {
		t.Error("portable dashboard changed native layout")
	}
}

func TestPortableOutcomeAndSummary(t *testing.T) {
	var out = bytes.NewBuffer(t.Allocator(), nil)
	defer out.Free()
	cli.WriteHumanOutcome(&out, "./out.txt", "complete", true, 123, true, 0, false)
	cli.WriteHumanSummary(&out, "build", 0, 124, cli.Progress{Completed: 1}, true, false, 0, false)
	if out.String() != "[./out.txt] 0.1s - ✓\n1 complete · 0 failed  0.1s - ✓\n" {
		t.Error("ANSI outcome or summary drifted")
	}
	out.Reset()
	cli.WriteHumanOutcome(&out, "./out.txt", "complete", false, 123, true, 0, false)
	cli.WriteHumanSummary(&out, "build", 0, 124, cli.Progress{Completed: 1}, false, false, 0, false)
	if out.String() != "done [./out.txt] complete\ndone build · 1 targets complete · 0 failed · 0 cancelled · 124ms\n" {
		t.Error("text contract changed")
	}
}

func TestPortableProgressWidthAndGraphemes(t *testing.T) {
	var out = bytes.NewBuffer(t.Allocator(), nil)
	defer out.Free()
	cli.WriteProgressField(&out, "界e\u0301x", 3)
	if out.String() != "界…" {
		t.Error("progress split a grapheme or wide cell")
	}
	out.Reset()
	if cli.WriteDashboard(&out, cli.ProgressView{Width: 39, Height: 12}) != 0 || out.Len() != 0 {
		t.Error("unsafe dashboard size was rendered")
	}
	cli.WriteProgressField(&out, "unsafe\x1b[2J", 40)
	if out.String() != "(see log)" {
		t.Error("terminal control escaped the presentation boundary")
	}
}

func TestPortableFrameUpdates(t *testing.T) {
	var out = bytes.NewBuffer(t.Allocator(), nil)
	defer out.Free()
	cli.WriteFrameUpdate(&out, "heading\nnew\n", "heading\nold\n", 2, 2)
	if out.String() != "\x1b[1A\rnew\x1b[K\x1b[1B\r" {
		t.Error("unchanged dashboard rows were repainted")
	}
	out.Reset()
	cli.WriteFrameUpdate(&out, "heading\n", "heading\nold\n", 2, 1)
	if out.String() != "\x1b[2A\rheading\x1b[K\n\x1b[K\n\x1b[1A\r" {
		t.Error("shrinking dashboard did not clear its owned row")
	}
	out.Reset()
	cli.WriteFrameUpdate(&out, "heading\n", "heading\n", 1, 1)
	if out.Len() != 0 {
		t.Error("identical frame emitted output")
	}
}
