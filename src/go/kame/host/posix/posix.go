// Package posix implements the native host with Go's operating system APIs.
package posix

import (
	"bytes"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"kame/host"
	"kame/internal/c"
	"kame/internal/mem"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// These aliases preserve the POSIX package's public API while the portable
// process contract lives in host.
type Outcome = host.ProcessOutcome
type EventKind = host.ProcessEventKind
type DiagnosticCode = string
type Diagnostic = host.ProcessDiagnostic
type Request = host.ProcessRequest
type Event = host.ProcessEvent
type EventResult = host.ProcessEventResult

const (
	Exited                = host.ProcessExited
	TimedOut              = host.ProcessTimedOut
	Cancelled             = host.ProcessCancelled
	Failed                = host.ProcessFailed
	Started               = host.ProcessStarted
	Stdout                = host.ProcessStdout
	Stderr                = host.ProcessStderr
	Terminal              = host.ProcessTerminal
	DiagnosticHostFailure = "HOST_FAIL"
)

type running struct {
	snapshot     func() host.ProcessEvent
	nextSnapshot time.Time
	termination  sync.Once
	commandMu    sync.Mutex
	owned        map[*exec.Cmd]*ownedProcess
	stopped      chan struct{}
	halt         sync.Once
	stopIO       func()
	cmds         []*exec.Cmd
	outcome      atomic.Int32
	done         chan struct{}
}
type nativeHost struct {
	predicates  chan *predicateRequest
	waitFailure atomic.Bool
	mu          sync.Mutex
	processes   map[int64]*running
	events      chan host.ProcessEvent
	locks       map[int]*cacheLease
	closed      chan struct{}
	once        sync.Once
}
type Host struct {
	Readers     []recordCursor
	StdinRecord int64
	Alloc       mem.Allocator
	native      *nativeHost
	Memory      *host.MemoryResources
	start       time.Time
	pending     []host.ProcessEvent
}

func New(a mem.Allocator) *Host {
	return &Host{Alloc: a, Memory: host.NewMemoryResources(a), start: time.Now(), native: &nativeHost{processes: map[int64]*running{}, predicates: make(chan *predicateRequest, 64), events: make(chan host.ProcessEvent, 2), locks: map[int]*cacheLease{}, closed: make(chan struct{})}}
}
func (h *Host) SupportsExecutor(n, v string) bool { return n == "" || n == "local" }
func (h *Host) SupportsStreamOperands() bool      { return true }
func (h *Host) emit(e host.ProcessEvent) {
	select {
	case h.native.events <- e:
	case <-h.native.closed:
	}
}

type streamWriter struct {
	onTruncate func()
	mu         sync.Mutex
	h          *Host
	id         int64
	kind       host.ProcessEventKind
	capture    bytes.Buffer
	stopped    <-chan struct{}
	limit      int
	truncated  bool
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.limit > 0 {
		n := min(len(p), max(0, w.limit-w.capture.Len()))
		w.capture.Write(p[:n])
		firstOverflow := !w.truncated && n < len(p)
		w.truncated = w.truncated || n < len(p)
		if firstOverflow && w.onTruncate != nil {
			w.onTruncate()
		}
	} else if len(p) != 0 {
		w.truncated = true
	}
	event := host.ProcessEvent{Kind: w.kind, ID: w.id, Data: bytes.Clone(p)}
	select {
	case w.h.native.events <- event:
	case <-w.stopped:
	case <-w.h.native.closed:
	}
	return len(p), nil
}
func (h *Host) Start(r host.ProcessRequest) bool {
	if h == nil || h.native == nil {
		return false
	}
	if r.ID <= 0 {
		h.reject(r.ID, "invalid request")
		return false
	}
	if strings.IndexByte(string(r.Script), 0) >= 0 {
		h.reject(r.ID, "invalid request")
		return false
	}
	if r.InputMode != "" && r.InputMode != "closed" && r.InputMode != "invocation" {
		return false
	}
	if r.Topology && host.ProcessTopologyError(h.Alloc, r) != "" {
		return false
	}
	if r.CollectFailures && !r.Topology {
		return false
	}
	stages := r.Stages
	if len(stages) == 0 {
		if r.Topology {
			return false
		}
		args := r.Argv
		if len(args) == 0 {
			args = append(append([]string{}, r.Shell...), string(r.Script))
		}
		stages = []host.ProcessStage{{Argv: args}}
	}
	if r.InputMode == "invocation" && (h.StdinRecord != 0 || r.InputFD < 0 || r.InputRoot < 0 || r.InputRoot >= len(stages) || r.Input != "" || len(r.Stages) == 0) {
		return false
	}
	if r.Topology {
		return h.startTopology(r)
	}
	run := &running{done: make(chan struct{}), stopped: make(chan struct{})}
	run.stopIO = func() { run.halt.Do(func() { close(run.stopped) }) }
	h.native.mu.Lock()
	if _, ok := h.native.processes[r.ID]; ok {
		h.native.mu.Unlock()
		h.reject(r.ID, "request ID already active")
		return false
	}
	h.native.processes[r.ID] = run
	h.native.mu.Unlock()
	out := &streamWriter{h: h, id: r.ID, kind: host.ProcessStdout, limit: r.RetainBytes, stopped: run.stopped}
	errout := &streamWriter{h: h, id: r.ID, kind: host.ProcessStderr, limit: r.RetainBytes, stopped: run.stopped}
	if len(r.Argv) > 0 || len(r.Stages) > 0 {
		out.onTruncate = func() { h.Stop(r.ID, 100) }
	}
	var commands []*exec.Cmd
	var truncateFiles []*os.File
	var resources []io.Closer
	closeAll := func() {
		for _, f := range resources {
			f.Close()
		}
	}
	fail := func() bool {
		run.stopIO()
		for _, cmd := range run.cmds {
			if cmd.Process != nil {
				run.signalCommands(syscall.SIGKILL)
				run.waitCommand(cmd)
			}
		}
		closeAll()
		close(run.done)
		h.native.mu.Lock()
		delete(h.native.processes, r.ID)
		h.native.mu.Unlock()
		h.reject(r.ID, "cannot start process")
		h.pending[0].StdoutTruncated, h.pending[0].StderrTruncated = out.truncated, errout.truncated
		h.pending[0].Stages = make([]host.ProcessStageResult, len(stages))
		for i := range h.pending[0].Stages {
			h.pending[0].Stages[i] = host.ProcessStageResult{Status: 127, EffectiveStatus: 127, HasEffectiveStatus: true, Outcome: host.ProcessFailed}
		}
		return true
	}
	var previous *os.File
	for i, s := range stages {
		if len(s.Argv) == 0 || s.Kind != "" {
			return fail()
		}
		cmd := stageCommand(r, s)
		if cmd.Err != nil {
			return fail()
		}
		if cmd.Dir != "" {
			info, e := os.Stat(cmd.Dir)
			if e != nil || !info.IsDir() {
				return fail()
			}
		}
		cmd.Stderr = errout
		cmd.Stdout = out
		if previous != nil {
			cmd.Stdin = previous
		}
		input := s.Input
		if i == 0 && input == "" {
			input = r.Input
		}
		if input != "" {
			f, e := os.Open(input)
			if e != nil {
				return fail()
			}
			resources = append(resources, f)
			cmd.Stdin = f
		}
		if r.InputMode == "invocation" && i == r.InputRoot {
			fd, e := syscall.Dup(r.InputFD)
			if e != nil {
				return fail()
			}
			f := os.NewFile(uintptr(fd), "invocation input")
			resources = append(resources, f)
			cmd.Stdin = f
		}
		previous = nil
		if i < len(stages)-1 {
			rd, wr, e := os.Pipe()
			if e != nil {
				return fail()
			}
			resources = append(resources, rd, wr)
			cmd.Stdout = wr
			previous = rd
		}
		output := s.Output
		app := s.Append
		if i == len(stages)-1 && output == "" {
			output, app = r.Output, r.Append
		}
		if output != "" {
			flags := os.O_WRONLY | os.O_CREATE
			if app {
				flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
			}
			f, e := os.OpenFile(output, flags, 0666)
			if e != nil {
				return fail()
			}
			resources = append(resources, f)
			if !app {
				truncateFiles = append(truncateFiles, f)
			}
			cmd.Stdout = f
		}
		switch s.ErrorMode {
		case host.StderrStdout:
			cmd.Stderr = cmd.Stdout
		case host.StderrPresentation:
			cmd.Stderr = out
			if i < len(stages)-1 {
				cmd.Stderr = cmd.Stdout
			}
		case host.StderrFile:
			flags := os.O_WRONLY | os.O_CREATE
			if s.ErrorAppend {
				flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
			}
			f, e := os.OpenFile(s.Error, flags, 0666)
			if e != nil {
				return fail()
			}
			resources = append(resources, f)
			if !s.ErrorAppend {
				truncateFiles = append(truncateFiles, f)
			}
			cmd.Stderr = f
		}
		commands = append(commands, cmd)
	}
	for _, f := range truncateFiles {
		info, e := f.Stat()
		if e != nil || (info.Mode().IsRegular() && f.Truncate(0) != nil) {
			return fail()
		}
	}
	for i, cmd := range commands {
		if run.startCommand(cmd) != nil {
			return fail()
		}
		run.stageTimeout(cmd, stages[i].TimeoutMS)
	}
	closeAll()
	h.pending = append(h.pending, host.ProcessEvent{Kind: host.ProcessStarted, ID: r.ID, PID: int64(run.cmds[0].Process.Pid), PGID: int64(run.cmds[0].Process.Pid)})
	var timer *time.Timer
	if timeout := processTimeout(r); timeout > 0 {
		timer = time.AfterFunc(time.Duration(timeout)*time.Millisecond, func() { run.terminate(host.ProcessTimedOut, 100) })
	}
	go func() {
		defer close(run.done)
		if timer != nil {
			defer timer.Stop()
		}
		errorsByStage := make([]error, len(run.cmds))
		var waits sync.WaitGroup
		for index, cmd := range run.cmds {
			waits.Add(1)
			go func(index int, cmd *exec.Cmd) {
				defer waits.Done()
				errorsByStage[index] = run.waitCommand(cmd)
			}(index, cmd)
		}
		waits.Wait()
		terminal := host.ProcessEvent{Kind: host.ProcessTerminal, ID: r.ID, PID: int64(run.cmds[0].Process.Pid), PGID: int64(run.cmds[0].Process.Pid), RetainBytes: r.RetainBytes, HasEffectiveStatus: true}
		for index, cmd := range run.cmds {
			err := errorsByStage[index]
			if errors.Is(err, errObserveExit) {
				terminal.Diagnostic = host.ProcessDiagnostic{Code: DiagnosticHostFailure, Message: err.Error()}
			}
			status, sig := 0, 0
			if err != nil {
				status = 1
				if cmd.ProcessState != nil {
					status = cmd.ProcessState.ExitCode()
					if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
						sig = int(ws.Signal())
						status = 128 + sig
					}
				}
			}
			effective := status
			if sig == 0 && status > 0 && status < 256 && stages[index].Accepted[status] {
				effective = 0
			}
			stage := host.ProcessStageResult{Status: status, Signal: sig, EffectiveStatus: effective, HasEffectiveStatus: true}
			if effective != 0 {
				terminal.EffectiveStatus = effective
				terminal.EffectiveSignal = sig
			}
			terminal.Stages = append(terminal.Stages, stage)
			if status != 0 {
				terminal.Status, terminal.Signal = status, sig
			}
		}
		if len(r.Stages) == 0 && len(r.Argv) == 0 {
			terminal.Stages = nil
		}
		terminal.Outcome = host.ProcessOutcome(run.outcome.Load())
		if terminal.Diagnostic.Code != "" {
			terminal.Outcome = host.ProcessFailed
		}
		if h.native.waitFailure.Swap(false) {
			terminal.Outcome = host.ProcessFailed
			terminal.Diagnostic = host.ProcessDiagnostic{Code: DiagnosticHostFailure, Message: "waitpid failed"}
		}

		terminal.Stdout = bytes.Clone(out.capture.Bytes())
		terminal.Stderr = bytes.Clone(errout.capture.Bytes())
		terminal.StdoutTruncated, terminal.StderrTruncated = out.truncated, errout.truncated
		h.native.mu.Lock()
		delete(h.native.processes, r.ID)
		h.native.mu.Unlock()
		h.emit(terminal)
	}()
	return true
}
func killGroup(pid int, s syscall.Signal) { syscall.Kill(-pid, s) }
func (h *Host) reject(id int64, message string) {
	h.pending = append([]host.ProcessEvent{{Kind: host.ProcessTerminal, ID: id, Outcome: host.ProcessFailed, Diagnostic: host.ProcessDiagnostic{Code: DiagnosticHostFailure, Message: message}}}, h.pending...)
}
func (h *Host) Pump(ms int) bool {
	h.refreshSnapshots()
	h.drainPredicates()
	if len(h.pending) > 0 {
		return true
	}
	if ms > 0 {
		select {
		case e := <-h.native.events:
			h.pending = append(h.pending, e)
		case item := <-h.native.predicates:
			h.evaluatePredicate(item)
		case <-time.After(time.Duration(ms) * time.Millisecond):
		}
	}
	n := len(h.native.events)
	for j := 0; j < n; j++ {
		h.pending = append(h.pending, <-h.native.events)
	}
	return true
}
func (h *Host) Next() host.ProcessEventResult {
	if len(h.pending) == 0 {
		return host.ProcessEventResult{}
	}
	e := h.pending[0]
	h.pending[0] = host.ProcessEvent{}
	h.pending = h.pending[1:]
	return host.ProcessEventResult{OK: true, Event: e}
}
func (h *Host) Cancel(id int64) bool      { return h.cancel(id, 100) }
func (h *Host) Stop(id, grace int64) bool { return h.cancel(id, grace) }
func (h *Host) cancel(id, grace int64) bool {
	h.native.mu.Lock()
	r := h.native.processes[id]
	h.native.mu.Unlock()
	if r == nil {
		return false
	}
	r.terminate(host.ProcessCancelled, grace)
	return true
}
func (h *Host) CancelAll() {
	h.native.mu.Lock()
	var ids []int64
	for id := range h.native.processes {
		ids = append(ids, id)
	}
	h.native.mu.Unlock()
	for _, id := range ids {
		h.Cancel(id)
	}
}
func (h *Host) Active() int {
	h.native.mu.Lock()
	defer h.native.mu.Unlock()
	return len(h.native.processes)
}
func (h *Host) Free() {
	if h == nil {
		return
	}
	h.native.once.Do(func() {
		close(h.native.closed)
		h.CancelAll()
		h.native.mu.Lock()
		var runs []*running
		for _, r := range h.native.processes {
			runs = append(runs, r)
		}
		h.native.mu.Unlock()
		for _, r := range runs {
			<-r.done
		}
		for _, r := range h.Readers {
			km_record_close(r.FD)
		}
		for stripe := range h.native.locks {
			km_host_cache_unlock(h.native, c.Int(stripe))
		}
		h.Memory.Free()
	})
}
func (h *Host) ForceWaitpidFailureForTest() {
	h.native.waitFailure.Store(true)
	h.native.mu.Lock()
	defer h.native.mu.Unlock()
	for _, r := range h.native.processes {
		r.signalCommands(syscall.SIGKILL)
	}
}

var signalOnce sync.Once
var pendingSignal atomic.Int32

func InstallSignals() bool {
	signalOnce.Do(func() {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		go func() {
			for sig := range ch {
				n := int32(sig.(syscall.Signal))
				if !pendingSignal.CompareAndSwap(0, n) {
					pendingSignal.Store(-n)
				}
			}
		}()
	})
	return true
}
func TakeSignal() int             { return int(pendingSignal.Swap(0)) }
func km_cli_signal_pending() bool { return pendingSignal.Load() != 0 }
func terminal(f *os.File) bool {
	_, e := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	return e == nil
}
func StderrIsTerminal() bool { return terminal(os.Stderr) }
func StdoutIsTerminal() bool { return terminal(os.Stdout) }
func StdinIsTerminal() bool  { return terminal(os.Stdin) }
func StderrWidth() int {
	size, e := unix.IoctlGetWinsize(2, unix.TIOCGWINSZ)
	if e != nil {
		return 0
	}
	return int(size.Col)
}
func StderrHeight() int {
	size, e := unix.IoctlGetWinsize(2, unix.TIOCGWINSZ)
	if e != nil {
		return 0
	}
	return int(size.Row)
}
func Environment(a mem.Allocator) []string            { return os.Environ() }
func FreeEnvironment(a mem.Allocator, v []string)     {}
func km_cli_environment_names_case_insensitive() bool { return runtime.GOOS == "windows" }
func Pick(executable string, args []string, input string, selection *string) int {
	cmd := exec.Command(executable, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr
	b, e := cmd.Output()
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			return x.ExitCode()
		}
		return 127
	}
	*selection = string(b)
	return 0
}
func EnvironmentNameEqual(left string, right string) bool {
	return EnvironmentNameEqualForPlatform(left, right, km_cli_environment_names_case_insensitive())
}

// EnvironmentNameEqualForPlatform exposes the name policy without changing the
// native platform, so portable callers and tests can apply either rule.
func EnvironmentNameEqualForPlatform(left string, right string, windows bool) bool {
	if left == right {
		return true
	}
	if !windows {
		return false
	}
	for i, j := 0, 0; i < len(left) && j < len(right); {
		a, an := utf8.DecodeRuneInString(left[i:])
		b, bn := utf8.DecodeRuneInString(right[j:])
		if unicode.ToLower(a) != unicode.ToLower(b) {
			return false
		}
		i += an
		j += bn
		if i == len(left) || j == len(right) {
			return i == len(left) && j == len(right)
		}
	}
	return len(left) == 0 && len(right) == 0
}

func km_file_watch_stamp(name string, data []byte) bool {
	if len(data) != 98 {
		return false
	}
	clear(data)
	for link := 0; link < 2; link++ {
		var info unix.Stat_t
		var e error
		if link == 0 {
			e = unix.Stat(name, &info)
		} else {
			e = unix.Lstat(name, &info)
		}
		if e == unix.ENOENT || e == unix.ENOTDIR {
			continue
		}
		if e != nil {
			return false
		}
		offset := link * 49
		data[offset] = 1
		fields := []uint64{uint64(info.Dev), info.Ino, uint64(info.Mode), uint64(info.Size), uint64(info.Mtim.Sec*1e9 + info.Mtim.Nsec), uint64(info.Ctim.Sec*1e9 + info.Ctim.Nsec)}
		for i, v := range fields {
			binary.LittleEndian.PutUint64(data[offset+1+i*8:], v)
		}
	}
	return true
}

type cacheLease struct {
	file *os.File
	path string
	refs int
}

var cacheLeasesMu sync.Mutex
var cacheLeases = map[string]*cacheLease{}

func km_host_cache_lock(h *nativeHost, name string, stripe c.Int) c.Int {
	cacheLeasesMu.Lock()
	defer cacheLeasesMu.Unlock()
	if h.locks[int(stripe)] != nil {
		return 0
	}
	if lease := cacheLeases[name]; lease != nil {
		lease.refs++
		h.locks[int(stripe)] = lease
		return 0
	}
	if os.MkdirAll(filepath.Dir(name), 0700) != nil {
		return -1
	}
	f, e := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0666)
	if e != nil {
		return -1
	}
	status := LockFile(int(f.Fd()), name, "cache", true, false)
	if status != 0 {
		f.Close()
		return c.Int(status)
	}
	lease := &cacheLease{file: f, path: name, refs: 1}
	cacheLeases[name] = lease
	h.locks[int(stripe)] = lease
	return 0
}
func km_host_cache_unlock(h *nativeHost, stripe c.Int) {
	cacheLeasesMu.Lock()
	defer cacheLeasesMu.Unlock()
	if lease := h.locks[int(stripe)]; lease != nil {
		delete(h.locks, int(stripe))
		lease.refs--
		if lease.refs == 0 {
			UnlockFile(int(lease.file.Fd()), true)
			lease.file.Close()
			delete(cacheLeases, lease.path)
		}
	}
}
