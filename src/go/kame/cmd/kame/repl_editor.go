package main

import (
	"bufio"
	"errors"
	"io"
	"kame/lang/script"
	"os"
	"path/filepath"
	"strings"

	"github.com/reeflective/readline"
	"github.com/reeflective/readline/inputrc"
	"golang.org/x/term"
)

const replHistoryLength = 1000

func newLineReader(in io.Reader) lineReader {
	if file, ok := in.(*os.File); ok && file == os.Stdin && term.IsTerminal(int(file.Fd())) {
		return newEditorReader()
	}
	return &pipeReader{reader: bufio.NewReader(in)}
}

// editorReader edits whole entries with readline, in raw mode only while
// reading, so commands run with the terminal's normal settings.
type editorReader struct {
	shell   *readline.Shell
	history *replHistory
	prompt  string
	output  *os.File // replaces a non-terminal stdout while editing
	context func() completionContext
	color   bool
	known   map[string]bool // command names on PATH, gathered once per entry
}

func newEditorReader() *editorReader {
	e := &editorReader{history: loadReplHistory()}
	e.shell = readline.NewShell(inputrc.WithApp("kash"))
	// Terminals that do not answer cursor-position queries would stall the
	// editor; dumb ones are assumed not to.
	terminal := os.Getenv("TERM")
	if terminal == "" || terminal == "dumb" {
		e.shell.Config.Set("cursor-position-probe", false)
	}
	e.color = os.Getenv("NO_COLOR") == "" && terminal != "dumb"
	if !term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd())) {
		e.output = os.Stderr
	}
	e.shell.History.Delete()
	e.shell.History.Add("kash", e.history)
	e.shell.AcceptMultiline = func(line []rune) bool { return replEntryComplete(string(line)) }
	e.shell.Prompt.Primary(func() string { return e.prompt })
	// Tab inserts the common prefix and lists the candidates; Tab again
	// selects in the menu.
	e.shell.Config.Set("menu-complete-display-prefix", true)
	// Tab indents at the start of a line, as block bodies need, and
	// completes elsewhere.
	e.shell.Keymap.Register(map[string]func(){"kash-complete-or-indent": e.completeOrIndent, "kash-insert-newline": e.insertNewline})
	for _, keymap := range []string{"emacs", "emacs-standard", "vi-insert"} {
		e.shell.Config.Bind(keymap, "\t", "kash-complete-or-indent", false)
		// Ctrl-X Enter adds a line without submitting, e.g. for a final &
		// line. (This readline never dispatches ESC Enter, so not Alt-Enter.)
		e.shell.Config.Bind(keymap, "\x18\r", "kash-insert-newline", false)
	}
	return e
}

func (e *editorReader) insertNewline() {
	line, cursor := e.shell.Line(), e.shell.Cursor()
	line.Insert(cursor.Pos(), '\n')
	cursor.Inc()
}

func (e *editorReader) completeOrIndent() {
	line, cursor := e.shell.Line(), e.shell.Cursor()
	before := string((*line)[:cursor.Pos()])
	if start := strings.LastIndexByte(before, '\n') + 1; strings.TrimLeft(before[start:], " \t") == "" {
		line.Insert(cursor.Pos(), '\t')
		cursor.Inc()
		return
	}
	e.shell.Keymap.Commands()["complete"]()
}

// bind connects completion and highlighting to the session.
func (e *editorReader) bind(repl *replSession) {
	// The editor runs on its own goroutine: it asks the session's goroutine,
	// which owns the program, for a snapshot (replSession.read).
	e.context = func() completionContext {
		reply := make(chan completionContext, 1)
		repl.contextRequests <- reply
		return <-reply
	}
	e.shell.Completer = e.complete
	if e.color {
		e.shell.SyntaxHighlighter = e.highlight
	}
}

// ReadLine reads one whole entry; the prompt is the first-line prompt.
func (e *editorReader) ReadLine(prompt string) (string, error) {
	e.prompt, e.known = prompt, nil
	if e.output != nil {
		// readline renders on os.Stdout; keep the prompt out of redirected
		// output. Commands are not running while an entry is edited.
		stdout := os.Stdout
		os.Stdout = e.output
		defer func() { os.Stdout = stdout }()
	}
	line, err := e.shell.Readline()
	// readline echoes ^C and ends the line itself.
	switch {
	case errors.Is(err, readline.ErrInterrupt):
		return "", errInterrupted
	case errors.Is(err, io.EOF):
		return "", io.EOF
	case err != nil:
		return "", err
	}
	// A block entry ends with the blank line that submitted it.
	return strings.TrimRight(line, " \t\r\n"), nil
}

func (e *editorReader) Close() { e.history.Close() }

// replEntryComplete reports whether text is a whole entry, by the same rules
// readEntry applies line by line: open expressions continue, and an entry
// that began a control block continues until a blank line.
func replEntryComplete(text string) bool {
	if head, last, multi := cutLastLine(text); multi && replBackgroundLine(last) && strings.TrimSpace(head) != "" {
		return true
	}
	lines := strings.Split(text, "\n")
	block := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			if block || i == len(lines)-1 && strings.TrimSpace(text) == "" {
				return true
			}
			continue
		}
		switch script.ClassifyKashInput(strings.Join(lines[:i+1], "\n")) {
		case script.KashBlock:
			block = true
		case script.KashComplete:
			if !block && i == len(lines)-1 {
				return true
			}
		}
	}
	return false
}

func (e *editorReader) complete(line []rune, cursor int) readline.Completions {
	text := string(line)
	pos := len(string(line[:cursor]))
	word, candidates := completionsAt(text, pos, e.context())
	// An empty word directly after a delimiter would let readline pick the
	// delimiter as the prefix; claim it and reinsert it instead.
	lead := ""
	if start := pos - len(word); word == "" && start > 0 && !strings.ContainsRune(" \t\n", rune(text[start-1])) {
		lead = text[start-1 : start]
	}
	values := make([]readline.Completion, len(candidates))
	for i, c := range candidates {
		values[i] = readline.Completion{Value: lead + c.Text, Display: c.Display, Description: c.Description, Tag: c.Tag}
	}
	completions := readline.CompleteRaw(values).NoSpace('/')
	completions.PREFIX = lead + word
	return completions
}

// highlight colors an entry by Kash token, marking command words that name
// no session command, keyword, existing path, or executable on PATH.
func (e *editorReader) highlight(line []rune) string {
	text := string(line)
	var b strings.Builder
	last := 0
	for _, token := range script.KashTokens(text) {
		b.WriteString(text[last:token.Start])
		style := replTokenStyles[token.Kind]
		if token.Kind == script.KashTokenCommand && !e.commandKnown(text, token) {
			style = replUnknownCommandStyle
		}
		if style == "" {
			b.WriteString(text[token.Start:token.End])
		} else {
			b.WriteString("\x1b[" + style + "m" + text[token.Start:token.End] + "\x1b[0m")
		}
		last = token.End
	}
	b.WriteString(text[last:])
	return b.String()
}

var replTokenStyles = map[script.KashTokenKind]string{
	script.KashTokenCommand:    "1;32",
	script.KashTokenKeyword:    "1;35",
	script.KashTokenDefinition: "34",
	script.KashTokenOption:     "36",
	script.KashTokenString:     "33",
	script.KashTokenVariable:   "1;36",
	script.KashTokenOperator:   "1",
	script.KashTokenComment:    "2",
	script.KashTokenFunction:   "34",
	script.KashTokenConstant:   "35",
	script.KashTokenNumber:     "35",
}

const replUnknownCommandStyle = "1;31"

// commandKnown checks a whole literal command word. Words with substitutions
// or quotes are not judged.
func (e *editorReader) commandKnown(text string, token script.KashToken) bool {
	if token.Start > 0 && !strings.ContainsRune(" \t\n;|&(", rune(text[token.Start-1])) ||
		token.End < len(text) && !strings.ContainsRune(" \t\n;|&)<>", rune(text[token.End])) {
		return true
	}
	word := completionUnescape(text[token.Start:token.End])
	if replSessionCommands[word] != "" {
		return true
	}
	ctx := e.context()
	if _, ok := ctx.Aliases[word]; ok {
		return true
	}
	if strings.Contains(word, "/") {
		_, err := os.Stat(completionDirectory(word, ctx))
		return err == nil
	}
	if e.known == nil {
		e.known = map[string]bool{}
		for _, directory := range commandPathDirectories(ctx) {
			if entries, err := os.ReadDir(directory); err == nil && directory != "" {
				for _, entry := range entries {
					e.known[entry.Name()] = executableFile(filepath.Join(directory, entry.Name()))
				}
			}
		}
	}
	return e.known[word]
}

// replHistory keeps recent entries in memory, oldest first, and appends new
// ones to the history file when one can be opened. A multi-line entry is
// stored as lines ending in a backslash, which a Kash line cannot.
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
		h.entries = parseReplHistory(string(data))
		if len(h.entries) > replHistoryLength {
			h.entries = h.entries[len(h.entries)-replHistoryLength:]
		}
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		h.file, _ = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	}
	return h
}

func parseReplHistory(data string) []string {
	var entries []string
	var pending []string
	for _, line := range strings.Split(data, "\n") {
		if continued, ok := strings.CutSuffix(line, "\\"); ok {
			pending = append(pending, continued)
			continue
		}
		pending = append(pending, line)
		if entry := strings.Join(pending, "\n"); strings.TrimSpace(entry) != "" {
			entries = append(entries, entry)
		}
		pending = pending[:0]
	}
	return entries
}

func formatReplHistory(entry string) string {
	return strings.ReplaceAll(entry, "\n", "\\\n") + "\n"
}

// Write records an accepted entry, as readline's history source.
func (h *replHistory) Write(entry string) (int, error) {
	entry = strings.TrimRight(entry, " \t\r\n")
	if strings.TrimSpace(entry) == "" || len(h.entries) != 0 && h.entries[len(h.entries)-1] == entry {
		return len(h.entries), nil
	}
	h.entries = append(h.entries, entry)
	if len(h.entries) > replHistoryLength {
		h.entries = h.entries[1:]
	}
	if h.file != nil {
		h.file.WriteString(formatReplHistory(entry))
	}
	return len(h.entries), nil
}

func (h *replHistory) GetLine(index int) (string, error) {
	if index < 0 || index >= len(h.entries) {
		return "", errors.New("history index out of range")
	}
	return h.entries[index], nil
}

func (h *replHistory) Len() int { return len(h.entries) }

func (h *replHistory) Dump() any { return h.entries }

func (h *replHistory) Close() {
	if h.file != nil {
		h.file.Close()
	}
}
