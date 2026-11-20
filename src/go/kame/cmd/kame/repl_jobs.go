package main

import (
	"cmp"
	"fmt"
	"io"
	"kame/core"
	"kame/host/posix"
	"kame/lang/eval"
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
	first      int    // first queue slot owned by this entry
	index      int    // work item of handle
	handle     *program.Handle
	pending    []int64 // host nodes pending when handle started, for interrupts
	async      []*eval.ProcessHandle
	state      replJobState
	signal     int // stopping signal
	status     int
	cancelled  bool // ended by kill %N or session cancellation
	seq        int  // start order, for the current job
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
	s.started++
	j := &replJob{text: replJobText(text), first: first, next: first, end: len(s.run.Session.Work), background: background, startedAt: time.Now().UnixNano(), seq: s.started}
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
	for _, process := range j.async {
		if root := process.Root(); root != nil {
			roots = append(roots, root)
		}
	}
	return roots
}

// asyncDone reports whether every & graph j started has finished.
func (s *replSession) asyncDone(j *replJob) bool {
	for _, process := range j.async {
		if state := process.State(); state != core.NodeComplete && state != core.NodeFailed && state != core.NodeCancelled {
			return false
		}
	}
	return true
}

// attributeAsync assigns new & graphs to the job whose running statement
// started them. Graphs of preloaded sources and the startup file belong to
// no job.
func (s *replSession) attributeAsync(launcher int64, process *eval.ProcessHandle) {
	p := s.run.Session.Program
	node := p.Engine.LookupID(launcher)
	for _, j := range s.jobs {
		for _, root := range s.roots(j) {
			if root.Reaches(node) {
				j.async = append(j.async, process)
				return
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
		s.finish(j, s.asyncStatus(j), false)
	}
}

// asyncStatus is the status of an evaluated job: that of its last failed &
// graph, as for a statement, otherwise its own.
func (s *replSession) asyncStatus(j *replJob) int {
	status := j.status
	for _, process := range j.async {
		if !process.Observed() && process.State() == core.NodeFailed {
			status = processExitStatus(process.Diagnostic(), s.run.Inv.Name == "run")
		}
	}
	return status
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
	s.run.Session.DiscardWork(j.first, j.end)
	j.state, j.status, j.stop = jobDone, status, stop
	if j.id != 0 {
		for _, process := range j.async {
			s.run.Session.Program.Eval.ObserveProcess(process)
		}
		j.notify = j.background
	}
	s.run.Session.Program.CollectProcesses()
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
	for _, process := range j.async {
		eval.CancelProcess(process)
	}
	j.cancelled = true
	s.finish(j, 130, false)
	// Nothing may tick before the next entry: stop the processes now.
	s.run.Session.Program.DrainCancellations()
}

func (s *replSession) cancelJobs() {
	for _, j := range slices.Clone(s.jobs) {
		if j.state != jobDone {
			s.cancel(j)
		}
	}
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

// foreground runs j in the foreground until it finishes or stops. A typed
// entry also returns once only its & graphs run, which run on as a
// background job; a job brought back with fg is waited for to the end. It
// returns the session status and whether the session must end.
func (s *replSession) foreground(j *replJob, resumed bool) (int, bool) {
	run := s.run
	j.background = false
	posix.ResetSignals()
	s.takeStop()
	for {
		// A host timeout can finish the job during tick; still end the invocation.
		if run.Inv.TimeoutMS > 0 && (time.Now().UnixNano()-j.startedAt)/1000000 >= run.Inv.TimeoutMS {
			h := j.handle
			j.handle = nil
			s.cancelJobs()
			return run.endForTimeout(h)
		}
		s.advanceAll()
		if j.state != jobRunning {
			break
		}
		if j.evaluated && !resumed {
			// Only & graphs remain: the prompt returns and they run on.
			s.number(j)
			j.background = true
			return j.status, false
		}
		if signal := posix.TakeSignal(); signal == int(syscall.SIGINT) && j.handle == nil {
			// Only & graphs remain: cancel them as kill %N would.
			posix.ResetSignals()
			s.cancel(j)
			s.terminalLine()
			s.forget(j)
			return 128 + signal, false
		} else if signal == int(syscall.SIGINT) {
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
			s.cancelJobs()
			return run.endForSignal(h, signal, false)
		}
		if s.jobControl && s.takeStop() {
			s.suspend(j, int(syscall.SIGTSTP))
			break
		}
		s.tick(10)
		if signal := nativeOutputSignal(); signal != 0 {
			h := j.handle
			j.handle = nil
			s.cancelJobs()
			return run.endForSignal(h, signal, true)
		}
		if code := nativeOutputFailure(); code != "" && (j.handle == nil || j.handle.Node.Diagnostic.Code == "") {
			h := j.handle
			j.handle = nil
			s.cancelJobs()
			return run.endForOutputFailure(h, code)
		}
	}
	if j.state == jobStopped {
		s.terminalLine()
		s.report(j)
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
	case j.cancelled:
		return "killed"
	case j.status == 0:
		return "done"
	case j.status > 128:
		return "signal " + unix.SignalName(syscall.Signal(j.status-128))
	}
	return "exit " + strconv.Itoa(j.status)
}

// line is `[N]M STATE  TEXT`, M marking the current (+) and previous (-) job.
func (s *replSession) line(j *replJob) string {
	mark, ranked := " ", s.ranked()
	if len(ranked) > 0 && ranked[0] == j {
		mark = "+"
	} else if len(ranked) > 1 && ranked[1] == j {
		mark = "-"
	}
	return fmt.Sprintf("[%d]%s %s  %s\n", j.id, mark, j.stateText(), j.text)
}

func (s *replSession) report(j *replJob) { io.WriteString(s.run.Err, s.line(j)) }

// ranked lists live numbered jobs, current first: stopped jobs from the most
// recently stopped, then running ones from the most recently started.
func (s *replSession) ranked() []*replJob {
	var ranked []*replJob
	for _, j := range s.jobs {
		if j.id != 0 && j.state != jobDone {
			ranked = append(ranked, j)
		}
	}
	slices.SortStableFunc(ranked, func(a, b *replJob) int {
		if (a.state == jobStopped) != (b.state == jobStopped) {
			if a.state == jobStopped {
				return -1
			}
			return 1
		}
		if a.state == jobStopped && a.stoppedAt != b.stoppedAt {
			return cmp.Compare(b.stoppedAt, a.stoppedAt)
		}
		return cmp.Compare(b.seq, a.seq)
	})
	return ranked
}

// current is the current job (+), nil without one.
func (s *replSession) current() *replJob {
	if ranked := s.ranked(); len(ranked) > 0 {
		return ranked[0]
	}
	return nil
}

// notifyJobs reports background jobs that stopped or finished since the
// last prompt, forgetting the finished ones.
func (s *replSession) notifyJobs() {
	for _, j := range slices.Clone(s.jobs) {
		if !j.notify {
			continue
		}
		j.notify = false
		if s.jobControl {
			s.report(j)
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

// jobCommand handles terminal job selectors and lets ordinary handle
// operands reach the evaluator.
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
		for _, j := range slices.Clone(s.jobs) {
			if j.id == 0 {
				continue
			}
			io.WriteString(s.run.Out, s.line(j))
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
		status, stop := s.foreground(j, true)
		return true, status, stop
	case "kill":
		return s.kill(fields[1:])
	case "wait":
		if len(fields) == 1 || strings.HasPrefix(fields[1], "%") {
			return s.wait(fields[1:])
		}
	}
	return false, 0, false
}

// wait waits until each operand job has finished or stopped, or without
// operands until no job runs. A finished job's status is observed by wait,
// which forgets it without a report.
func (s *replSession) wait(operands []string) (bool, int, bool) {
	var jobs []*replJob
	for _, operand := range operands {
		j, ok := s.lookupJob("wait", operand)
		if !ok {
			return true, 1, false
		}
		jobs = append(jobs, j)
	}
	if len(operands) == 0 {
		for _, j := range s.jobs {
			if j.state == jobRunning {
				jobs = append(jobs, j)
			}
		}
	}
	posix.ResetSignals()
	s.takeStop()
	for slices.ContainsFunc(jobs, func(j *replJob) bool { return j.state == jobRunning }) {
		if signal := posix.TakeSignal(); signal == int(syscall.SIGINT) {
			// An interrupt ends the wait, not the jobs.
			posix.ResetSignals()
			s.terminalLine()
			return true, 128 + signal, false
		} else if signal != 0 {
			s.cancelJobs()
			status, stop := s.run.endForSignal(nil, signal, false)
			return true, status, stop
		}
		if s.takeStop() {
			s.terminalLine()
			return true, 128 + int(syscall.SIGTSTP), false
		}
		s.tick(10)
	}
	status := 0
	for _, j := range jobs {
		if j.state == jobDone {
			status = j.status
			j.notify = false
			s.forget(j)
		} else {
			status = 128 + j.signal
		}
	}
	if len(operands) == 0 {
		status = 0
	}
	return true, status, false
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

// lookupJob resolves %N, the current job %+ (or %%), and the previous job %-.
func (s *replSession) lookupJob(name, operand string) (*replJob, bool) {
	if operand == "%+" || operand == "%%" || operand == "%-" {
		ranked, i := s.ranked(), 0
		if operand == "%-" {
			i = 1
		}
		if i < len(ranked) {
			return ranked[i], true
		}
		which := "current"
		if i == 1 {
			which = "previous"
		}
		fmt.Fprintf(s.run.Err, "%s: no %s job\n", name, which)
		return nil, false
	}
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

type replSignalError int

func (e replSignalError) Error() string { return "shell interrupted by signal" }

// read waits for the next entry, running background jobs meanwhile. The
// editor runs on its own goroutine and reaches session state only through
// contextRequests, so the program stays owned by this one.
func (s *replSession) read(reader lineReader, prompt string) (replEntry, error) {
	result := make(chan replInput, 1)
	signals := time.NewTicker(25 * time.Millisecond)
	defer signals.Stop()
	for _, o := range s.outputs {
		o.editing = true
		defer o.stopEditing()
	}
	// Highlighting runs per keystroke: hand it the session state now rather
	// than per key through contextRequests, which waits out job ticks.
	// Tab completion still asks for a fresh context.
	if editor, ok := reader.(*editorReader); ok {
		editor.snapshot = s.completion()
	}
	go func() {
		entry, err := readEntry(reader, prompt)
		result <- replInput{entry, err}
	}()
	for {
		if signal := posix.TakeSignal(); signal != 0 {
			if signal == int(syscall.SIGINT) {
				posix.ResetSignals()
			} else {
				return replEntry{}, replSignalError(signal)
			}
		}
		select {
		case r := <-result:
			return r.entry, r.err
		case reply := <-s.contextRequests:
			reply <- s.completion()
			continue
		default:
		}
		if s.running() || len(s.run.Session.Program.Pending) != 0 {
			s.tick(10)
			continue
		}
		select {
		case r := <-result:
			return r.entry, r.err
		case reply := <-s.contextRequests:
			reply <- s.completion()
		case <-signals.C:
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
