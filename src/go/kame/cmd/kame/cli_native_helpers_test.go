package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Go is only a subprocess driver here: parser, evaluator, allocator and host
// code execute in the compiled Solod binary, never in compatibility stubs.
func runNativeCLI(t *testing.T, args []string, in io.Reader, out, errOut io.Writer) int {
	t.Helper()
	return runNativeCLIIn(t, "", args, in, out, errOut)
}

func runNativeCLIIn(t *testing.T, directory string, args []string, in io.Reader, out, errOut io.Writer) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nativeCLIBinary(t), args...)
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 2 * time.Second
	command.Dir, command.Stdin, command.Stdout, command.Stderr = directory, in, out, errOut
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			t.Fatalf("compiled CLI timed out: %q", args)
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		t.Fatalf("cannot execute compiled CLI: %v", err)
	}
	return 0
}

func nativeWriteAST(t *testing.T, out io.Writer, language, name, text string) int {
	t.Helper()
	directory := t.TempDir()
	if !filepath.IsLocal(name) {
		t.Fatalf("AST fixture name must be relative: %q", name)
	}
	filename := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return runNativeCLIIn(t, directory, []string{"do", "parse", "--json", "--lang", language, name}, nil, out, io.Discard)
}
