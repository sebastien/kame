package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A rule reading N files resumes once per read. Work per resumption must not
// grow with the reads already made (memo revalidation, scheduler scans,
// operation-state and observation lookups), or the build is quadratic.
func TestNativeTrackedReadsScaleLinearly(t *testing.T) {
	binary := nativeCLIBinary(t)
	if testing.Short() {
		t.Skip("scaling check runs two builds")
	}
	build := func(n int) time.Duration {
		directory := t.TempDir()
		if err := os.Mkdir(filepath.Join(directory, "in"), 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			nativeCLIWrite(t, filepath.Join(directory, "in", fmt.Sprintf("%05d.txt", i)), "x\n")
		}
		nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), "rule ./out.txt =\n\tstr (wildcard ./in/*.txt |> filter exists? |> map (p => count (read p)) |> count)\n")
		command := exec.Command(binary, "./out.txt")
		command.Dir = directory
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build of %d reads: %v\n%s", n, err, output)
		}
		if got, _ := os.ReadFile(filepath.Join(directory, "out.txt")); strings.TrimSpace(string(got)) != fmt.Sprint(n) {
			t.Fatalf("build of %d reads produced %q", n, got)
		}
		return command.ProcessState.UserTime() + command.ProcessState.SystemTime()
	}
	small, large := build(400), build(1600)
	t.Logf("400 reads: %v CPU, 1600 reads: %v CPU", small, large)
	// Linear work gives about 4x; the quadratic engine gave more than 10x.
	if ratio := float64(large) / float64(max(small, time.Millisecond)); ratio > 8 {
		t.Fatalf("4x more reads took %.1fx more CPU (%v, %v)", ratio, small, large)
	}
}
