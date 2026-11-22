package main

import (
	"iter"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/source"
	"kame/lang/usage"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// completionContext is the session state completion reads. Gathering it
// evaluates nothing.
type completionContext struct {
	Directory string // session directory, absolute
	Home      string // $HOME of the session child environment
	Path      string // $PATH of the session child environment
	Names     []eval.InteractiveName
	Aliases   map[string][]string
	Specs     *completionSpecs // command specs; nil disables them
}

// completionCandidate is one completion. Text replaces the word being
// completed; directories end with / and take no trailing space.
type completionCandidate struct {
	Text        string // inserted text, escaped
	Display     string // listed text
	Description string
	Tag         string // group heading
	Style       string // display-only SGR style
}

const (
	completionDelimiters = " \t\r\n()[]|;&<>=\"'"
	completionEscapes    = " \t\\()[]{}|;&<>\"'$@#*?~"
)

// Completion group headings, in listing order.
const (
	tagSession   = "session commands"
	tagKeywords  = "keywords"
	tagCommands  = "commands"
	tagFunctions = "functions"
	tagBindings  = "bindings"
	tagCommand   = "subcommands"
	tagOptions   = "options"
	tagValues    = "values"
	tagFiles     = "files"
)

var (
	replSessionCommands = map[string]string{"alias": "define or list command aliases", "unalias": "remove command aliases", "cd": "change the session directory", "source": "run a file in the session", "jobs": "list jobs", "fg": "resume a job in the foreground", "bg": "resume a job in the background", "wait": "wait for jobs to finish", "history": "select history scope or clear history", "option": "inspect or change shell options", "explain": "show the full report of the latest failure", "complete": "refresh or show command completion specs"}
	replKeywords        = []string{"def", "defer", "elif", "else", "exit", "for", "if", "let", "match", "return", "while", "with"}
)

// completionsAt returns the word before pos and its candidates, sorted by
// group and text, without duplicates.
func completionsAt(line string, pos int, ctx completionContext) (string, []completionCandidate) {
	start := completionWordStart(line, pos)
	word, before := line[start:pos], line[:start]
	candidates := completionCandidates(word, before, ctx)
	slices.SortStableFunc(candidates, func(a, b completionCandidate) int { return strings.Compare(a.Text, b.Text) })
	candidates = slices.CompactFunc(candidates, func(a, b completionCandidate) bool { return a.Text == b.Text })
	return word, candidates
}

// completionWordStart finds the start of the word ending at pos: the byte
// after the nearest unescaped delimiter.
func completionWordStart(line string, pos int) int {
	return completionAfterDelimiter(line[:pos], completionDelimiters)
}

func completionAfterDelimiter(line, delimiters string) int {
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) {
			if end := source.ContinuationEnd(line, i, len(line)); end != i {
				i = end - 1
			} else {
				i++
			}
		} else if strings.IndexByte(delimiters, line[i]) >= 0 {
			start = i + 1
		}
	}
	return start
}

func completionCandidates(word, before string, ctx completionContext) []completionCandidate {
	entryBefore := before
	// Use the current logical line, retaining escaped line endings.
	before = before[completionAfterDelimiter(before, "\n"):]
	if fields := strings.Fields(before); len(fields) != 0 && fields[0] == "option" {
		args := fields[1:]
		if len(args) != 0 && (args[0] == "-l" || args[0] == "--local" || args[0] == "-g" || args[0] == "--global") {
			args = args[1:]
		}
		var candidates []completionCandidate
		if len(args) == 0 {
			for _, option := range replOptionRegistry {
				if strings.HasPrefix(option.name, word) {
					candidates = append(candidates, completionCandidate{Text: option.name, Description: option.description, Tag: tagSession})
				}
			}
			if len(fields) == 1 {
				for _, flag := range []string{"-l", "--local", "-g", "--global"} {
					if strings.HasPrefix(flag, word) {
						candidates = append(candidates, completionCandidate{Text: flag, Description: "option scope", Tag: tagSession})
					}
				}
			}
		} else if len(args) == 1 {
			if option, ok := findReplOption(args[0]); ok {
				for _, choice := range option.choices {
					if strings.HasPrefix(choice, word) {
						candidates = append(candidates, completionCandidate{Text: choice, Tag: tagSession})
					}
				}
			}
		}
		return candidates
	}
	trimmed := strings.TrimRight(before, " \t")
	switch {
	case strings.HasPrefix(word, "$"):
		var candidates []completionCandidate
		for _, name := range ctx.Names {
			if !name.Callable && strings.HasPrefix("$"+name.Name, word) {
				candidates = append(candidates, completionCandidate{Text: "$" + name.Name, Display: "$" + name.Name, Description: name.Detail, Tag: tagBindings})
			}
		}
		return candidates
	case strings.HasSuffix(before, "(") && !strings.HasSuffix(before, "$("):
		var candidates []completionCandidate
		for _, name := range ctx.Names {
			if name.Callable && strings.HasPrefix(name.Name, word) {
				candidates = append(candidates, completionCandidate{Text: name.Name, Display: name.Name, Description: name.Detail, Tag: tagFunctions})
			}
		}
		return candidates
	case completionCommandPosition(trimmed) && !strings.Contains(word, "/"):
		var candidates []completionCandidate
		for name, prefix := range ctx.Aliases {
			if strings.HasPrefix(name, word) {
				candidates = append(candidates, completionCandidate{Text: name, Display: name, Description: strings.Join(prefix, " "), Tag: tagCommands})
			}
		}
		for name, description := range replSessionCommands {
			if strings.HasPrefix(name, word) && trimmed == "" {
				candidates = append(candidates, completionCandidate{Text: name, Display: name, Description: description, Tag: tagSession})
			}
		}
		for _, name := range replKeywords {
			if strings.HasPrefix(name, word) {
				candidates = append(candidates, completionCandidate{Text: name, Display: name, Tag: tagKeywords})
			}
		}
		return append(candidates, completeExecutables(word, ctx)...)
	case strings.TrimSpace(before) == "cd":
		return completePaths(word, ctx, true)
	default:
		if candidates, ok := commandCompletions(word, entryBefore, ctx); ok {
			return candidates
		}
		return completePaths(word, ctx, false)
	}
}

// commandCompletions completes the arguments of an external command from
// its spec (spec 058). ok is false when no spec applies.
func commandCompletions(word, before string, ctx completionContext) ([]completionCandidate, bool) {
	if ctx.Specs == nil {
		return nil, false
	}
	words, current, ok := commandWords(before + word)
	if !ok || len(words) == 0 || !strings.HasSuffix(current.raw, word) {
		return nil, false
	}
	// The editor's word starts after delimiters such as =; the spec sees the
	// whole argument, and prefix is the part already typed before the word.
	prefix, ok := expr.LiteralCommandWord(current.raw[:len(current.raw)-len(word)])
	if !ok || !words[0].literal {
		return nil, false
	}
	name, args := words[0].text, words[1:]
	if alias := ctx.Aliases[name]; len(alias) != 0 {
		name = alias[0]
		args = append(literalWords(alias[1:]), args...)
	}
	name = strings.TrimPrefix(name, "x/")
	if replSessionCommands[name] != "" || slices.Contains(replKeywords, name) {
		return nil, false
	}
	typed := name
	name = filepath.Base(name)
	if !specName.MatchString(name) {
		return nil, false
	}
	texts := make([]string, len(args))
	literal := current.literal
	for i, w := range args {
		texts[i] = w.text
		literal = literal && w.literal
	}
	var matches []usage.Candidate
	var files usage.Files
	live := false
	if executable := resolveExecutable(typed, ctx); literal && executable != "" {
		if protocol := ctx.Specs.protocol(executable); protocol != "" {
			matches, files, live = liveComplete(protocol, executable, typed, texts, current.text, ctx)
			matches = slices.DeleteFunc(matches, func(m usage.Candidate) bool { return !strings.HasPrefix(m.Text, current.text) })
		}
	}
	if !live {
		spec, found, cached := ctx.Specs.lookup(name)
		if !cached && resolveCommand(name, ctx) {
			ctx.Specs.await(name)
			spec, found, _ = ctx.Specs.lookup(name)
		}
		if !found {
			return nil, false
		}
		matches, files = spec.Complete(texts, current.text)
	}
	var candidates []completionCandidate
	for _, m := range matches {
		if !strings.HasPrefix(m.Text, prefix) || strings.IndexFunc(m.Text, unicode.IsControl) >= 0 {
			continue
		}
		text := m.Text[len(prefix):]
		tag := map[string]string{"command": tagCommand, "flag": tagOptions}[m.Kind]
		if tag == "" {
			tag = tagValues
		}
		description := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, m.Help)
		candidates = append(candidates, completionCandidate{Text: completionEscape(text), Display: text, Description: description, Tag: tag})
	}
	switch files {
	case usage.AnyFiles:
		candidates = append(candidates, completePaths(word, ctx, false)...)
	case usage.DirFiles:
		candidates = append(candidates, completePaths(word, ctx, true)...)
	}
	return candidates, true
}

type commandWord struct {
	raw, text string
	literal   bool // text is an exact argv value, not a substitution or a glob
}

func decodeCommandWord(raw string) commandWord {
	text, literal := expr.LiteralCommandWord(raw)
	if !literal {
		text = raw
	}
	return commandWord{raw: raw, text: text, literal: literal}
}

func literalWords(texts []string) []commandWord {
	words := make([]commandWord, len(texts))
	for i, text := range texts {
		words[i] = commandWord{text: text, literal: true}
	}
	return words
}

// commandWords splits the command that the end of text belongs to into its
// words before the last one and the last one, which may be empty. It follows
// pipelines, sequences, and $( ) and ( ) groups, drops redirections and a
// leading if, elif or while, and reports false inside an open quote or
// expression group, or for a redirection target.
func commandWords(text string) ([]commandWord, commandWord, bool) {
	type segment struct {
		words     []commandWord
		start     int
		close     byte
		exprGroup bool
	}
	var stack []segment
	current := segment{start: -1}
	var quote byte
	end := func(i int) {
		if current.start >= 0 {
			current.words = append(current.words, decodeCommandWord(text[current.start:i]))
		}
		current.start = -1
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(text) {
				i++
			}
			continue
		}
		start := current.start
		if start < 0 {
			start = i
		}
		switch c {
		case '\\':
			if next := source.ContinuationEnd(text, i, len(text)); next != i {
				i = next - 1
				continue
			}
			current.start = start
			if i+1 < len(text) {
				i++
			}
		case '\'', '"':
			quote = c
			current.start = start
		case ' ', '\t', '\r':
			end(i)
		case '\n':
			end(i)
			if current.close == 0 {
				current.words = nil
			}
		case '#':
			if current.start < 0 && !current.exprGroup {
				n := strings.IndexByte(text[i:], '\n')
				if n < 0 {
					return nil, commandWord{}, false
				}
				i += n - 1
			} else {
				current.start = start
			}
		case '|', ';', '&', '?':
			// & within a redirection, as in 2>&1 or &>file, is part of the word.
			if c == '&' && (i > start && (text[i-1] == '>' || text[i-1] == '<') || i+1 < len(text) && text[i+1] == '>') {
				current.start = start
				continue
			}
			end(i)
			current.words = nil
		case '(', '{':
			interpolation := i > start && text[i-1] == '$' && !completionEscaped(text, i-1)
			if c == '{' && !interpolation && current.close != '}' {
				current.start = start
				continue
			}
			current.start = start
			stack = append(stack, current)
			current = segment{start: -1, close: ')', exprGroup: c != '(' || !interpolation}
			if c == '{' {
				current.close = '}'
			}
		case ')', '}':
			if c != current.close {
				if c == '}' {
					current.start = start
					continue
				}
				return nil, commandWord{}, false
			}
			current = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		default:
			current.start = start
		}
	}
	if quote != 0 || current.exprGroup {
		return nil, commandWord{}, false
	}
	last := decodeCommandWord("")
	if current.start >= 0 {
		last = decodeCommandWord(text[current.start:])
	}
	var words []commandWord
	for i := 0; i < len(current.words); i++ {
		w := current.words[i].raw
		switch {
		case w == ">" || w == ">>" || w == "<" || w == "2>" || w == "2>>" || w == "&>":
			if i+1 == len(current.words) {
				return nil, commandWord{}, false // completing a redirection target
			}
			i++
		case strings.HasPrefix(w, ">") || strings.HasPrefix(w, "<") || strings.HasPrefix(w, "2>") || strings.HasPrefix(w, "&>"):
		case len(words) == 0 && (w == "if" || w == "elif" || w == "while"):
		default:
			words = append(words, current.words[i])
		}
	}
	if strings.HasPrefix(last.raw, ">") || strings.HasPrefix(last.raw, "<") || strings.HasPrefix(last.raw, "2>") || strings.HasPrefix(last.raw, "&>") {
		return nil, commandWord{}, false
	}
	return words, last, true
}

// completionCommandPosition reports whether a word after text, which has no
// trailing blanks, starts a command.
func completionCommandPosition(text string) bool {
	if text == "" || strings.HasSuffix(text, "$(") && !completionEscaped(text, len(text)-2) || strings.IndexByte("|;&", text[len(text)-1]) >= 0 && !completionEscaped(text, len(text)-1) {
		return true
	}
	for _, keyword := range []string{"if", "elif", "while"} {
		rest, ok := strings.CutSuffix(text, keyword)
		if ok && (rest == "" || rest[len(rest)-1] == ' ' || rest[len(rest)-1] == '\t') && completionCommandPosition(strings.TrimRight(rest, " \t")) {
			return true
		}
	}
	return false
}

func completionEscaped(text string, pos int) bool {
	end := pos
	for pos > 0 && text[pos-1] == '\\' {
		pos--
	}
	return (end-pos)%2 != 0
}

func commandPathDirectories(ctx completionContext) []string {
	directories := strings.Split(ctx.Path, string(os.PathListSeparator))
	for i, directory := range directories {
		if !filepath.IsAbs(directory) {
			directories[i] = filepath.Join(ctx.Directory, directory)
		}
	}
	return directories
}

func completeExecutables(word string, ctx completionContext) []completionCandidate {
	word, ok := expr.LiteralCommandWord(word)
	if !ok {
		return nil
	}
	var candidates []completionCandidate
	for directory, entry := range pathExecutables(ctx, func(name string) bool {
		return strings.HasPrefix(name, word) && (!strings.HasPrefix(name, ".") || strings.HasPrefix(word, "."))
	}) {
		display, style := entry.Name()+"*", "32"
		if entry.Type()&os.ModeSymlink != 0 {
			display, style = entry.Name()+"@", "36"
		}
		candidates = append(candidates, completionCandidate{Text: completionEscape(entry.Name()), Display: display, Description: directory, Tag: tagCommands, Style: style})
	}
	return candidates
}

// pathExecutables enumerates the first executable with each selected name.
func pathExecutables(ctx completionContext, accept func(string) bool) iter.Seq2[string, os.DirEntry] {
	return func(yield func(string, os.DirEntry) bool) {
		seen := map[string]bool{}
		for _, directory := range commandPathDirectories(ctx) {
			entries, _ := os.ReadDir(directory)
			for _, entry := range entries {
				name := entry.Name()
				if !seen[name] && accept(name) && executableFile(filepath.Join(directory, name)) {
					seen[name] = true
					if !yield(directory, entry) {
						return
					}
				}
			}
		}
	}
}

// resolveExecutable finds a literal command path or the first executable on PATH.
func resolveExecutable(word string, ctx completionContext) string {
	if strings.Contains(word, "/") {
		path := word
		if !filepath.IsAbs(path) {
			path = filepath.Join(ctx.Directory, path)
		}
		if executableFile(path) {
			return filepath.Clean(path)
		}
		return ""
	}
	for _, directory := range commandPathDirectories(ctx) {
		if path := filepath.Join(directory, word); directory != "" && executableFile(path) {
			return filepath.Clean(path)
		}
	}
	return ""
}

func resolveCommand(name string, ctx completionContext) bool {
	return !strings.Contains(name, "/") && resolveExecutable(name, ctx) != ""
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

// completePaths lists entries of the directory named by word's leading part.
// The typed part is kept as typed; entry names are escaped.
func completePaths(word string, ctx completionContext, dirsOnly bool) []completionCandidate {
	typedDir, base := "", word
	if slash := strings.LastIndexByte(word, '/'); slash >= 0 {
		typedDir, base = word[:slash+1], word[slash+1:]
	}
	root, rawDir := ctx.Directory, typedDir
	if ctx.Home != "" && strings.HasPrefix(rawDir, "~/") {
		root, rawDir = ctx.Home, rawDir[2:]
	}
	directory, ok := expr.LiteralCommandWord(rawDir)
	if !ok {
		return nil
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(root, directory)
	}
	// A basename here is a path component, not a standalone home expansion.
	decoded, ok := expr.LiteralCommandWord("/" + base)
	if !ok {
		return nil
	}
	base = decoded[1:]
	entries, _ := os.ReadDir(directory)
	var candidates []completionCandidate
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, base) || strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		dir := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(filepath.Join(directory, name))
			dir = err == nil && info.IsDir()
		}
		if dirsOnly && !dir {
			continue
		}
		c := completionCandidate{Text: typedDir + completionEscape(name), Display: name, Tag: tagFiles}
		info, err := entry.Info()
		mode := entry.Type()
		if err == nil {
			mode = info.Mode()
		}
		switch {
		case mode&os.ModeSymlink != 0:
			c.Display += "@"
			c.Style = "36"
		case dir:
			c.Style = "34"
		case mode&os.ModeNamedPipe != 0:
			c.Display += "|"
			c.Description, c.Style = "fifo", "33"
		case mode&os.ModeSocket != 0:
			c.Display += "="
			c.Description, c.Style = "socket", "33"
		case mode&os.ModeDevice != 0:
			c.Display += "#"
			c.Description, c.Style = "device", "33"
		case mode.IsRegular() && mode&0o111 != 0:
			c.Display += "*"
			c.Style = "32"
		case !mode.IsRegular():
			c.Description, c.Style = "special", "33"
		}
		if dir {
			c.Text += "/"
			c.Display += "/"
		}
		candidates = append(candidates, c)
	}
	return candidates
}

func completionEscape(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if strings.IndexByte(completionEscapes, name[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(name[i])
	}
	return b.String()
}
