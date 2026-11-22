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
	// Linear work gives about 4x; the quadratic engine gave more than 10x.
	scalingCheck(t, 400, 1600, "rule ./out.txt =\n\tstr (wildcard ./in/* |> filter exists? |> map (p => count (read p)) |> count)\n")
}

// A rule with N computed inputs whose body reads each one resumes once per
// read; its inputs must not be resolved and requested again on each
// resumption, nor its frame rebuilt and rehashed, nor N instances scanned to
// find one. The old planner took 20x for 4x more inputs.
func TestNativeComputedInputsScaleLinearly(t *testing.T) {
	scalingCheck(t, 200, 800, "rule ./parts/{part}.txt =\n\tstr part \"\\n\"\n"+
		"rule ./out.txt : ${wildcard ./in/* |> map (seed => ./parts/${basename seed}.txt)} =\n\tstr (inputs |> map (path => count (read path)) |> count)\n")
}

// scalingCheck builds program with n and 4n files in ./in, expecting ./out.txt
// to hold the count, and fails when CPU time grows more than 8x.
func scalingCheck(t *testing.T, small, large int, program string) {
	t.Helper()
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
			nativeCLIWrite(t, filepath.Join(directory, "in", fmt.Sprintf("%05d", i)), "x\n")
		}
		nativeCLIWrite(t, filepath.Join(directory, "Kamefile"), program)
		command := exec.Command(binary, "--allow-read", "./out.txt")
		command.Dir = directory
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build with %d files: %v\n%s", n, err, output)
		}
		if got, _ := os.ReadFile(filepath.Join(directory, "out.txt")); strings.TrimSpace(string(got)) != fmt.Sprint(n) {
			t.Fatalf("build with %d files produced %q", n, got)
		}
		return command.ProcessState.UserTime() + command.ProcessState.SystemTime()
	}
	first, second := build(small), build(large)
	t.Logf("%d files: %v CPU, %d files: %v CPU", small, first, large, second)
	if ratio := float64(second) / float64(max(first, time.Millisecond)); ratio > 8 {
		t.Fatalf("%dx more files took %.1fx more CPU (%v, %v)", large/small, ratio, first, second)
	}
}
