package main

import (
	"bufio"
	"errors"
	"io"
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

// runInteractive runs preloaded work, then reads, appends, and runs Kash
// entries until end of input or exit. Failures end the entry, not the session.
// The caller joins outstanding work and runs finalization as for batch runs.
func runInteractive(run *sessionRun, in io.Reader) int {
	session := run.Session
	run.Interactive = true
	// Progress rows and taskbar state would draw over foreground programs and
	// the prompt; commands report through their own output instead.
	dashboardLive = false
	emitTaskbar(run.Err, 0, 0)
	taskbarEnabled = false
	entries := 0
	for i := 0; i < len(session.Work); i++ {
		if status, stop := run.step(i); stop {
			return status
		}
	}
	reader := newLineReader(in)
	defer reader.Close()
	if _, ok := reader.(*terminalReader); ok {
		// Foreground commands receive the terminal; Kame waits like a shell
		// without job control.
		session.Program.Eval.Terminal = true
		posix.IgnoreJobControlSignals()
	}
	shell := replShell{Program: session.Program, FollowGrants: len(run.Inv.Grants) == 0 && !run.Inv.NoDefaultGrants}
	status := 0
	for {
		text, err := readEntry(reader, shell.prompt())
		if errors.Is(err, errInterrupted) {
			status = 130
			continue
		}
		if err != nil {
			break
		}
		// Entries, builtins included, are numbered in typed order.
		entries++
		if handled, builtin := shell.run(text, run.Err); handled {
			status = builtin
			continue
		}
		name := "<kash:" + strconv.Itoa(entries) + ">"
		run.Fragments = append(run.Fragments, program.Fragment{Name: name, Text: text, Lang: "kash"})
		first, diagnostics := session.AppendKash(name, text)
		for i := range diagnostics {
			run.emit(diagnosticWriter(run.Out, run.Err, run.Inv.JSON), diagnostics[i], run.Inv.JSON)
			diagnostics[i].Free(mem.System)
		}
		if len(diagnostics) != 0 {
			status = 1
			continue
		}
		posix.ResetSignals()
		run.StartedAt = time.Now().UnixNano() // --timeout bounds each entry.
		status = 0
		for i := first; i < len(session.Work); i++ {
			var stop bool
			if status, stop = run.step(i); stop {
				return status
			}
			if status != 0 {
				break
			}
		}
	}
	if run.joinNeeded() {
		if joined, stop := run.step(len(session.Work)); stop || joined != 0 {
			return joined
		}
	}
	return status
}

// readEntry assembles one entry. Text ending inside an expression continues
// until it closes; a control header continues until a blank line, as the
// body's extent cannot be known before then.
func readEntry(reader lineReader, prompt string) (string, error) {
	var lines []string
	block := false
	for {
		if len(lines) != 0 {
			prompt = replContinuation
		}
		line, err := reader.ReadLine(prompt)
		if errors.Is(err, io.EOF) && len(lines) != 0 {
			return strings.Join(lines, "\n"), nil
		}
		if err != nil {
			return "", err
		}
		blank := strings.TrimSpace(line) == ""
		if blank && len(lines) == 0 {
			continue
		}
		if blank && block {
			return strings.Join(lines, "\n"), nil
		}
		lines = append(lines, line)
		text := strings.Join(lines, "\n")
		switch script.ClassifyKashInput(text) {
		case script.KashBlock:
			block = true
		case script.KashComplete:
			if !block {
				return text, nil
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

// pipeReader reads entries from non-terminal input without prompts.
type pipeReader struct{ reader *bufio.Reader }

func (r *pipeReader) ReadLine(string) (string, error) {
	line, err := r.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (r *pipeReader) Close() {}

// terminalReader edits lines in raw mode only while reading, so commands run
// with the terminal's normal settings and signal keys.
type terminalReader struct {
	fd      int
	input   *interruptReader
	output  *os.File
	history *replHistory
	term    *term.Terminal
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
}

func (r *terminalReader) ReadLine(prompt string) (string, error) {
	state, err := term.MakeRaw(r.fd)
	if err != nil {
		return "", err
	}
	if width, height, err := term.GetSize(r.fd); err == nil && width > 0 && height > 0 {
		r.term.SetSize(width, height)
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
type interruptReader struct {
	reader      io.Reader
	interrupted bool
}

func (r *interruptReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	for i := range buffer[:n] {
		switch buffer[i] {
		case 3:
			r.interrupted = true
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
