package main

import (
	"fmt"
	"io"
	"kame/core"
	"kame/host/posix"
	"kame/program"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Job control (052): every entry is a job, its Kash evaluation together with
// the & graphs it started. Jobs advance statement by statement, so several
// can run while the prompt is shown, and the engine's root suspension pauses
// one wherever it is.

type replJobState int

const (
	jobRunning replJobState = iota
	jobStopped
	jobDone
)

type replJob struct {
	id         int    // 0 until the entry becomes a numbered job
	text       string // listed text: the first line, ... when there are more
	next, end  int    // work items: the next to start, end exclusive
	index      int    // work item of handle
	handle     *program.Handle
	pending    []int64 // host nodes pending when handle started, for interrupts
	async      []int   // async graphs it started, as eval indices
	state      replJobState
	signal     int // stopping signal
	status     int
	stop       bool // the session must end (exit)
	evaluated  bool // every statement finished
	background bool
	notify     bool  // report the state before the next prompt
	startedAt  int64 // for --timeout, shifted past time spent stopped
	stoppedAt  int64
}

func replJobText(text string) string {
	first, _, more := strings.Cut(text, "\n")
	if more {
		first += " ..."
	}
	return first
}

// newJob makes work items [first, len(Work)) the next job.
func (s *replSession) newJob(text string, first int, background bool) *replJob {
	j := &replJob{text: replJobText(text), next: first, end: len(s.run.Session.Work), background: background, startedAt: time.Now().UnixNano()}
	s.jobs = append(s.jobs, j)
	if background && s.jobControl {
		s.number(j)
	}
	return j
}

// number gives j the next job number: one past the highest, or 1.
func (s *replSession) number(j *replJob) {
	if j.id != 0 {
		return
	}
	id := 1
	for _, other := range s.jobs {
		if other != j && other.id >= id {
			id = other.id + 1
		}
	}
	j.id = id
}

// roots are the engine roots holding j's live work.
func (s *replSession) roots(j *replJob) []*core.Root {
	var roots []*core.Root
	if j.handle != nil && j.handle.Root != nil {
		roots = append(roots, j.handle.Root)
	}
	eval := s.run.Session.Program.Eval
	for _, i := range j.async {
		if _, root := eval.AsyncProcess(i); root != nil {
			roots = append(roots, root)
		}
	}
	return roots
}

// asyncDone reports whether every & graph j started has finished.
func (s *replSession) asyncDone(j *replJob) bool {
	eval := s.run.Session.Program.Eval
	for _, i := range j.async {
		if _, root := eval.AsyncProcess(i); root != nil {
			if state := root.Node().State; state != core.NodeComplete && state != core.NodeFailed && state != core.NodeCancelled {
				return false
			}
		}
	}
	return true
}

// attributeAsync assigns new & graphs to the job whose running statement
// started them. Graphs of preloaded sources and the startup file belong to
// no job.
func (s *replSession) attributeAsync() {
	p := s.run.Session.Program
	for ; s.asyncSeen < p.Eval.AsyncProcessCount(); s.asyncSeen++ {
		launcher, _ := p.Eval.AsyncProcess(s.asyncSeen)
		node := p.Engine.LookupID(launcher)
		for _, j := range s.jobs {
			if j.handle != nil && j.handle.Root.Reaches(node) {
				j.async = append(j.async, s.asyncSeen)
				break
			}
		}
	}
}

// advance runs j's statements in order until one is still running.
func (s *replSession) advance(j *replJob) {
	run := s.run
	for j.state == jobRunning && !j.evaluated {
		if j.handle == nil {
			if j.next >= j.end {
				j.evaluated = true
				break
			}
			h, status, stop := run.beginWork(j.next)
			if h == nil {
				s.finish(j, status, stop)
				return
			}
			j.handle, j.index, j.pending = h, j.next, run.Session.Program.PendingNodes()
			j.next++
		}
		// & graphs are attributed while the statement that started them,
		// freed once it is done, still reaches them.
		s.attributeAsync()
		done, status, stop := run.pollWork(j.index, j.handle)
		if !done {
			return
		}
		j.handle = nil
		if stop || status != 0 {
			s.finish(j, status, stop)
			return
		}
	}
	if j.state == jobRunning && j.evaluated && s.asyncDone(j) {
		s.finish(j, j.status, false)
	}
}

func (s *replSession) advanceAll() {
	for _, j := range slices.Clone(s.jobs) {
		s.advance(j)
	}
}

// finish ends j. A numbered job's & graphs are observed by its report.
func (s *replSession) finish(j *replJob, status int, stop bool) {
	if j.handle != nil {
		j.handle.Free()
		j.handle = nil
	}
	j.state, j.status, j.stop = jobDone, status, stop
	if j.id != 0 {
		eval := s.run.Session.Program.Eval
		for _, i := range j.async {
			eval.ObserveAsyncProcess(i)
		}
		j.notify = j.background
	}
	if j.id == 0 {
		s.forget(j)
	}
}

func (s *replSession) forget(j *replJob) {
	s.jobs = slices.DeleteFunc(s.jobs, func(other *replJob) bool { return other == j })
}

// suspend stops a running job wherever it is.
func (s *replSession) suspend(j *replJob, signal int) {
	if j.state != jobRunning {
		return
	}
	j.state, j.signal, j.stoppedAt = jobStopped, signal, time.Now().UnixNano()
	s.run.Session.Program.SuspendRoots(s.roots(j))
	s.number(j)
	j.notify = j.background
}

// resume continues a stopped job, in the foreground or the background.
func (s *replSession) resume(j *replJob, foreground bool) {
	if j.state == jobStopped {
		j.state = jobRunning
		j.startedAt += time.Now().UnixNano() - j.stoppedAt
	}
	j.background = !foreground
	s.run.Session.Program.ResumeRoots(s.roots(j), foreground)
}

// cancel ends a job as an interrupt ends the foreground entry.
func (s *replSession) cancel(j *replJob) {
	eval := s.run.Session.Program.Eval
	for _, i := range j.async {
		eval.CancelAsyncProcess(i)
	}
	s.finish(j, 130, false)
	// Nothing may tick before the next entry: stop the processes now.
	s.run.Session.Program.DrainCancellations()
}

// handleStops suspends the jobs whose graphs the host reports stopped.
func (s *replSession) handleStops() {
	p := s.run.Session.Program
	for _, stop := range p.TakeHostStops() {
		if !stop.Stopped {
			continue
		}
		node := p.Engine.LookupID(stop.NodeID)
		for _, j := range s.jobs {
			if j.state == jobRunning && program.RootsReach(s.roots(j), node) {
				s.suspend(j, stop.Signal)
				break
			}
		}
	}
}

// tick services the program once and advances every job.
func (s *replSession) tick(wait int) {
	run, p := s.run, s.run.Session.Program
	tickCLIProgram(p, wait, run.Err, run.Inv.JSON)
	drainEvents(p, run.Out, run.Err, run.Inv.JSON, &run.Progress)
	s.attributeAsync()
	s.handleStops()
	s.advanceAll()
}

func (s *replSession) running() bool {
	return slices.ContainsFunc(s.jobs, func(j *replJob) bool { return j.state == jobRunning })
}

// backgroundNode reports nodes of running background jobs, whose graphs
// attach the terminal without taking its foreground.
func (s *replSession) backgroundNode(id int64) bool {
	node := s.run.Session.Program.Engine.LookupID(id)
	for _, j := range s.jobs {
		if j.background && program.RootsReach(s.roots(j), node) {
			return true
		}
	}
	return false
}

// takeStop reports whether the session itself received SIGTSTP (Ctrl-Z while
// Kame, not a child, holds the terminal).
func (s *replSession) takeStop() bool {
	for {
		select {
		case sig := <-s.stopSignals:
			if sig == syscall.SIGTSTP {
				return true
			}
		default:
			return false
		}
	}
}

// foreground runs j in the foreground until it finishes, stops, or leaves
// only & graphs running. It returns the session status and whether the
// session must end.
func (s *replSession) foreground(j *replJob) (int, bool) {
	run := s.run
	j.background = false
	posix.ResetSignals()
	s.takeStop()
	for {
		s.attributeAsync()
		s.advanceAll()
		if j.state != jobRunning {
			break
		}
		if j.evaluated {
			// Only & graphs remain: the prompt returns and they run on.
			s.number(j)
			j.background = true
			return j.status, false
		}
		if signal := posix.TakeSignal(); signal == int(syscall.SIGINT) {
			status, stop := run.interrupt(j.handle, j.pending, signal)
			j.handle = nil
			s.finish(j, status, stop)
			if stop {
				return status, true
			}
			s.forget(j)
			return status, false
		} else if signal != 0 {
			h := j.handle
			j.handle = nil
			return run.endForSignal(h, signal, false)
		}
		if s.jobControl && s.takeStop() {
			s.suspend(j, int(syscall.SIGTSTP))
			break
		}
		if run.Inv.TimeoutMS > 0 && (time.Now().UnixNano()-j.startedAt)/1000000 >= run.Inv.TimeoutMS {
			h := j.handle
			j.handle = nil
			return run.endForTimeout(h)
		}
		s.tick(10)
		if signal := nativeOutputSignal(); signal != 0 {
			h := j.handle
			j.handle = nil
			return run.endForSignal(h, signal, true)
		}
		if code := nativeOutputFailure(); code != "" && (j.handle == nil || j.handle.Node.Diagnostic.Code == "") {
			h := j.handle
			j.handle = nil
			return run.endForOutputFailure(h, code)
		}
	}
	if j.state == jobStopped {
		s.terminalLine()
		s.report(j, true)
		j.notify = false
		return 128 + int(syscall.SIGTSTP), false
	}
	s.forget(j)
	return j.status, j.stop
}

// terminalLine ends the line on which the terminal echoed ^Z.
func (s *replSession) terminalLine() {
	if s.jobControl {
		io.WriteString(s.run.Err, "\n")
	}
}

// stateText is a job's STATE as jobs lists it.
func (j *replJob) stateText() string {
	switch {
	case j.state == jobRunning:
		return "running"
	case j.state == jobStopped && j.signal == int(syscall.SIGTTIN):
		return "stopped (tty input)"
	case j.state == jobStopped && j.signal == int(syscall.SIGTTOU):
		return "stopped (tty output)"
	case j.state == jobStopped:
		return "stopped"
	case j.status == 0:
		return "done"
	case j.status > 128:
		return "signal " + unix.SignalName(syscall.Signal(j.status-128))
	}
	return "exit " + strconv.Itoa(j.status)
}

// report writes `[N]+ STATE  TEXT`; current marks the current job.
func (s *replSession) report(j *replJob, current bool) {
	mark := " "
	if current {
		mark = "+"
	}
	fmt.Fprintf(s.run.Err, "[%d]%s %s  %s\n", j.id, mark, j.stateText(), j.text)
}

// current is the most recently stopped job, otherwise the most recent one.
func (s *replSession) current() *replJob {
	var latest *replJob
	for _, j := range s.jobs {
		if j.id == 0 || j.state == jobDone {
			continue
		}
		if j.state == jobStopped && (latest == nil || latest.state != jobStopped || j.stoppedAt >= latest.stoppedAt) {
			latest = j
		} else if latest == nil || latest.state != jobStopped && j.id > latest.id {
			latest = j
		}
	}
	return latest
}

// notifyJobs reports background jobs that stopped or finished since the
// last prompt, forgetting the finished ones.
func (s *replSession) notifyJobs() {
	current := s.current()
	for _, j := range slices.Clone(s.jobs) {
		if !j.notify {
			continue
		}
		j.notify = false
		if s.jobControl {
			s.report(j, j == current)
		}
		if j.state == jobDone {
			s.forget(j)
		}
	}
}

func (s *replSession) stopped() []*replJob {
	var stopped []*replJob
	for _, j := range s.jobs {
		if j.state == jobStopped {
			stopped = append(stopped, j)
		}
	}
	return stopped
}

// end settles jobs when the session ends: stopped jobs are cancelled and
// running ones joined. A second interrupt while joining forces shutdown as
// for one entry.
func (s *replSession) end() (int, bool) {
	for _, j := range s.stopped() {
		s.cancel(j)
	}
	posix.ResetSignals()
	for s.running() {
		if signal := posix.TakeSignal(); signal != 0 {
			for _, j := range slices.Clone(s.jobs) {
				if j.state == jobRunning {
					s.cancel(j)
				}
			}
			if signal < 0 {
				return 128 - signal, true
			}
		}
		s.tick(10)
	}
	return 0, false
}

// jobCommand runs jobs, fg, bg, and kill %N on a terminal session. It
// reports false for other entries; kill without a %N operand runs the
// external command.
func (s *replSession) jobCommand(text string) (bool, int, bool) {
	if !s.jobControl || strings.ContainsRune(text, '\n') {
		return false, 0, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false, 0, false
	}
	errOut := s.run.Err
	switch fields[0] {
	case "jobs":
		if len(fields) != 1 {
			fmt.Fprintln(errOut, "jobs: expected no operand")
			return true, 1, false
		}
		current := s.current()
		for _, j := range slices.Clone(s.jobs) {
			if j.id == 0 {
				continue
			}
			fmt.Fprintf(s.run.Out, "[%d]%s %s  %s\n", j.id, map[bool]string{true: "+", false: " "}[j == current], j.stateText(), j.text)
			if j.state == jobDone {
				j.notify = false
				s.forget(j)
			}
		}
		return true, 0, false
	case "fg", "bg":
		j, ok := s.selectJob(fields[0], fields[1:])
		if !ok {
			return true, 1, false
		}
		if fields[0] == "bg" {
			if j.state != jobStopped {
				fmt.Fprintf(errOut, "bg: job %d is already running\n", j.id)
				return true, 1, false
			}
			s.resume(j, false)
			return true, 0, false
		}
		fmt.Fprintln(s.run.Err, j.text)
		s.resume(j, true)
		status, stop := s.foreground(j)
		return true, status, stop
	case "kill":
		return s.kill(fields[1:])
	}
	return false, 0, false
}

// selectJob resolves the optional %N operand of fg and bg.
func (s *replSession) selectJob(name string, operands []string) (*replJob, bool) {
	if len(operands) > 1 {
		fmt.Fprintf(s.run.Err, "%s: expected at most one job\n", name)
		return nil, false
	}
	if len(operands) == 0 {
		j := s.current()
		if j == nil {
			fmt.Fprintf(s.run.Err, "%s: no current job\n", name)
		}
		return j, j != nil
	}
	return s.lookupJob(name, operands[0])
}

func (s *replSession) lookupJob(name, operand string) (*replJob, bool) {
	number, err := strconv.Atoi(strings.TrimPrefix(operand, "%"))
	if !strings.HasPrefix(operand, "%") || err != nil {
		fmt.Fprintf(s.run.Err, "%s: expected %%N: %s\n", name, operand)
		return nil, false
	}
	for _, j := range s.jobs {
		if j.id == number && j.state != jobDone {
			return j, true
		}
	}
	fmt.Fprintf(s.run.Err, "%s: no such job: %s\n", name, operand)
	return nil, false
}

// kill handles `kill [-SIGNAL] %N...` when an operand names a job.
func (s *replSession) kill(operands []string) (bool, int, bool) {
	if !slices.ContainsFunc(operands, func(o string) bool { return strings.HasPrefix(o, "%") }) {
		return false, 0, false
	}
	signal := 0
	if len(operands) != 0 && strings.HasPrefix(operands[0], "-") {
		signal = parseSignal(operands[0][1:])
		if signal == 0 {
			fmt.Fprintf(s.run.Err, "kill: unknown signal: %s\n", operands[0])
			return true, 1, false
		}
		operands = operands[1:]
	}
	var jobs []*replJob
	for _, operand := range operands {
		j, ok := s.lookupJob("kill", operand)
		if !ok {
			return true, 1, false
		}
		jobs = append(jobs, j)
	}
	p := s.run.Session.Program
	for _, j := range jobs {
		switch signal {
		case 0:
			s.cancel(j)
		case int(syscall.SIGSTOP), int(syscall.SIGTSTP):
			s.suspend(j, signal)
		case int(syscall.SIGCONT):
			s.resume(j, false)
		default:
			p.SignalRoots(s.roots(j), signal)
			if j.state == jobStopped {
				s.resume(j, false)
			}
		}
	}
	return true, 0, false
}

// parseSignal reads a signal name, with or without SIG, or a number.
func parseSignal(text string) int {
	if n, err := strconv.Atoi(text); err == nil && n > 0 && n < 65 {
		return n
	}
	name := strings.ToUpper(text)
	if !strings.HasPrefix(name, "SIG") {
		name = "SIG" + name
	}
	return int(unix.SignalNum(name))
}

// replInput is one read from the input goroutine.
type replInput struct {
	entry replEntry
	err   error
}

// read waits for the next entry, running background jobs meanwhile. The
// editor runs on its own goroutine and reaches session state only through
// contextRequests, so the program stays owned by this one.
func (s *replSession) read(reader lineReader, prompt string) (replEntry, error) {
	result := make(chan replInput, 1)
	for _, o := range s.outputs {
		o.editing = true
		defer o.stopEditing()
	}
	go func() {
		entry, err := readEntry(reader, prompt)
		result <- replInput{entry, err}
	}()
	for {
		select {
		case r := <-result:
			return r.entry, r.err
		case reply := <-s.contextRequests:
			reply <- s.completion()
			continue
		default:
		}
		if s.running() {
			s.tick(10)
			continue
		}
		select {
		case r := <-result:
			return r.entry, r.err
		case reply := <-s.contextRequests:
			reply <- s.completion()
		}
	}
}

// editorOutput writes background job output above the entry being edited.
// ponytail: readline's Printf is not synchronized with its own goroutine;
// a keystroke rendered at the same instant can garble one redraw.
type editorOutput struct {
	w     io.Writer
	shell interface {
		Printf(string, ...any) (int, error)
	}
	editing bool
	partial []byte
}

func (o *editorOutput) Write(data []byte) (int, error) {
	if !o.editing {
		return o.w.Write(data)
	}
	o.partial = append(o.partial, data...)
	if i := strings.LastIndexByte(string(o.partial), '\n'); i >= 0 {
		o.shell.Printf("%s", strings.ReplaceAll(string(o.partial[:i]), "\n", "\r\n"))
		o.partial = o.partial[i+1:]
	}
	return len(data), nil
}

func (o *editorOutput) stopEditing() {
	o.editing = false
	if len(o.partial) != 0 {
		o.w.Write(o.partial)
		o.partial = nil
	}
}
