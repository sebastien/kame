package main

import (
	"bufio"
	"errors"
	"io"
	"kame/lang/script"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/reeflective/readline"
	"github.com/reeflective/readline/inputrc"
	"golang.org/x/term"
)

const replHistoryLength = 1000

// resetInputModes turns off terminal input reports that a crashed or killed
// full-screen program may have left on: mouse tracking (and its SGR/urxvt
// encodings), focus events, and kitty keyboard flags. The editor reads none
// of them, so they would otherwise be echoed or inserted as text.
const resetInputModes = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1015l\x1b[?1004l\x1b[=0;1u"

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
	right   string   // right prompt, empty for none
	output  *os.File // replaces a non-terminal stdout while editing
	context func() completionContext
	// snapshot is the session state for highlighting, taken by the session
	// before each entry is read.
	snapshot completionContext
	color    bool
	known    map[string]bool          // command words judged during this entry
	path     map[string]pathDirectory // PATH directory listings, kept across entries
	menu     bool                     // the latest completion has multiple candidates
}

// pathDirectory is a PATH directory's entry names as of its modification time.
type pathDirectory struct {
	modified time.Time
	names    map[string]struct{}
}

func newEditorReader() *editorReader {
	e := &editorReader{history: loadReplHistory()}
	e.resetEditor()
	return e
}

// Keep one readline instance so buffered keystrokes survive prompt boundaries;
// clear its separate history-edit cache explicitly.
func (e *editorReader) resetEditor() {
	e.shell = readline.NewShell(inputrc.WithApp("kash"))
	// Terminals that do not answer cursor-position queries would stall the
	// editor; dumb ones are assumed not to.
	terminal := os.Getenv("TERM")
	if terminal == "" || terminal == "dumb" {
		e.shell.Config.Set("cursor-position-probe", false)
	} else {
		// A bracketed paste is inserted at once, with one redraw, and its
		// line breaks do not submit the entry; otherwise every pasted
		// character is a keystroke. Bash enables it by default too.
		e.shell.Config.Set("enable-bracketed-paste", true)
	}
	e.color = os.Getenv("NO_COLOR") == "" && terminal != "dumb"
	if !term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd())) {
		e.output = os.Stderr
	}
	e.shell.History.Delete()
	e.shell.History.Add("kash", e.history)
	e.shell.AcceptMultiline = func(line []rune) bool { return replEntryComplete(string(line)) }
	e.shell.Prompt.Primary(func() string { return e.prompt })
	e.shell.Prompt.Right(func() string { return e.right })
	e.shell.Hint.SetProvider(e.completionSeparator)
	// Redraw a menu without accepting its virtual insertion or dropping its keymap.
	e.shell.Config.Bind("menu-select", "\x0c", "clear-screen", false)
	if e.shell.Config.GetString("completion-selection-style") == "\x1b[1;30m" {
		e.shell.Config.Set("completion-selection-style", "\x1b[39;49;7m")
	}
	// Tab inserts the common prefix and lists the candidates; Tab again
	// selects in the menu.
	e.shell.Config.Set("menu-complete-display-prefix", true)
	// Tab indents at the start of a line, as block bodies need, and
	// completes elsewhere.
	e.shell.Keymap.Register(map[string]func(){"kash-complete-or-indent": e.completeOrIndent, "kash-insert-newline": e.insertNewline, "kash-history-toggle": e.toggleHistory})
	for _, keymap := range []string{"emacs", "emacs-standard", "vi-insert"} {
		e.shell.Config.Bind(keymap, "\t", "kash-complete-or-indent", false)
		// Ctrl-X Enter adds a line without submitting, e.g. for a final &
		// line. (This readline never dispatches ESC Enter, so not Alt-Enter.)
		e.shell.Config.Bind(keymap, "\x18\r", "kash-insert-newline", false)
		e.shell.Config.Bind(keymap, "\x18h", "kash-history-toggle", false)
	}
	e.shell.Config.Bind("vi-command", "\x18h", "kash-history-toggle", false)
	if e.context != nil {
		e.shell.Completer = e.complete
		if e.color {
			e.shell.SyntaxHighlighter = e.highlight
		}
	}
	// Expired history must not bypass GetLine through readline's undo cache,
	// even if this prompt remains open across a retention deadline.
	guarded := make(map[string]func())
	for name, command := range e.shell.Keymap.Commands() {
		guarded[name] = func() {
			if e.history.expireView() {
				e.clearHistoryCache()
			}
			command()
		}
	}
	e.shell.Keymap.Register(guarded)
}

// Revert releases readline's per-entry undo items through its public API.
// Merely deleting a Source leaves these cached texts indexed by source name.
func (e *editorReader) clearHistoryCache() {
	line := append([]rune(nil), (*e.shell.Line())...)
	cursor := e.shell.Cursor().Pos()
	n := e.history.Len()
	e.shell.History.Walk(n + 1)
	for i := 0; i < n; i++ {
		e.shell.History.Revert()
		e.shell.History.Walk(-1)
	}
	e.shell.History.Revert()
	e.shell.History.Delete("kash")
	e.shell.History.Add("kash", e.history)
	e.shell.Line().Set(line...)
	e.shell.Cursor().Set(cursor)
}

func (e *editorReader) toggleHistory() {
	e.clearHistoryCache()
	e.history.setScope(!e.history.global)
	e.history.refresh(e.history.cwd)
	view := "local"
	if e.history.global {
		view = "global"
	}
	e.shell.Hint.Set("history: " + view)
}

// completionSeparator lives in readline's hint area, so resize and cleanup
// use the same layout bookkeeping as the completion menu.
func (e *editorReader) completionSeparator(_ []rune, _ int) []rune {
	if !e.menu || e.shell.Keymap.Local() != "menu-select" {
		return nil
	}
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width < 2 {
		return nil
	}
	glyph := "─"
	if os.Getenv("TERM") == "dumb" {
		glyph = "-"
	}
	line := strings.Repeat(glyph, width-1)
	if e.color {
		line = "\x1b[2m" + line + "\x1b[0m"
	}
	return []rune(line)
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
	e.prompt, e.known, e.menu = prompt, nil, false
	if e.output != nil {
		// readline renders on os.Stdout; keep the prompt out of redirected
		// output. Commands are not running while an entry is edited.
		stdout := os.Stdout
		os.Stdout = e.output
		defer func() { os.Stdout = stdout }()
	}
	if terminal := os.Getenv("TERM"); terminal != "" && terminal != "dumb" {
		io.WriteString(os.Stdout, resetInputModes)
	}
	line, err := e.shell.Readline()
	e.clearHistoryCache()
	// readline echoes ^C and ends the line itself.
	switch {
	case errors.Is(err, readline.ErrInterrupt):
		return "", errInterrupted
	case errors.Is(err, io.EOF):
		return "", io.EOF
	case err != nil:
		return "", err
	}
	// Record ourselves: readline deduplicates against the selected view and
	// honors inputrc's append cap, neither of which is our per-scope policy.
	e.history.record(line)
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
	e.menu = len(candidates) > 1
	// An empty word directly after a delimiter would let readline pick the
	// delimiter as the prefix; claim it and reinsert it instead.
	lead := ""
	if start := pos - len(word); word == "" && start > 0 && !strings.ContainsRune(" \t\n", rune(text[start-1])) {
		lead = text[start-1 : start]
	}
	values := make([]readline.Completion, len(candidates))
	for i, c := range candidates {
		style := ""
		if e.color {
			style = c.Style
		}
		values[i] = readline.Completion{Value: lead + c.Text, Display: c.Display, Description: c.Description, Tag: c.Tag, Style: style}
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
	ctx := e.snapshot
	if _, ok := ctx.Aliases[word]; ok {
		return true
	}
	if strings.Contains(word, "/") {
		_, err := os.Stat(completionDirectory(word, ctx))
		return err == nil
	}
	if known, ok := e.known[word]; ok {
		return known
	}
	if e.known == nil {
		e.known = map[string]bool{}
	}
	known := false
	for _, directory := range commandPathDirectories(ctx) {
		if _, ok := e.pathNames(directory)[word]; ok && executableFile(filepath.Join(directory, word)) {
			known = true
			break
		}
	}
	e.known[word] = known
	return known
}

// pathNames lists a PATH directory, reusing the previous listing while the
// directory is unmodified: only the looked-up word is then checked with stat.
func (e *editorReader) pathNames(directory string) map[string]struct{} {
	info, err := os.Stat(directory)
	if err != nil || directory == "" {
		delete(e.path, directory)
		return nil
	}
	if cached, ok := e.path[directory]; ok && cached.modified.Equal(info.ModTime()) {
		return cached.names
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = struct{}{}
	}
	if e.path == nil {
		e.path = map[string]pathDirectory{}
	}
	e.path[directory] = pathDirectory{modified: info.ModTime(), names: names}
	return names
}
