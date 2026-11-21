package posix

import (
	"bufio"
	_ "embed"
	"errors"
	"io"
	"kame/host"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

//go:embed foreign_bash.sh
var bashBridgeScript string

type bashBridge struct {
	cmd     *exec.Cmd
	dir     string
	send    *os.File
	receive *os.File
	reply   *bufio.Reader
	process *ownedProcess
	done    chan struct{}
	stop    sync.Once
	busy    bool // guarded by the native host mutex
}

func startBashBridge(call host.ForeignShellCall) (*bashBridge, error) {
	dir, err := os.MkdirTemp("", "kash-shell-")
	if err != nil {
		return nil, err
	}
	w := &bashBridge{dir: dir, done: make(chan struct{})}
	started := false
	defer func() {
		if !started {
			os.RemoveAll(dir)
		}
	}()
	out, errOut := filepath.Join(dir, "out"), filepath.Join(dir, "err")
	for _, path := range []string{out, errOut} {
		if err := unix.Mkfifo(path, 0600); err != nil {
			return nil, err
		}
	}
	input, send, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer input.Close()
	receive, output, err := os.Pipe()
	if err != nil {
		send.Close()
		return nil, err
	}
	defer output.Close()
	w.send, w.receive, w.reply = send, receive, bufio.NewReader(receive)
	w.cmd = exec.Command(call.Executable, "--noprofile", "--norc", "-c", bashBridgeScript, "kash-bash", out, errOut)
	w.cmd.Dir = call.Directory
	w.cmd.Env = []string{}
	for _, assignment := range call.Environment {
		name, _, _ := strings.Cut(assignment, "=")
		switch name {
		case "BASH_ENV", "BASHOPTS", "SHELLOPTS":
			continue
		}
		if !strings.HasPrefix(name, "BASH_FUNC_") && !strings.HasPrefix(name, "_kash_bridge_") {
			w.cmd.Env = append(w.cmd.Env, assignment)
		}
	}
	w.cmd.ExtraFiles = []*os.File{input, output}
	w.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := w.cmd.Start(); err != nil {
		send.Close()
		receive.Close()
		return nil, err
	}
	w.process = &ownedProcess{cmd: w.cmd}
	started = true
	go func() {
		// Keep the PID waitable until signalling the owned process group.
		observeProcess(w.cmd.Process.Pid, func(stopped bool, _ int) {
			if stopped {
				w.process.signal(syscall.SIGKILL)
			}
		})
		w.process.mu.Lock()
		killGroup(w.cmd.Process.Pid, syscall.SIGKILL)
		w.process.retired = true
		w.process.mu.Unlock()
		w.cmd.Wait()
		close(w.done)
	}()
	return w, nil
}

func (w *bashBridge) close() {
	w.stop.Do(func() {
		w.process.signal(syscall.SIGKILL)
		w.send.Close()
		w.receive.Close()
		<-w.done
		os.RemoveAll(w.dir)
	})
}

func (h *Host) CloseForeignShell(id int64) {
	h.native.mu.Lock()
	worker := h.native.foreignShells[id]
	delete(h.native.foreignShells, id)
	h.native.mu.Unlock()
	if worker != nil {
		worker.close()
	}
}

func (h *Host) SupportsForeignShell(name string) bool { return name == "bash" }

func (h *Host) StartForeignShell(id int64, call host.ForeignShellCall) bool {
	if !h.SupportsForeignShell(call.Adapter) {
		return false
	}
	key := call.Worker
	var worker *bashBridge
	if key == 0 {
		key = id
		var err error
		worker, err = startBashBridge(call)
		if err != nil {
			return false
		}
	}
	h.native.mu.Lock()
	if worker == nil {
		worker = h.native.foreignShells[key]
	} else {
		if h.native.foreignShells == nil {
			h.native.foreignShells = make(map[int64]*bashBridge)
		}
		h.native.foreignShells[key] = worker
	}
	if worker == nil || worker.busy {
		h.native.mu.Unlock()
		return false
	}
	worker.busy = true
	r := &running{host: h, id: id, done: make(chan struct{}), stopped: make(chan struct{})}
	r.stopIO = func() {
		close(r.stopped)
		worker.process.signal(syscall.SIGKILL)
		worker.send.Close()
		worker.receive.Close()
	}
	h.native.processes[id] = r
	h.native.mu.Unlock()
	go h.exchangeForeignShell(r, worker, key, call)
	return true
}

func (h *Host) exchangeForeignShell(r *running, worker *bashBridge, key int64, call host.ForeignShellCall) {
	defer func() {
		close(r.done)
		h.native.mu.Lock()
		delete(h.native.processes, r.id)
		h.native.mu.Unlock()
	}()
	var timer *time.Timer
	if call.TimeoutMS > 0 {
		timer = time.AfterFunc(time.Duration(call.TimeoutMS)*time.Millisecond, func() { r.terminate(host.ProcessTimedOut, 0) })
	}
	status, reply, err := worker.exchange(call, func(kind host.ProcessEventKind) io.Writer {
		return &streamWriter{h: h, id: r.id, kind: kind, stopped: r.stopped}
	}, r.stopped)
	if timer != nil {
		if !timer.Stop() {
			<-r.stopped
		}
	}
	// Disarm cancellation before this worker can be used by the next exchange.
	r.termination.Do(func() {})
	outcome := host.ProcessOutcome(r.outcome.Load())
	if err != nil && outcome == host.ProcessExited {
		outcome = host.ProcessFailed
	}
	if outcome != host.ProcessExited || call.Worker == 0 && status != 0 {
		h.CloseForeignShell(key)
	}
	h.native.mu.Lock()
	worker.busy = false
	h.native.mu.Unlock()
	h.emit(host.ProcessEvent{Kind: host.ProcessTerminal, ID: r.id, Outcome: outcome, Status: status, ForeignShell: reply})
}

func (w *bashBridge) exchange(call host.ForeignShellCall, writer func(host.ProcessEventKind) io.Writer, stopped <-chan struct{}) (int, *host.ForeignShellReply, error) {
	var readers, holds []*os.File
	defer func() {
		for _, f := range readers {
			f.Close()
		}
		for _, f := range holds {
			f.Close()
		}
	}()
	for _, name := range []string{"out", "err"} {
		path := filepath.Join(w.dir, name)
		r, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return 0, nil, err
		}
		readers = append(readers, r)
		// Keep each FIFO open until Bash has opened and closed its side.
		hold, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return 0, nil, err
		}
		holds = append(holds, hold)
	}
	copies := make(chan error, len(readers))
	for i, reader := range readers {
		destination := writer([]host.ProcessEventKind{host.ProcessStdout, host.ProcessStderr}[i])
		go func() {
			_, err := io.Copy(destination, reader)
			copies <- err
		}()
	}
	status, reply, err := w.transact(call)
	for _, f := range holds {
		f.Close()
	}
	if err != nil {
		// Force EOF on a broken exchange, but drain already-produced output.
		w.process.signal(syscall.SIGKILL)
	}
	for range readers {
		select {
		case copyErr := <-copies:
			err = errors.Join(err, copyErr)
		case <-stopped:
			return 0, nil, io.ErrClosedPipe
		}
	}
	return status, reply, err
}

func (w *bashBridge) transact(call host.ForeignShellCall) (int, *host.ForeignShellReply, error) {
	mode := "call"
	if call.Worker == 0 {
		mode = "source"
	}
	fields := []string{mode, call.Directory, call.Command}
	for _, assignment := range call.Set {
		name, _, _ := strings.Cut(assignment, "=")
		if bashBridgeSyncName(name) {
			fields = append(fields, assignment)
		}
	}
	fields = append(fields, "")
	for _, name := range call.Unset {
		if bashBridgeSyncName(name) {
			fields = append(fields, name)
		}
	}
	fields = append(fields, "", strconv.Itoa(len(call.Args)))
	fields = append(fields, call.Args...)
	for _, field := range fields {
		if strings.ContainsRune(field, 0) {
			return 0, nil, errors.New("NUL in foreign shell request")
		}
	}
	if _, err := io.WriteString(w.send, strings.Join(fields, "\x00")+"\x00"); err != nil {
		return 0, nil, err
	}
	const replyLimit = 4 << 20
	remaining := replyLimit
	reply := &host.ForeignShellReply{}
	for _, section := range []*[]string{&reply.Before, &reply.After, &reply.Functions} {
		var token strings.Builder
		for {
			part, err := w.reply.ReadSlice(0)
			remaining -= len(part)
			if remaining < 0 {
				return 0, nil, errors.New("foreign shell reply exceeds byte limit")
			}
			if err == bufio.ErrBufferFull {
				token.Write(part)
				continue
			}
			if err != nil {
				return 0, nil, err
			}
			token.Write(part[:len(part)-1])
			if token.Len() == 0 {
				break
			}
			*section = append(*section, token.String())
			token.Reset()
		}
	}
	statusToken, err := w.reply.ReadSlice(0)
	if err != nil {
		return 0, nil, err
	}
	status, err := strconv.Atoi(string(statusToken[:len(statusToken)-1]))
	if err != nil || status < 0 || status > 255 {
		return 0, nil, errors.New("invalid foreign shell status")
	}
	return status, reply, nil
}

func bashBridgeSyncName(name string) bool {
	switch name {
	case "SHLVL", "PWD", "OLDPWD", "_", "BASH_ENV", "BASHOPTS", "SHELLOPTS":
		return false
	}
	return !strings.HasPrefix(name, "_kash_bridge_")
}
