package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Unobserved direct-session output reaches children as the invocation's own
// descriptors, as under a POSIX shell; captures and pipeline edges keep pipes.
func TestNativeKashForegroundOutputInheritsDescriptors(t *testing.T) {
	binary := nativeCLIBinary(t)
	directory := t.TempDir()
	interpreter := filepath.Join(directory, "kash")
	if err := os.Symlink(binary, interpreter); err != nil {
		t.Fatal(err)
	}
	stdoutName, stderrName := filepath.Join(directory, "out.txt"), filepath.Join(directory, "err.txt")
	nativeCLIWrite(t, filepath.Join(directory, "script"), strings.Join([]string{
		`readlink /proc/self/fd/1`,
		`sh -c "readlink /proc/self/fd/2 >&2"`,
		`readlink /proc/self/fd/1 | cat`,
		`captured = $(readlink /proc/self/fd/1)`,
		`echo (starts? "pipe:" captured)`,
	}, "\n")+"\n")
	stdout, err := os.Create(stdoutName)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(stderrName)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(interpreter, "script")
	command.Dir, command.Stdout, command.Stderr = directory, stdout, stderr
	runErr := command.Run()
	stdout.Close()
	stderr.Close()
	out, _ := os.ReadFile(stdoutName)
	errOut, _ := os.ReadFile(stderrName)
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if runErr != nil || len(lines) != 3 || lines[0] != stdoutName || !strings.HasPrefix(lines[1], "pipe:") || lines[2] != "true" {
		t.Fatalf("status=%v stdout=%q stderr=%q", runErr, out, errOut)
	}
	if strings.TrimSpace(string(errOut)) != stderrName {
		t.Fatalf("stderr was not inherited: %q", errOut)
	}
}
