package posix

import (
	"errors"
	"io"
	"kame/host"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// terminalHandoff lends the controlling terminal to one foreground graph.
// Stages keep their own process groups, because a group is killed when its
// leader is reaped, so at most one stage's group becomes the terminal's
// foreground group: the stage that requested it, or with ForegroundAuto the
// last attached stage writing to the terminal (a pager, an editor), otherwise
// the first attached stage reading it. Only that stage reads the terminal, so
// no other stage can stop on a background read.
type terminalHandoff struct {
	tty      *os.File
	state    *term.State
	group    int
	attached bool
	once     sync.Once
	err      error
}

// terminalBusy admits one handoff at a time across concurrent graphs.
var terminalBusy atomic.Bool

var (
	errTerminalBusy    = errors.New("the terminal is already lent to another process graph")
	errTerminalMissing = errors.New("no controlling terminal is available")
)

// TerminalHandoffActive reports whether a child currently holds the terminal,
// so presentation must not draw on it.
func TerminalHandoffActive() bool { return terminalBusy.Load() }

// openTerminalHandoff claims the controlling terminal for one graph.
func openTerminalHandoff() (*terminalHandoff, error) {
	if !terminalBusy.CompareAndSwap(false, true) {
		return nil, errTerminalBusy
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		terminalBusy.Store(false)
		return nil, errTerminalMissing
	}
	state, err := term.GetState(int(tty.Fd()))
	if err != nil {
		tty.Close()
		terminalBusy.Store(false)
		return nil, errTerminalMissing
	}
	// Restore whichever group held the foreground, which need not be Kame's.
	group, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	if err != nil {
		tty.Close()
		terminalBusy.Store(false)
		return nil, err
	}
	return &terminalHandoff{tty: tty, state: state, group: group}, nil
}

// startTerminalHandoff applies a request's handoff to its commands, aligned
// with stages. A required handoff that cannot open the terminal returns an
// error before any spawn; an optional one leaves the graph's pipes in place.
func startTerminalHandoff(r host.ProcessRequest, stages []host.ProcessStage, commands []*exec.Cmd, out, errout io.Writer) (*terminalHandoff, error) {
	if !r.Terminal {
		return nil, nil
	}
	handoff, err := openTerminalHandoff()
	if err != nil {
		if r.TerminalRequired {
			return nil, err
		}
		return nil, nil
	}
	handoff.attach(stages, commands, out, errout, r.ForegroundAuto)
	return handoff, nil
}

// terminalRejection classifies a refused handoff on the rejection event that
// fail() queued first: competing for the terminal is invalid execution, a
// missing terminal is a host failure.
func terminalRejection(h *Host, err error) {
	if errors.Is(err, errTerminalBusy) {
		h.pending[0].Diagnostic.Code = "EXPR_INVALID"
	}
}

// attach replaces each attached stage's unredirected standard streams,
// recognized as the graph's own stdout/stderr writers or a closed stdin, with
// the terminal. It runs after redirections and before any command starts.
func (t *terminalHandoff) attach(stages []host.ProcessStage, commands []*exec.Cmd, out, errout io.Writer, auto bool) {
	attached := func(i int) bool { return i < len(stages) && stages[i].Terminal && commands[i] != nil }
	foreground := -1
	for i := range commands {
		if attached(i) && stages[i].Foreground {
			foreground = i
		}
	}
	if foreground < 0 && auto {
		for i, cmd := range commands {
			if attached(i) && cmd.Stdout == out {
				foreground = i
			}
		}
		for i, cmd := range commands {
			if foreground < 0 && attached(i) && cmd.Stdin == nil {
				foreground = i
			}
		}
	}
	for i, cmd := range commands {
		if !attached(i) {
			continue
		}
		if cmd.Stdout == out {
			cmd.Stdout = t.tty
		}
		if cmd.Stderr == out || cmd.Stderr == errout {
			cmd.Stderr = t.tty
		}
		if cmd.Stdin == nil && (i == foreground || foreground < 0) {
			cmd.Stdin = t.tty
		}
	}
	if foreground >= 0 {
		// The child takes the foreground before exec, so it cannot read the
		// terminal while still in a background group.
		commands[foreground].SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: int(t.tty.Fd())}
		t.attached = true
	}
}

// restore returns the terminal to its previous foreground group and restores the
// terminal modes a child may have left changed. It runs once, after every
// stage has been reaped or on a launch failure.
func (t *terminalHandoff) restore() error {
	if t == nil {
		return nil
	}
	t.once.Do(func() {
		fd := int(t.tty.Fd())
		if t.attached {
			// A background group changing the foreground receives SIGTTOU.
			ignored := signal.Ignored(syscall.SIGTTOU)
			if !ignored {
				signal.Ignore(syscall.SIGTTOU)
			}
			t.err = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, t.group)
			t.err = errors.Join(t.err, term.Restore(fd, t.state))
			if !ignored {
				signal.Reset(syscall.SIGTTOU)
			}
		}
		if !t.attached {
			t.err = term.Restore(fd, t.state)
		}
		t.err = errors.Join(t.err, t.tty.Close())
		terminalBusy.Store(false)
	})
	return t.err
}

// A restore error must fail an otherwise successful graph, without replacing
// a child failure that was already observed.
func terminalRestoreFailure(event *host.ProcessEvent, err error) {
	if err != nil && event.EffectiveStatus == 0 && event.Diagnostic.Code == "" {
		event.Outcome = host.ProcessFailed
		event.Diagnostic = host.ProcessDiagnostic{Code: DiagnosticHostFailure, Message: "cannot restore controlling terminal: " + err.Error()}
	}
}

// IgnoreJobControlSignals makes an interactive session behave like a shell
// without job control: Kame itself is never stopped by terminal access, and
// children inherit the ignored dispositions, so a stray background read from
// the terminal fails instead of stopping the session.
func IgnoreJobControlSignals() {
	signal.Ignore(syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU)
}
