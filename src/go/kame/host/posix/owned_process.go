package posix

import (
	"errors"
	"io"
	"kame/host"
	"os/exec"
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
	timer   *time.Timer
}

func (p *ownedProcess) signal(signal syscall.Signal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.retired {
		killGroup(p.cmd.Process.Pid, signal)
	}
}
func (p *ownedProcess) wait() error {
	observed := observeProcessExit(p.cmd.Process.Pid)
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
	return p.wait()
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
	p.timer = time.AfterFunc(time.Duration(milliseconds)*time.Millisecond, func() {
		p.mu.Lock()
		active := !p.retired
		p.mu.Unlock()
		if active {
			r.terminate(host.ProcessTimedOut, 100)
		}
	})
}
