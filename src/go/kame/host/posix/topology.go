package posix

import (
	"bufio"
	"bytes"
	"encoding/binary"
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
	"unicode/utf8"
)

type graphPipe struct{ read, write *os.File }
type synchronizedWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *synchronizedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(b)
}

type operatorExit int

func (e operatorExit) Error() string { return "stream operator failed" }
func processExit(err error, cmd *exec.Cmd, stage host.ProcessStage) host.ProcessStageResult {
	result := host.ProcessStageResult{HasEffectiveStatus: true}
	if errors.Is(err, errObserveExit) {
		result.Outcome = host.ProcessFailed
	}
	if err != nil {
		result.Status = 1
		if cmd.ProcessState != nil {
			result.Status = cmd.ProcessState.ExitCode()
			if state, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && state.Signaled() {
				result.Signal = int(state.Signal())
				result.Status = 128 + result.Signal
			}
		}
	}
	result.EffectiveStatus = result.Status
	if result.Signal == 0 && result.Status > 0 && result.Status < 256 && stage.Accepted[result.Status] {
		result.EffectiveStatus = 0
	}
	return result
}
func stageCommand(r host.ProcessRequest, s host.ProcessStage) *exec.Cmd {
	cmd := exec.Command(s.Argv[0], s.Argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Dir = s.Directory
	if cmd.Dir == "" {
		cmd.Dir = r.Directory
	}
	cmd.Env = append([]string{}, r.Environment...)
	for _, replacement := range s.Environment {
		key, _, _ := strings.Cut(replacement, "=")
		for j := len(cmd.Env) - 1; j >= 0; j-- {
			name, _, _ := strings.Cut(cmd.Env[j], "=")
			if EnvironmentNameEqual(key, name) {
				cmd.Env = append(cmd.Env[:j], cmd.Env[j+1:]...)
			}
		}
		cmd.Env = append(cmd.Env, replacement)
	}
	// Resolve with the child's cwd and merged environment. exec.Command's
	// default lookup consults the parent PATH before Cmd.Env is assigned.
	lookupEnv := cmd.Env
	hasPath := false
	for _, entry := range lookupEnv {
		if strings.HasPrefix(entry, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath {
		lookupEnv = append(append([]string{}, lookupEnv...), "PATH=/bin:/usr/bin")
	}
	dir := cmd.Dir
	if dir == "" {
		dir = "."
	}
	if absolute, e := filepath.Abs(dir); e == nil {
		dir = absolute
	}
	resolved := ResolveToolForPlatform(nil, s.Argv[0], dir, lookupEnv, false)
	if resolved == "" {
		cmd.Err = exec.ErrNotFound
	} else {
		cmd.Path, cmd.Err = resolved, nil
	}
	return cmd
}
func (h *Host) startTopology(r host.ProcessRequest) bool {
	run := &running{done: make(chan struct{}), stopped: make(chan struct{})}
	h.native.mu.Lock()
	if h.native.processes[r.ID] != nil {
		h.native.mu.Unlock()
		h.reject(r.ID, "request ID already active")
		return false
	}
	h.native.processes[r.ID] = run
	h.native.mu.Unlock()
	count := len(r.Stages)
	nodes := make([]nodeMeter, count)
	meters := make([]edgeMeter, len(r.Edges))
	pipes := make([]graphPipe, len(r.Edges))
	inputs, outputs := make([][]int, count), make([][]int, count)
	operands := make([][]int, count)
	var files []*os.File
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			for _, f := range files {
				f.Close()
			}
		})
	}
	run.stopIO = func() { run.halt.Do(func() { close(run.stopped) }); closeAll() }
	var parentCopies []*os.File
	var truncateFiles []*os.File
	commands := make([]*exec.Cmd, count)
	workerTemplates := make([]*exec.Cmd, count)
	readers := make([][]io.Reader, count)
	writers := make([][]io.Writer, count)
	sinks := make([]io.Writer, count)
	out := &streamWriter{h: h, id: r.ID, kind: host.ProcessStdout, limit: r.RetainBytes, stopped: run.stopped, onTruncate: func() { h.Stop(r.ID, 100) }}
	errout := &streamWriter{h: h, id: r.ID, kind: host.ProcessStderr, limit: r.RetainBytes, stopped: run.stopped}
	fail := func(message string) bool {
		run.stopIO()
		run.signalCommands(syscall.SIGKILL)
		for _, cmd := range run.cmds {
			run.waitCommand(cmd)
		}
		close(run.done)
		h.native.mu.Lock()
		delete(h.native.processes, r.ID)
		h.native.mu.Unlock()
		h.reject(r.ID, message)
		h.pending[0].StdoutTruncated, h.pending[0].StderrTruncated = out.truncated, errout.truncated
		h.pending[0].Stages = make([]host.ProcessStageResult, count)
		return true
	}
	for i, edge := range r.Edges {
		rd, wr, e := os.Pipe()
		if e != nil {
			return fail("cannot create graph pipe")
		}
		pipes[i] = graphPipe{read: rd, write: wr}
		files = append(files, rd, wr)
		meters[i].framed = r.Stages[edge.From].Framed
		if r.Stages[edge.From].Kind != "" {
			meters[i].measurement = 1
		} else if r.Stages[edge.To].Kind != "" {
			meters[i].measurement = 2
		}
		meters[i].queued = r.Stages[edge.From].Overflow != 0
		if edge.Argument == 0 {
			inputs[edge.To] = append(inputs[edge.To], i)
		} else {
			operands[edge.To] = append(operands[edge.To], i)
		}
		outputs[edge.From] = append(outputs[edge.From], i)
	}
	openFile := func(name, dir string, write, appendOutput bool) (*os.File, error) {
		if !filepath.IsAbs(name) && dir != "" {
			name = filepath.Join(dir, name)
		}
		flags := os.O_RDONLY
		if write {
			flags = os.O_WRONLY | os.O_CREATE
			if appendOutput {
				flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
			}
		}
		f, e := os.OpenFile(name, flags, 0666)
		if e == nil {
			files = append(files, f)
			if write && !appendOutput {
				truncateFiles = append(truncateFiles, f)
			}
		}
		return f, e
	}
	for i, s := range r.Stages {
		dir := s.Directory
		if dir == "" {
			dir = r.Directory
		}
		for _, index := range inputs[i] {
			if s.Kind == "" {
				readers[i] = append(readers[i], pipes[index].read)
			} else {
				readers[i] = append(readers[i], &meteredReader{file: pipes[index].read, meter: &meters[index], node: &nodes[i]})
			}
		}
		for _, index := range outputs[i] {
			if s.Kind == "" {
				writers[i] = append(writers[i], pipes[index].write)
			} else {
				writers[i] = append(writers[i], &meteredWriter{file: pipes[index].write, meter: &meters[index], node: &nodes[i]})
			}
		}
		if len(writers[i]) == 0 {
			sinks[i] = out
			if s.Kind == "err" {
				sinks[i] = errout
			}
		} else if len(writers[i]) == 1 {
			sinks[i] = writers[i][0]
		} else {
			sinks[i] = io.MultiWriter(writers[i]...)
		}
		input := s.Input
		if i == 0 && input == "" {
			input = r.Input
		}
		if input != "" {
			f, e := openFile(input, dir, false, false)
			if e != nil {
				return fail("cannot open graph input")
			}
			readers[i] = []io.Reader{f}
		}
		if r.InputMode == "invocation" && i == r.InputRoot {
			fd, e := syscall.Dup(r.InputFD)
			if e != nil {
				return fail("cannot borrow graph input")
			}
			f := os.NewFile(uintptr(fd), "graph invocation input")
			files = append(files, f)
			readers[i] = []io.Reader{f}
		}
		path, app := s.Output, s.Append
		if len(outputs[i]) == 0 && path == "" && s.Kind != "err" {
			path, app = r.Output, r.Append
		}
		if path != "" {
			f, e := openFile(path, dir, true, app)
			if e != nil {
				return fail("cannot open graph output")
			}
			if len(outputs[i]) > 0 || s.Kind == "copy" {
				sinks[i] = io.MultiWriter(sinks[i], f)
			} else {
				sinks[i] = f
			}
		}
		if s.Kind != "" && s.Kind != "each" {
			continue
		}
		cmd := stageCommand(r, s)
		for _, edge := range operands[i] {
			cmd.Args[r.Edges[edge].Argument] = "/dev/fd/" + strconv.Itoa(3+len(cmd.ExtraFiles))
			cmd.ExtraFiles = append(cmd.ExtraFiles, pipes[edge].read)
			parentCopies = append(parentCopies, pipes[edge].read)
		}
		if s.Kind == "each" {
			workerTemplates[i] = cmd
		} else {
			commands[i] = cmd
		}
		cmd.Stdout = sinks[i]
		cmd.Stderr = errout
		if len(readers[i]) == 1 {
			cmd.Stdin = readers[i][0]
		}
		switch s.ErrorMode {
		case host.StderrStdout:
			cmd.Stderr = cmd.Stdout
		case host.StderrPresentation:
			cmd.Stderr = out
			for _, edge := range outputs[i] {
				kind := r.Stages[r.Edges[edge].To].Kind
				if kind != "out" && kind != "err" {
					cmd.Stderr = cmd.Stdout
					break
				}
			}
		case host.StderrFile:
			f, e := openFile(s.Error, dir, true, s.ErrorAppend)
			if e != nil {
				return fail("cannot open graph stderr")
			}
			cmd.Stderr = f
		}
		if cmd.Err != nil {
			return fail("cannot resolve graph stage")
		}
		if cmd.Dir != "" {
			info, e := os.Stat(cmd.Dir)
			if e != nil || !info.IsDir() {
				return fail("cannot open graph stage directory")
			}
		}
		if s.Kind == "each" {
			continue
		}
		for _, edge := range inputs[i] {
			parentCopies = append(parentCopies, pipes[edge].read)
		}
		for _, edge := range outputs[i] {
			parentCopies = append(parentCopies, pipes[edge].write)
		}
	}
	// Open every input and sink before truncating any existing output or
	// launching a process. A malformed later branch must leave prior sinks intact.
	for _, f := range truncateFiles {
		info, e := f.Stat()
		if e != nil {
			return fail("cannot inspect graph sink")
		}
		if info.Mode().IsRegular() && f.Truncate(0) != nil {
			return fail("cannot truncate graph sink")
		}
	}
	for i, cmd := range commands {
		if cmd == nil {
			continue
		}
		if run.startCommand(cmd) != nil {
			return fail("cannot start graph stage")
		}
		run.stageTimeout(cmd, r.Stages[i].TimeoutMS)
	}
	for _, f := range parentCopies {
		f.Close()
	}
	var pid int64
	if len(run.cmds) > 0 {
		pid = int64(run.cmds[0].Process.Pid)
	}
	h.pending = append(h.pending, host.ProcessEvent{Kind: host.ProcessStarted, ID: r.ID, PID: pid, PGID: pid})
	run.snapshot = func() host.ProcessEvent {
		states := make([]int, count)
		for i := range nodes {
			states[i] = nodes[i].state()
		}
		return host.ProcessEvent{Kind: host.ProcessSnapshot, ID: r.ID, StageStates: states, EdgeMetrics: edgeSnapshots(meters)}
	}
	var timer *time.Timer
	if timeout := processTimeout(r); timeout > 0 {
		timer = time.AfterFunc(time.Duration(timeout)*time.Millisecond, func() { run.terminate(host.ProcessTimedOut, 100) })
	}
	results := make([]host.ProcessStageResult, count)
	drainCopies := false
	for _, stage := range r.Stages {
		if stage.Kind == "copy" {
			drainCopies = true
		}
	}
	for _, stage := range r.Stages {
		if stage.Kind != "" && stage.Kind != "copy" {
			drainCopies = false
			break
		}
	}
	var fatalOnce sync.Once
	var fatalResult host.ProcessStageResult
	var fatalKind string
	fatal := false
	retireFailure := func(kind string, result host.ProcessStageResult) {
		if result.EffectiveStatus == 0 || (r.CollectFailures && result.Signal == 0 && kind == "") {
			return
		}
		fatalOnce.Do(func() {
			fatal = true
			fatalResult = result
			fatalKind = kind
			if !drainCopies || kind != "" {
				run.stopIO()
			}
			run.signalCommands(syscall.SIGKILL)
		})
	}
	var workers sync.WaitGroup
	workers.Add(count)
	for i, s := range r.Stages {
		go func(i int, s host.ProcessStage) {
			defer workers.Done()
			if cmd := commands[i]; cmd != nil {
				e := run.waitCommand(cmd)
				results[i] = processExit(e, cmd, s)
				nodes[i].complete(results[i])
				retireFailure(s.Kind, results[i])
				return
			}
			defer func() {
				for _, edge := range inputs[i] {
					pipes[edge].read.Close()
				}
				for _, edge := range outputs[i] {
					pipes[edge].write.Close()
				}
			}()
			var e error
			var result host.ProcessStageResult
			if s.Kind == "each" {
				result = runEach(r, s, workerTemplates[i], run, readers[i][0], sinks[i])
			} else if s.Overflow != 0 {
				var failureMu sync.Mutex
				var first *host.ProcessStageResult
				e = runLossyBroadcast(readers[i][0], writers[i], s, func(e error) {
					failureMu.Lock()
					defer failureMu.Unlock()
					if first == nil {
						result := operatorResult(e, run)
						first = &result
						retireFailure(s.Kind, result)
					}
				})
				failureMu.Lock()
				result = operatorResult(e, run)
				if first != nil {
					result = *first
				}
				failureMu.Unlock()
			} else {
				var firstFailure *host.ProcessStageResult
				e = runOperator(r, i, readers[i], writers[i], sinks[i], func(node int, data []byte) int { return h.streamItem(r, run, node, data) }, func(e error) {
					failure := operatorResult(e, run)
					firstFailure = &failure
					retireFailure(s.Kind, failure)
				})
				result = operatorResult(e, run)
				if firstFailure != nil {
					result = *firstFailure
				}
			}

			results[i] = result
			nodes[i].complete(result)
			retireFailure(s.Kind, result)
		}(i, s)
	}
	go func() {
		workers.Wait()
		if timer != nil {
			timer.Stop()
		}
		closeAll()
		e := host.ProcessEvent{Kind: host.ProcessTerminal, ID: r.ID, PID: pid, PGID: pid, Stages: results, EdgeMetrics: edgeSnapshots(meters), Outcome: host.ProcessOutcome(run.outcome.Load()), HasEffectiveStatus: true, RetainBytes: r.RetainBytes, Stdout: bytes.Clone(out.capture.Bytes()), Stderr: bytes.Clone(errout.capture.Bytes()), StdoutTruncated: out.truncated, StderrTruncated: errout.truncated}
		for _, stage := range results {
			if stage.Status != 0 {
				e.Status, e.Signal = stage.Status, stage.Signal
			}
			if stage.EffectiveStatus != 0 {
				e.EffectiveStatus, e.EffectiveSignal = stage.EffectiveStatus, stage.Signal
			}
		}
		if fatal && e.Outcome == host.ProcessExited {
			e.Status, e.Signal = fatalResult.Status, fatalResult.Signal
			e.EffectiveStatus, e.EffectiveSignal = fatalResult.EffectiveStatus, fatalResult.Signal
		}

		// Preserve an operator's causal framing/predicate error over SIGPIPE
		// induced upstream while retiring the graph.
		for i, stage := range results {
			if !(fatal && fatalKind != "" && fatalKind != "each") && r.Stages[i].Kind != "" && r.Stages[i].Kind != "each" && ((stage.Status >= 120 && stage.Status <= 124) || stage.Status == 126) {
				e.Status, e.Signal = stage.Status, 0
				e.EffectiveStatus, e.EffectiveSignal = stage.EffectiveStatus, 0
			}
		}

		h.native.mu.Lock()
		delete(h.native.processes, r.ID)
		h.native.mu.Unlock()
		h.emit(e)
		close(run.done)
	}()
	return true
}
func readFrame(reader io.Reader, budget int) ([]byte, error) {
	var length [4]byte
	n, e := io.ReadFull(reader, length[:])
	if e == io.EOF && n == 0 {
		return nil, io.EOF
	}
	if e != nil {
		return nil, operatorExit(124)
	}
	size := binary.LittleEndian.Uint32(length[:])
	if size == 0 || size > host.RecordLimit {
		return nil, operatorExit(121)
	}
	if budget > 0 && int(size) > budget {
		return nil, operatorExit(126)
	}
	b := make([]byte, int(size)+4)
	copy(b, length[:])
	if _, e = io.ReadFull(reader, b[4:]); e != nil {
		return nil, operatorExit(124)
	}
	if reader, ok := reader.(interface{ frameRead(int) }); ok {
		reader.frameRead(int(size))
	}
	return b, nil
}
func runOperator(r host.ProcessRequest, index int, readers []io.Reader, writers []io.Writer, sink io.Writer, item func(int, []byte) int, failed func(error)) error {
	s := r.Stages[index]
	if s.Kind == "merge" {
		shared := &synchronizedWriter{out: sink}
		copyInput := func(reader io.Reader) error {
			if !s.Framed {
				return copyBudget(shared, reader, s.BufferBytes)
			}
			for {
				b, e := readFrame(reader, s.BufferBytes)
				if e == io.EOF {
					return nil
				}
				if e != nil {
					return e
				}
				if _, e = shared.Write(b); e != nil {
					return e
				}
			}
		}
		if s.Order != 0 {
			active := append([]io.Reader{}, readers...)
			for len(active) > 0 {
				for i := 0; i < len(active); {
					b, e := readFrame(active[i], s.BufferBytes)
					if e == io.EOF {
						active = append(active[:i], active[i+1:]...)
						continue
					}
					if e != nil {
						return e
					}
					if _, e = shared.Write(b); e != nil {
						return e
					}
					i++
				}
			}
			return nil
		}
		failures := make(chan error, len(readers))
		for _, reader := range readers {
			go func(reader io.Reader) { failures <- copyInput(reader) }(reader)
		}
		var failure error
		for range readers {
			if e := <-failures; e != nil && failure == nil {
				failure = e
				failed(e)
			}
		}
		return failure
	}
	if len(readers) != 1 {
		return operatorExit(124)
	}
	input := readers[0]
	if s.Kind == "frame" {
		reader := bufio.NewReader(input)
		for {
			var line []byte
			for {
				part, e := reader.ReadSlice('\n')
				if len(line)+len(part) > host.RecordLimit {
					return operatorExit(121)
				}
				line = append(line, part...)
				if e == bufio.ErrBufferFull {
					continue
				}
				if e != nil && e != io.EOF {
					return e
				}
				break
			}
			if len(line) == 0 {
				return nil
			}
			if !utf8.Valid(line) {
				return operatorExit(120)
			}
			b := make([]byte, 4+len(line))
			binary.LittleEndian.PutUint32(b, uint32(len(line)))
			copy(b[4:], line)
			if _, e := sink.Write(b); e != nil {
				return e
			}
		}
	}
	framed := s.Framed || s.Kind == "encode" || s.Kind == "filter" || s.Kind == "balance"
	if !framed {
		return copyBudget(sink, input, s.BufferBytes)
	}
	destination := 0
	for {
		b, e := readFrame(input, s.BufferBytes)
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		switch s.Kind {
		case "encode":
			b = b[4:]
		case "filter":
			if r.StreamItem == nil {
				return operatorExit(124)
			}
			selected := item(index, b[4:])
			if selected < 0 {
				return operatorExit(-selected)
			}
			branch := 1
			if selected != 0 {
				branch = 0
			}
			if _, e = writers[branch].Write(b); e != nil {
				return e
			}
			continue
		case "balance":
			if _, e = writers[destination].Write(b); e != nil {
				return e
			}
			destination = (destination + 1) % len(writers)
			continue
		}
		if _, e = sink.Write(b); e != nil {
			return e
		}
	}
}

func copyBudget(dst io.Writer, src io.Reader, budget int) error {
	if budget <= 0 {
		budget = 16 * 1024
	}
	_, e := io.CopyBuffer(struct{ io.Writer }{dst}, struct{ io.Reader }{src}, make([]byte, budget))
	return e
}

func operatorResult(e error, run *running) host.ProcessStageResult {
	result := host.ProcessStageResult{HasEffectiveStatus: true}
	if e == nil {
		return result
	}
	select {
	case <-run.stopped:
		result.Outcome = host.ProcessCancelled
		return result
	default:
	}
	var code operatorExit
	if errors.Is(e, syscall.EPIPE) {
		result.Status = 141
	} else if errors.As(e, &code) {
		result.Status = int(code)
	} else {
		result.Status = 1
		result.Outcome = host.ProcessFailed
	}
	result.EffectiveStatus = result.Status
	return result
}
