package posix

import (
	"errors"
	"io"
	"kame/host"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"
)

var errObserveExit = errors.New("cannot observe process exit")

// Keep a waitable leader until its descendants have been signalled. The mutex
// prevents a timer or cancellation from signalling a PID/group after reaping,
// when that numeric identity could have been reassigned to another process.
type ownedProcess struct {
	cmd     *exec.Cmd
	mu      sync.Mutex
	retired bool
	timer   *pausableTimer
	stopped bool // guarded by running.control
}

func (p *ownedProcess) signal(signal syscall.Signal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.retired {
		killGroup(p.cmd.Process.Pid, signal)
	}
}
func (p *ownedProcess) wait(r *running) error {
	observed := observeProcess(p.cmd.Process.Pid, func(stopped bool, signal int) { r.stageChanged(p, stopped, signal) })
	// An exit can supersede an unobserved continue.
	r.stageChanged(p, false, 0)
	p.mu.Lock()
	killGroup(p.cmd.Process.Pid, syscall.SIGKILL)
	p.retired = true
	if p.timer != nil {
		p.timer.Stop()
	}
	p.mu.Unlock()
	result := p.cmd.Wait()
	if observed != nil {
		return errors.Join(errObserveExit, observed, result)
	}
	return result
}
func (r *running) startCommand(cmd *exec.Cmd) error {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	select {
	case <-r.stopped:
		return io.ErrClosedPipe
	default:
	}
	if e := cmd.Start(); e != nil {
		return e
	}
	if r.owned == nil {
		r.owned = map[*exec.Cmd]*ownedProcess{}
	}
	r.owned[cmd] = &ownedProcess{cmd: cmd}
	r.cmds = append(r.cmds, cmd)
	return nil
}
func (r *running) waitCommand(cmd *exec.Cmd) error {
	r.commandMu.Lock()
	p := r.owned[cmd]
	r.commandMu.Unlock()
	return p.wait(r)
}
func (r *running) signalCommands(signal syscall.Signal) {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	for _, p := range r.owned {
		p.signal(signal)
	}
}

func (r *running) terminate(outcome host.ProcessOutcome, grace int64) {
	r.termination.Do(func() {
		r.outcome.Store(int32(outcome))
		if r.stopIO != nil {
			r.stopIO()
		}
		signal := syscall.SIGKILL
		if grace > 0 {
			signal = syscall.SIGTERM
		}
		r.signalCommands(signal)
		// A stopped group must run to observe SIGTERM.
		r.signalCommands(syscall.SIGCONT)
		if grace > 0 {
			go func() {
				timer := time.NewTimer(time.Duration(grace) * time.Millisecond)
				defer timer.Stop()
				select {
				case <-r.done:
				case <-timer.C:
					r.signalCommands(syscall.SIGKILL)
				}
			}()
		}
	})
	// An unconditional shutdown also escalates a previous graceful stop.
	if grace <= 0 {
		r.signalCommands(syscall.SIGKILL)
	}
}
func processTimeout(r host.ProcessRequest) int64 { return r.TimeoutMS }

// Stage deadlines retire the graph only while that stage is still running.
func (r *running) stageTimeout(cmd *exec.Cmd, milliseconds int64) {
	if milliseconds <= 0 {
		return
	}
	r.commandMu.Lock()
	p := r.owned[cmd]
	r.commandMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired {
		return
	}
	p.timer = r.newTimer(milliseconds, func() {
		p.mu.Lock()
		active := !p.retired
		p.mu.Unlock()
		if active {
			r.terminate(host.ProcessTimedOut, 100)
		}
	})
}

// pausableTimer is a deadline that does not advance while paused.
type pausableTimer struct {
	mu        sync.Mutex
	fn        func()
	remaining time.Duration
	started   time.Time
	timer     *time.Timer
	stopped   bool
}

func (t *pausableTimer) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	if t.timer != nil {
		t.timer.Stop()
	}
}

func (t *pausableTimer) done() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

func (t *pausableTimer) pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil && t.timer.Stop() {
		t.remaining -= time.Since(t.started)
	}
	t.timer = nil
}

func (t *pausableTimer) resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.timer != nil {
		return
	}
	t.started = time.Now()
	t.timer = time.AfterFunc(max(t.remaining, 0), func() {
		t.mu.Lock()
		t.stopped = true
		t.mu.Unlock()
		t.fn()
	})
}

// newTimer starts a request deadline, paused while the request is.
func (r *running) newTimer(milliseconds int64, fn func()) *pausableTimer {
	t := &pausableTimer{fn: fn, remaining: time.Duration(milliseconds) * time.Millisecond}
	r.control.Lock()
	// Per-record workers start many deadlines; drop the finished ones.
	r.timers = slices.DeleteFunc(r.timers, (*pausableTimer).done)
	r.timers = append(r.timers, t)
	paused := r.paused
	r.control.Unlock()
	if !paused {
		t.resume()
	}
	return t
}

// Suspension (003): a request is paused while Kame suspended it or any of
// its stages is stopped. A stage stopped by a signal stops the whole request
// and reports one ProcessStopped; ProcessContinued follows once every
// stopped stage runs again. A terminal handoff is released while stopped.

// stageChanged records a stage's stop, continue, or exit, from its waiter.
func (r *running) stageChanged(p *ownedProcess, stopped bool, signal int) {
	r.control.Lock()
	var event host.ProcessEventKind = -1
	if stopped != p.stopped {
		p.stopped = stopped
		if stopped {
			r.stoppedStages++
		} else {
			r.stoppedStages--
		}
	}
	switch {
	case stopped && r.stoppedStages == 1 && !r.suspended && !r.reportedStop:
		event, r.reportedStop = host.ProcessStopped, true
	case r.stoppedStages == 0 && r.reportedStop && !r.suspended:
		event, r.reportedStop = host.ProcessContinued, false
	}
	r.updatePausedLocked()
	r.control.Unlock()
	if event == host.ProcessStopped {
		r.signalCommands(syscall.SIGSTOP) // Peers must not run against a stopped stage.
	}
	if event >= 0 && r.host != nil {
		r.host.emit(host.ProcessEvent{Kind: event, ID: r.id, Signal: signal})
	}
}

// updatePausedLocked pauses or resumes deadlines and releases the terminal
// when the request stops; r.control is held.
func (r *running) updatePausedLocked() {
	paused := r.suspended || r.stoppedStages > 0
	if paused == r.paused {
		return
	}
	r.paused = paused
	for _, t := range r.timers {
		if paused {
			t.pause()
		} else {
			t.resume()
		}
	}
	if paused {
		r.handoff.release()
	}
}

func (r *running) suspend() {
	r.control.Lock()
	r.suspended = true
	r.updatePausedLocked()
	r.control.Unlock()
	r.signalCommands(syscall.SIGSTOP)
}

// resume continues the request; foreground first returns a released
// terminal handoff to it. When another graph of the job already took the
// terminal, this one continues in the background.
func (r *running) resume(foreground bool) error {
	r.control.Lock()
	var err error
	if foreground {
		if err = r.handoff.reacquire(); errors.Is(err, errTerminalBusy) {
			err = nil
		}
	}
	if err == nil {
		r.suspended = false
		if r.stoppedStages == 0 {
			r.updatePausedLocked()
		}
	}
	r.control.Unlock()
	if err == nil {
		r.signalCommands(syscall.SIGCONT)
	}
	return err
}

// signal delivers one signal to every group, continuing a paused request
// for a signal that does not stop it.
func (r *running) signal(signal syscall.Signal) {
	r.signalCommands(signal)
	switch signal {
	case syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU:
		return
	}
	r.control.Lock()
	paused := r.paused
	r.control.Unlock()
	if paused {
		r.resume(false)
	}
}
