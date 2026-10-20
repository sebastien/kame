package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPickOversizedOutputIsDrainedAndRejected(t *testing.T) {
	binary := nativeCLIBinary(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is needed for the pseudo-terminal")
	}
	picker := filepath.Join(t.TempDir(), "picker")
	// A valid first row must not hide overflow later in the same output.
	text := "#!/bin/sh\nprintf 'command\\037help\\037do help\\n'\ndd if=/dev/zero bs=65536 count=4 2>/dev/null\n"
	if err := os.WriteFile(picker, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", `
import errno, os, pty, select, signal, sys, time
pid, fd = pty.fork()
if pid == 0:
    os.execv(sys.argv[1], [sys.argv[1], "--color=never", "do", "pick", "-c", "task default :", "--picker", sys.argv[2]])
output = bytearray()
status = None
deadline = time.monotonic() + 5
try:
    while time.monotonic() < deadline:
        if select.select([fd], [], [], 0.05)[0]:
            try:
                data = os.read(fd, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            output.extend(data)
        done, code = os.waitpid(pid, os.WNOHANG)
        if done:
            status = code
            break
    assert status is not None, "picker deadlocked instead of draining oversized output"
    assert os.waitstatus_to_exitcode(status) == 1, (status, output)
    assert b"HOST_FAIL" in output, output
finally:
    if status is None:
        os.killpg(pid, signal.SIGKILL)
        os.waitpid(pid, 0)
    os.close(fd)
`, binary, picker)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("oversized picker: %v\n%s", err, output)
	}
}
