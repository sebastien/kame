package main

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/script"
	"kame/program"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	replPrompt        = "kash> "
	replContinuation  = "   .. "
	replHistoryLength = 1000
)

var errInterrupted = errors.New("interrupted")

// lineReader yields one physical line without its terminator. Interrupt
// returns errInterrupted; end of input returns io.EOF.
type lineReader interface {
	ReadLine(prompt string) (string, error)
	Close()
}

// runInteractive runs preloaded work and the startup file, then reads,
// appends, and runs Kash entries until end of input or exit. Failures end the
// entry, not the session. The caller joins outstanding work and runs
// finalization as for batch runs.
func runInteractive(run *sessionRun, in io.Reader) int {
	session := run.Session
	run.Interactive = true
	// Progress rows and taskbar state would draw over foreground programs and
	// the prompt; commands report through their own output instead.
	dashboardLive = false
	emitTaskbar(run.Err, 0, 0)
	taskbarEnabled = false
	for i := 0; i < len(session.Work); i++ {
		if status, stop := run.step(i); stop {
			return status
		}
	}
	repl := &replSession{run: run, shell: replShell{Program: session.Program, FollowGrants: len(run.Inv.Grants) == 0 && !run.Inv.NoDefaultGrants}}
	reader := newLineReader(in)
	defer reader.Close()
	terminal, _ := reader.(*terminalReader)
	if terminal != nil {
		// Foreground commands receive the terminal; Kame waits like a shell
		// without job control.
		session.Program.Eval.Terminal = true
		posix.IgnoreJobControlSignals()
		terminal.complete = func(line string, pos int) (string, int, []string) {
			return completeLine(line, pos, repl.completion())
		}
	}
	status, stop := repl.loadStartup()
	if stop {
		return status
	}
	entries := 0
	for {
		prompt := ""
		if terminal != nil {
			if prompt, stop = repl.prompt(status); stop {
				return status
			}
		}
		text, _, err := readEntry(reader, prompt)
		if errors.Is(err, errInterrupted) {
			status = 130
			continue
		}
		if err != nil {
			break
		}
		// Entries, builtins included, are numbered in typed order.
		entries++
		name := "<kash:" + strconv.Itoa(entries) + ">"
		if status, stop = repl.runEntry(name, text, text, true); stop {
			return status
		}
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
	run            *sessionRun
	shell          replShell
	promptFragment int // 1 + index of the <kash-prompt> fragment; 0 before the first call
}

// runEntry runs one entry: a session command, or Kash appended to the
// session. parsed is the text given to the parser; it equals text, except for
// startup entries, which keep their file offsets through blank padding.
// fragment registers text as the diagnostic source named name.
func (s *replSession) runEntry(name, text, parsed string, fragment bool) (int, bool) {
	run, session := s.run, s.run.Session
	if handled, status := s.shell.run(text, run.Err); handled {
		return status, false
	}
	if fragment {
		run.Fragments = append(run.Fragments, program.Fragment{Name: name, Text: text, Lang: "kash"})
	}
	first, diagnostics := session.AppendKash(name, parsed)
	if s.reportDiagnostics(diagnostics) {
		return 1, false
	}
	posix.ResetSignals()
	run.StartedAt = time.Now().UnixNano() // --timeout bounds each entry.
	for i := first; i < len(session.Work); i++ {
		status, stop := run.step(i)
		if stop || status != 0 {
			return status, stop
		}
	}
	return 0, false
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

// loadStartup runs the startup file as entries. Each entry is parsed with
// the preceding file text blanked out, so diagnostics locate file positions
// against the whole file registered as one fragment.
func (s *replSession) loadStartup() (int, bool) {
	path, explicit := startupPath()
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
		entry, offset, err := readEntry(reader, "")
		if err != nil {
			return status, false
		}
		padding := []byte(text[:offset])
		for i := range padding {
			if padding[i] != '\n' {
				padding[i] = ' '
			}
		}
		var stop bool
		if status, stop = s.runEntry(path, entry, string(padding)+entry, false); stop {
			return status, true
		}
	}
}

// prompt returns the first-line prompt: the result of the session's
// kash-prompt function when it yields a string, otherwise the default.
func (s *replSession) prompt(status int) (string, bool) {
	fallback := s.shell.prompt()
	run, session := s.run, s.run.Session
	if !session.Program.Eval.HasFunction("kash-prompt") {
		return fallback, false
	}
	const name = "<kash-prompt>"
	text := "(kash-prompt [status: " + strconv.Itoa(status) + " dir: " + kameString(session.Program.Options.Directory) + " name: " + kameString(s.shell.directoryName()) + "])"
	if s.promptFragment == 0 {
		run.Fragments = append(run.Fragments, program.Fragment{Name: name, Lang: "kash"})
		s.promptFragment = len(run.Fragments)
	}
	run.Fragments[s.promptFragment-1].Text = text
	first, diagnostics := session.AppendKash(name, text)
	if s.reportDiagnostics(diagnostics) {
		return fallback, false
	}
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
	callables, values := p.Eval.InteractiveNames()
	home, _ := s.shell.lookup("HOME")
	path, _ := s.shell.lookup("PATH")
	return completionContext{Directory: p.Options.Directory, Home: home, Path: path, Callables: callables, Values: values}
}

// readEntry assembles one entry. Text ending inside an expression continues
// until it closes; a control header continues until a blank line, as the
// body's extent cannot be known before then. For pipe input it also returns
// the byte offset of the entry's first line.
func readEntry(reader lineReader, prompt string) (string, int, error) {
	var lines []string
	block := false
	offset := 0
	for {
		if len(lines) != 0 {
			prompt = replContinuation
		}
		line, err := reader.ReadLine(prompt)
		if errors.Is(err, io.EOF) && len(lines) != 0 {
			return strings.Join(lines, "\n"), offset, nil
		}
		if err != nil {
			return "", 0, err
		}
		blank := strings.TrimSpace(line) == ""
		if blank && len(lines) == 0 {
			continue
		}
		if blank && block {
			return strings.Join(lines, "\n"), offset, nil
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
				return text, offset, nil
			}
		}
	}
}

func newLineReader(in io.Reader) lineReader {
	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		output := os.Stderr
		if !term.IsTerminal(int(output.Fd())) {
			output = os.Stdout
		}
		r := &terminalReader{fd: int(file.Fd()), input: &interruptReader{reader: file}, output: output, history: loadReplHistory()}
		r.reset()
		return r
	}
	return &pipeReader{reader: bufio.NewReader(in)}
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

// terminalReader edits lines in raw mode only while reading, so commands run
// with the terminal's normal settings and signal keys.
type terminalReader struct {
	fd       int
	input    *interruptReader
	output   *os.File
	history  *replHistory
	term     *term.Terminal
	width    int
	complete func(line string, pos int) (string, int, []string)
}

// reset replaces the line editor. x/term keeps the bytes of a Ctrl-C or
// Ctrl-D that ended ReadLine buffered and would replay them on every later
// call, so an interrupted editor is discarded with its partial line.
func (r *terminalReader) reset() {
	r.term = term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{r.input, r.output}, replPrompt)
	r.term.History = r.history
	r.term.AutoCompleteCallback = r.autoComplete
}

// autoComplete handles Tab; other keys keep their editing behavior. Listed
// candidates are written above a redrawn line.
func (r *terminalReader) autoComplete(line string, pos int, key rune) (string, int, bool) {
	if key != '\t' || r.complete == nil {
		return "", 0, false
	}
	line, pos, list := r.complete(line, pos)
	if len(list) != 0 {
		r.term.Write([]byte(formatCompletionList(list, r.width)))
	}
	return line, pos, true
}

func (r *terminalReader) ReadLine(prompt string) (string, error) {
	state, err := term.MakeRaw(r.fd)
	if err != nil {
		return "", err
	}
	r.width = 80
	if width, height, err := term.GetSize(r.fd); err == nil && width > 0 && height > 0 {
		r.term.SetSize(width, height)
		r.width = width
	}
	r.term.SetPrompt(prompt)
	r.input.interrupted = false
	line, err := r.term.ReadLine()
	term.Restore(r.fd, state)
	if errors.Is(err, io.EOF) && r.input.interrupted {
		r.reset()
		io.WriteString(r.output, "^C\n")
		return "", errInterrupted
	}
	if errors.Is(err, io.EOF) {
		io.WriteString(r.output, "\n")
	}
	return line, err
}

func (r *terminalReader) Close() { r.history.Close() }

// interruptReader distinguishes Ctrl-C from Ctrl-D, which x/term both
// report as io.EOF while reading a line. It also maps newline to carriage
// return: lines typed ahead while a command ran in cooked mode had Enter
// translated to newline, which x/term does not accept as Enter.
// Bytes after a Ctrl-C are held back for the next editor, since the
// interrupted one is discarded with its buffer.
type interruptReader struct {
	reader      io.Reader
	interrupted bool
	pending     []byte
}

func (r *interruptReader) Read(buffer []byte) (int, error) {
	var n int
	var err error
	if len(r.pending) != 0 {
		n = copy(buffer, r.pending)
		r.pending = r.pending[n:]
	} else {
		n, err = r.reader.Read(buffer)
	}
	for i := range buffer[:n] {
		switch buffer[i] {
		case 3:
			r.interrupted = true
			r.pending = append(append([]byte(nil), buffer[i+1:n]...), r.pending...)
			return i + 1, err
		case '\n':
			buffer[i] = '\r'
		}
	}
	return n, err
}

// replHistory keeps recent lines in memory and appends new ones to the
// history file, when one can be opened.
type replHistory struct {
	entries []string
	file    *os.File
}

func replHistoryPath() string {
	if path := os.Getenv("KASH_HISTORY"); path != "" {
		return path
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "kash", "history")
}

func loadReplHistory() *replHistory {
	h := &replHistory{}
	path := replHistoryPath()
	if path == "" {
		return h
	}
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if line != "" {
				h.entries = append(h.entries, line)
			}
		}
		if len(h.entries) > replHistoryLength {
			h.entries = h.entries[len(h.entries)-replHistoryLength:]
		}
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		h.file, _ = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	}
	return h
}

func (h *replHistory) Add(entry string) {
	if strings.TrimSpace(entry) == "" || (len(h.entries) != 0 && h.entries[len(h.entries)-1] == entry) {
		return
	}
	h.entries = append(h.entries, entry)
	if len(h.entries) > replHistoryLength {
		h.entries = h.entries[1:]
	}
	if h.file != nil {
		h.file.WriteString(entry + "\n")
	}
}

func (h *replHistory) Len() int { return len(h.entries) }

func (h *replHistory) At(index int) string { return h.entries[len(h.entries)-1-index] }

func (h *replHistory) Close() {
	if h.file != nil {
		h.file.Close()
	}
}
