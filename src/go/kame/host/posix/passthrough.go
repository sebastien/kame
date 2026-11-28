package posix

import (
	"io"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

// attachPassthrough lends direct output where the child can safely block.
// The returned pipe copies belong to the caller through process launch.
func attachPassthrough(commands []*exec.Cmd, out, errout io.Writer) []*os.File {
	var owned []*os.File
	var stdoutFile, stderrFile *os.File
	var checkedStdout, checkedStderr bool
	for _, cmd := range commands {
		if cmd == nil {
			continue
		}
		if !checkedStdout && (cmd.Stdout == out || cmd.Stderr == out) {
			stdoutFile, checkedStdout = passthroughFile(os.Stdout, &owned), true
		}
		if !checkedStderr && cmd.Stderr == errout {
			stderrFile, checkedStderr = passthroughFile(os.Stderr, &owned), true
		}
		if cmd.Stdout == out && stdoutFile != nil {
			cmd.Stdout = stdoutFile
		}
		if cmd.Stderr == out && stdoutFile != nil {
			cmd.Stderr = stdoutFile
		} else if cmd.Stderr == errout && stderrFile != nil {
			cmd.Stderr = stderrFile
		}
	}
	return owned
}

func passthroughFile(file *os.File, owned *[]*os.File) *os.File {
	fd := int(file.Fd())
	kind := descriptorType(fd)
	if kind == unix.S_IFREG || kind == unix.S_IFCHR {
		return file
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return nil
	}
	if kind == unix.S_IFSOCK && flags&unix.O_NONBLOCK == 0 {
		return file // Public socket writes use per-call nonblocking I/O.
	}
	if kind == unix.S_IFIFO {
		if copyFD, err := blockingPipe(fd, flags); err == nil {
			direct := os.NewFile(uintptr(copyFD), file.Name())
			*owned = append(*owned, direct)
			return direct
		}
	}
	return nil // Keep the relay when a blocking pipe cannot be lent.
}
