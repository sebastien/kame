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
	terminal, _ := reader.(*editorReader)
	if terminal != nil {
		// Foreground commands receive the terminal; Kame waits like a shell
		// without job control.
		session.Program.Eval.Terminal = true
		posix.IgnoreJobControlSignals()
		terminal.bind(repl)
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
	home, _ := s.shell.lookup("HOME")
	path, _ := s.shell.lookup("PATH")
	return completionContext{Directory: p.Options.Directory, Home: home, Path: path, Names: p.Eval.InteractiveNames()}
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
