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
	tty        *os.File
	state      *term.State
	group      int
	attached   bool
	foreground *exec.Cmd
	// released is set while a stopped graph has given the terminal back;
	// jobState holds the modes it left (052 job control).
	released bool
	jobState *term.State
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

// openTerminalHandoff claims the controlling terminal for one graph; a
// background graph shares it without a claim.
func openTerminalHandoff(background bool) (*terminalHandoff, error) {
	if !background && !terminalBusy.CompareAndSwap(false, true) {
		return nil, errTerminalBusy
	}
	release := func() {
		if !background {
			terminalBusy.Store(false)
		}
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		release()
		return nil, errTerminalMissing
	}
	state, err := term.GetState(int(tty.Fd()))
	if err != nil {
		tty.Close()
		release()
		return nil, errTerminalMissing
	}
	// Restore whichever group held the foreground, which need not be Kame's.
	// A background graph launches while any job may hold it, and returns it
	// to the session's own group.
	group, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	if background {
		group, err = unix.Getpgrp(), nil
		if saved := sessionTerminalState.Load(); saved != nil {
			state = saved
		}
	}
	if err != nil {
		tty.Close()
		release()
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
	handoff, err := openTerminalHandoff(r.TerminalBackground)
	if err != nil {
		if r.TerminalRequired {
			return nil, err
		}
		return nil, nil
	}
	handoff.attach(stages, commands, out, errout, r.ForegroundAuto, r.TerminalBackground)
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
func (t *terminalHandoff) attach(stages []host.ProcessStage, commands []*exec.Cmd, out, errout io.Writer, auto, background bool) {
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
	if foreground >= 0 && background {
		// A background job's graph starts as if released: reading the
		// terminal stops it until fg gives it the foreground.
		t.attached, t.released, t.foreground = true, true, commands[foreground]
	} else if foreground >= 0 {
		// The child takes the foreground before exec, so it cannot read the
		// terminal while still in a background group.
		commands[foreground].SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: int(t.tty.Fd())}
		t.attached = true
		t.foreground = commands[foreground]
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
		switch {
		case t.released:
			// A stopped graph already returned the terminal.
		case t.attached:
			t.err = t.giveTerminal(t.group, t.state)
		default:
			t.err = term.Restore(fd, t.state)
		}
		t.err = errors.Join(t.err, t.tty.Close())
		if !t.released {
			terminalBusy.Store(false)
		}
	})
	return t.err
}

// giveTerminal makes group the terminal foreground with modes state. A
// background group changing the foreground receives SIGTTOU, which Kame
// must not stop on.
func (t *terminalHandoff) giveTerminal(group int, state *term.State) error {
	fd := int(t.tty.Fd())
	var err error
	// Modes first: once another group holds the foreground, Kame's own
	// tcsetattr would also raise SIGTTOU.
	withSignalIgnored(syscall.SIGTTOU, func() {
		err = errors.Join(term.Restore(fd, state), unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, group))
	})
	return err
}

// release returns the terminal of a stopped graph to the group that held it
// before launch, saving the modes the graph left. The caller serializes
// release and reacquire for one handoff.
func (t *terminalHandoff) release() {
	if t == nil || !t.attached || t.released {
		return
	}
	t.jobState, _ = term.GetState(int(t.tty.Fd()))
	t.giveTerminal(t.group, t.state)
	t.released = true
	terminalBusy.Store(false)
}

// reacquire gives the terminal back to a released graph with its modes.
func (t *terminalHandoff) reacquire() error {
	if t == nil || !t.released {
		return nil
	}
	if !terminalBusy.CompareAndSwap(false, true) {
		return errTerminalBusy
	}
	state := t.jobState
	if state == nil {
		state = t.state
	}
	if err := t.giveTerminal(t.foreground.Process.Pid, state); err != nil {
		terminalBusy.Store(false)
		return err
	}
	t.released = false
	return nil
}

// withSignalIgnored runs fn with sig ignored, then restores Kame's handling:
// ignored stays ignored, and a notified signal is notified again.
func withSignalIgnored(sig syscall.Signal, fn func()) {
	if signal.Ignored(sig) {
		fn()
		return
	}
	signal.Ignore(sig)
	fn()
	signal.Reset(sig)
	if notified := jobControlNotify.Load(); notified != nil {
		signal.Notify(*notified, sig)
	}
}

var (
	jobControlNotify     atomic.Pointer[chan os.Signal]
	sessionTerminalState atomic.Pointer[term.State]
)

// CatchJobControlSignals keeps Kame from stopping on terminal access, like
// IgnoreJobControlSignals, but catches the signals instead of ignoring them,
// so children start with default dispositions and the terminal can stop
// them. SIGTSTP, SIGTTIN, and SIGTTOU arrive on the returned channel.
func CatchJobControlSignals() <-chan os.Signal {
	// The session's own modes, which background graphs return the terminal to.
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		if state, err := term.GetState(int(tty.Fd())); err == nil {
			sessionTerminalState.Store(state)
		}
		tty.Close()
	}
	ch := make(chan os.Signal, 4)
	jobControlNotify.Store(&ch)
	signal.Notify(ch, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU)
	return ch
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
