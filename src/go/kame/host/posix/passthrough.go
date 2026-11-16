package posix

import (
	"io"
	"os"
	"os/exec"
)

// attachPassthrough replaces presentation writers with the invocation's own
// stdout/stderr, so children write directly as under a POSIX shell rather
// than through a host relay. Stages that already hold the terminal, file
// redirections, and graph edges are not presentation writers and are kept.
func attachPassthrough(commands []*exec.Cmd, out, errout io.Writer) {
	for _, cmd := range commands {
		if cmd == nil {
			continue
		}
		if cmd.Stdout == out {
			cmd.Stdout = os.Stdout
		}
		if cmd.Stderr == out {
			cmd.Stderr = os.Stdout
		} else if cmd.Stderr == errout {
			cmd.Stderr = os.Stderr
		}
	}
}
