package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/program"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	replContinuation = "   .. "
)

var errInterrupted = errors.New("interrupted")

// lineReader yields one physical line without its terminator. Interrupt
// returns errInterrupted; end of input returns io.EOF.
type lineReader interface {
	ReadLine(prompt string) (string, error)
	Close()
}

// runInteractive loads the login profile when requested, runs preloaded work,
// and, for interactive sessions, loads initialization and reads entries.
// Entry failures do not end interactive sessions; the caller finalizes both modes.
func runInteractive(run *sessionRun, in io.Reader) int {
	session := run.Session
	preloaded := len(session.Work)
	run.Interactive = true
	defer run.Failure.Free(mem.System)
	// Progress rows and taskbar state would draw over foreground programs and
	// the prompt; commands report through their own output instead.
	dashboardLive = false
	emitTaskbar(run.Err, 0, 0)
	taskbarEnabled = false
	if !run.Inv.NoDefaultGrants && !run.Inv.ExplicitGrantsOnly {
		session.FollowDirectoryRead = !hasGrant(run.Inv.Grants, eval.Read)
		session.FollowDirectoryWrite = !hasGrant(run.Inv.Grants, eval.Write)
	}
	repl := &replSession{run: run, shell: replShell{Session: session}, contextRequests: make(chan chan completionContext)}
	session.Program.Eval.CommandAlias, session.Program.Eval.CommandAliasState = replCommandAlias, &repl.shell
	session.Program.Eval.SessionTool, session.Program.Eval.SessionToolState = replSessionTool, &repl.shell
	session.Program.BackgroundNode = repl.backgroundNode
	session.Program.Eval.ProcessStarted = repl.attributeAsync
	var reader lineReader
	var terminal *editorReader
	if run.Inv.Interactive {
		reader = newLineReader(in)
		defer reader.Close()
		terminal, _ = reader.(*editorReader)
	}
	repl.options = loadReplOptions(run.Err)
	if terminal != nil {
		// Foreground commands receive the terminal, and entries are jobs.
		session.Program.Eval.Terminal = true
		run.QuietExits = true
		repl.jobControl = true
		repl.stopSignals = posix.CatchJobControlSignals()
		terminal.bind(repl)
		repl.history = terminal.history
		repl.history.options = repl.options
		repl.history.applyOptions()
		for _, w := range []*io.Writer{&run.Out, &run.Err} {
			o := &editorOutput{w: *w, shell: terminal.shell}
			repl.outputs = append(repl.outputs, o)
			*w = o
		}
	}
	if run.Inv.Login {
		path, explicit := loginPath()
		if status, stop := repl.loadStartupFile(path, explicit); stop {
			repl.end()
			return status
		}
	}
	run.Interactive = run.Inv.Interactive
	for i := 0; i < preloaded; i++ {
		if status, stop := run.step(i); stop {
			repl.end()
			return status
		}
	}
	if !run.Inv.Interactive {
		if status, stop := repl.end(); stop {
			return status
		}
		if run.joinNeeded() {
			if status, stop := run.step(len(session.Work)); stop {
				return status
			}
		}
		if run.HasLast && !run.Inv.JSON && !run.Inv.DryRun {
			writeValue(run.Out, run.Last)
		}
		return 0
	}
	status, stop := repl.loadStartup()
	if stop {
		repl.end()
		return status
	}
	entries := 0
	for {
		repl.notifyJobs()
		prompt := ""
		if terminal != nil {
			terminal.history.refresh(repl.completion().Directory)
			if prompt, stop = repl.prompt(status); stop {
				return status
			}
			if terminal.right, stop = repl.rightPrompt(status); stop {
				return status
			}
		}
		entry, err := repl.read(reader, prompt)
		if signal, ok := err.(replSignalError); ok {
			repl.cancelJobs()
			status, _ := run.endForSignal(nil, int(signal), false)
			repl.end()
			return status
		}
		if errors.Is(err, errInterrupted) {
			status = 130
			continue
		}
		if err != nil {
			if repl.warnStopped() {
				status = 1
				continue
			}
			break
		}
		if replExitEntry(entry.Text) && repl.warnStopped() {
			status = 1
			continue
		}
		repl.warned = false
		// Entries, builtins included, are numbered in typed order.
		entries++
		name := "<kash:" + strconv.Itoa(entries) + ">"
		if status, stop = repl.runEntry(name, entry.Text, entry.Text, entry.Background); stop {
			repl.end()
			return status
		}
	}
	if joined, stop := repl.end(); stop {
		return joined
	}
	if run.joinNeeded() {
		if joined, stop := run.step(len(session.Work)); stop || joined != 0 {
			return joined
		}
	}
	return status
}

// replSession runs entries of one interactive session.
type replSession struct {
	run   *sessionRun
	shell replShell
	// Job control (repl_jobs.go).
	jobs            []*replJob
	started         int  // jobs started so far, for their start order
	jobControl      bool // a terminal session
	stopSignals     <-chan os.Signal
	warned          bool // stopped jobs were reported at the last end attempt
	outputs         []*editorOutput
	contextRequests chan chan completionContext
	history         *replHistory
	options         *replOptions
}

// warnStopped reports stopped jobs at the first attempt to end the session.
func (s *replSession) warnStopped() bool {
	stopped := s.stopped()
	if len(stopped) == 0 || s.warned {
		return false
	}
	s.warned = true
	fmt.Fprintln(s.run.Err, "kash: there are stopped jobs")
	for _, j := range stopped {
		s.report(j)
	}
	return true
}

// replExitEntry reports a whole-line exit statement.
func replExitEntry(text string) bool {
	fields := strings.Fields(text)
	return !strings.ContainsRune(text, '\n') && len(fields) != 0 && len(fields) <= 2 && fields[0] == "exit"
}

// runEntry runs one entry: a session command, or Kash appended to the
// session. parsed is the text given to the parser; it equals text, except for
// startup entries, which keep their file offsets through blank padding.
func (s *replSession) runEntry(name, text, parsed string, background bool) (int, bool) {
	session := s.run.Session
	fields := strings.Fields(text)
	if len(fields) != 0 && !session.Program.Eval.HasLexicalBinding(fields[0]) {
		if handled, status, stop := s.sessionCommand(name, text, parsed, fields); handled {
			return status, stop
		}
	}
	first, diagnostics := session.AppendKash(name, parsed)
	if s.reportDiagnostics(diagnostics) {
		return 1, false
	}
	job := s.newJob(text, first, background)
	if background {
		return 0, false
	}
	return s.foreground(job, false)
}

func (s *replSession) sessionCommand(name, text, parsed string, fields []string) (bool, int, bool) {
	run := s.run
	if s.options != nil {
		if handled, status := s.options.command(text, run.Out, run.Err); handled {
			if status == 0 && s.history != nil {
				s.history.applyOptions()
			}
			return true, status, false
		}
	}
	if s.history != nil {
		if handled, status := s.history.command(text, run.Out, run.Err); handled {
			return true, status, false
		}
	}
	if handled, status, stop := s.jobCommand(text); handled {
		return true, status, stop
	}
	if fields[0] == "explain" && !strings.ContainsRune(text, '\n') {
		return true, s.explain(len(fields)), false
	}
	if fields[0] == "alias" || fields[0] == "unalias" {
		status, stop := s.alias(fields[0], name, text, parsed)
		return true, status, stop
	}
	return false, 0, false
}

// explain renders the latest failure in full, including one a terminal
// session left unreported because a process exited unsuccessfully.
func (s *replSession) explain(fields int) int {
	run := s.run
	if fields != 1 {
		fmt.Fprintln(run.Err, "explain: expected no operand")
		return 2
	}
	if run.Failure.Code == "" {
		fmt.Fprintln(run.Err, "explain: no failure to explain")
		return 1
	}
	emitRunDiagnostic(run.Err, run.Failure, false, run.Fragments, run.Loaded)
	return 0
}

func (s *replSession) reportDiagnostics(diagnostics []diagnostic.Diagnostic) bool {
	run := s.run
	for i := range diagnostics {
		run.emit(diagnosticWriter(run.Out, run.Err, run.Inv.JSON), diagnostics[i], run.Inv.JSON)
		diagnostics[i].Free(mem.System)
	}
	return len(diagnostics) != 0
}

// startupPath selects the startup file and whether it was named explicitly.
func startupPath() (string, bool) {
	if path, ok := os.LookupEnv("KASH_INIT"); ok {
		return path, true
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "kash", "init.kash"), false
}

func loginPath() (string, bool) {
	if path, ok := os.LookupEnv("KASH_PROFILE"); ok {
		return path, true
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "kash", "profile.kash"), false
}

// loadStartup runs the startup file as entries. Each entry is parsed with
// the preceding file text blanked out, so diagnostics locate file positions
// against the whole file registered as one fragment.
func (s *replSession) loadStartup() (int, bool) {
	path, explicit := startupPath()
	return s.loadStartupFile(path, explicit)
}

func (s *replSession) loadStartupFile(path string, explicit bool) (int, bool) {
	if path == "" {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if explicit || !errors.Is(err, fs.ErrNotExist) {
			sourceError(diagnosticWriter(s.run.Out, s.run.Err, s.run.Inv.JSON), "HOST_FAIL", "cannot read startup file: "+err.Error(), s.run.Inv.JSON)
			return 1, false
		}
		return 0, false
	}
	text := string(data)
	s.run.Fragments = append(s.run.Fragments, program.Fragment{Name: path, Text: text, Lang: "kash"})
	reader := &pipeReader{reader: bufio.NewReader(strings.NewReader(text))}
	status := 0
	for {
		read, err := readEntry(reader, "")
		if err != nil {
			return status, false
		}
		entry, offset := read.Text, read.Offset
		padding := []byte(text[:offset])
		for i := range padding {
			if padding[i] != '\n' {
				padding[i] = ' '
			}
		}
		var stop bool
		if status, stop = s.runEntry(path, entry, string(padding)+entry, read.Background); stop {
			return status, true
		}
	}
}

// prompt returns the first-line prompt: the result of the session's
// kash-prompt function when it yields a string, otherwise the default.
func (s *replSession) prompt(status int) (string, bool) {
	return s.callPrompt("kash-prompt", status, s.shell.prompt())
}

// rightPrompt returns the right prompt: the result of the session's
// kash-rprompt function when it yields a string, otherwise none.
func (s *replSession) rightPrompt(status int) (string, bool) {
	return s.callPrompt("kash-rprompt", status, "")
}

// callPrompt calls the prompt function function with the prompt record,
// returning its string result, or fallback when it is undefined, fails, or
// yields another value.
func (s *replSession) callPrompt(function string, status int, fallback string) (string, bool) {
	run, session := s.run, s.run.Session
	if !session.Program.Eval.HasFunction(function) {
		return fallback, false
	}
	name := "<" + function + ">"
	jobs := 0
	for _, j := range s.jobs {
		if j.id != 0 && j.state != jobDone {
			jobs++
		}
	}
	text := "(" + function + " {status: " + strconv.Itoa(status) + ", dir: " + kameString(session.Directory()) + ", name: " + kameString(s.shell.directoryName()) + ", jobs: " + strconv.Itoa(jobs) + "})"
	first, diagnostics := session.AppendKash(name, text)
	if s.reportDiagnostics(diagnostics) {
		return fallback, false
	}
	defer session.DiscardWork(first, len(session.Work))
	var value core.Value
	run.Capture = &value
	defer func() {
		run.Capture = nil
		value.Free(mem.System)
	}()
	run.StartedAt = time.Now().UnixNano()
	for i := first; i < len(session.Work); i++ {
		session.Work[i].Display = false
		if result, stop := run.step(i); stop || result != 0 {
			return fallback, stop
		}
	}
	if value.Kind != core.String {
		return fallback, false
	}
	return value.Text, false
}

// kameString quotes text as a Kame expression string literal.
func kameString(text string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"', '\\', '@', '{':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (s *replSession) completion() completionContext {
	p := s.run.Session.Program
	home, _ := s.shell.lookup("HOME")
	path, _ := s.shell.lookup("PATH")
	aliases := make(map[string][]string, len(s.shell.aliases))
	for name, prefix := range s.shell.aliases {
		words := make([]string, len(prefix))
		for i, arg := range prefix {
			words[i] = arg.Text
		}
		aliases[name] = words
	}
	return completionContext{Directory: s.run.Session.Directory(), Home: home, Path: path, Names: p.Eval.InteractiveNames(), Aliases: aliases}
}

// replEntry is one entry read from the input: Kash text, the byte offset of
// its first line for pipe input, and whether a final & line submitted it as
// a background job.
type replEntry struct {
	Text       string
	Offset     int
	Background bool
}

// replBackgroundLine reports the line that submits an entry in the background.
func replBackgroundLine(line string) bool { return strings.TrimSpace(line) == "&" }

// readEntry assembles one entry. Text ending inside an expression continues
// until it closes; a control header continues until a blank line, as the
// body's extent cannot be known before then. A line holding only & ends the
// entry at once and marks it background.
func readEntry(reader lineReader, prompt string) (replEntry, error) {
	var lines []string
	block := false
	offset := 0
	for {
		if len(lines) != 0 {
			prompt = replContinuation
		}
		line, err := reader.ReadLine(prompt)
		if errors.Is(err, io.EOF) && len(lines) != 0 {
			return replEntry{Text: strings.Join(lines, "\n"), Offset: offset}, nil
		}
		if err != nil {
			return replEntry{}, err
		}
		// The editor returns a whole entry, whose last line may be the marker.
		if head, last, multi := cutLastLine(line); multi && replBackgroundLine(last) {
			if strings.TrimSpace(head) == "" {
				continue
			}
			return replEntry{Text: head, Background: true}, nil
		}
		if replBackgroundLine(line) {
			if len(lines) == 0 {
				continue
			}
			return replEntry{Text: strings.Join(lines, "\n"), Offset: offset, Background: true}, nil
		}
		blank := strings.TrimSpace(line) == ""
		if blank && len(lines) == 0 {
			continue
		}
		if blank && block {
			return replEntry{Text: strings.Join(lines, "\n"), Offset: offset}, nil
		}
		if pipe, ok := reader.(*pipeReader); ok && len(lines) == 0 {
			offset = pipe.line
		}
		lines = append(lines, line)
		text := strings.Join(lines, "\n")
		switch script.ClassifyKashInput(text) {
		case script.KashBlock:
			block = true
		case script.KashComplete:
			if !block {
				return replEntry{Text: text, Offset: offset}, nil
			}
		}
	}
}

// cutLastLine splits text before its last line, reporting whether it has
// more than one.
func cutLastLine(text string) (string, string, bool) {
	i := strings.LastIndexByte(text, '\n')
	if i < 0 {
		return "", text, false
	}
	return text[:i], text[i+1:], true
}

// pipeReader reads entries from non-terminal input without prompts. line
// is the byte offset of the last line read, consumed the offset of the next.
type pipeReader struct {
	reader         *bufio.Reader
	line, consumed int
}

func (r *pipeReader) ReadLine(string) (string, error) {
	line, err := r.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", io.EOF
	}
	r.line, r.consumed = r.consumed, r.consumed+len(line)
	return strings.TrimRight(line, "\r\n"), nil
}

func (r *pipeReader) Close() {}
