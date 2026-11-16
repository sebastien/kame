package main

import (
	"kame/lang/eval"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// completionContext is the session state completion reads. Gathering it
// evaluates nothing.
type completionContext struct {
	Directory string // session directory, absolute
	Home      string // $HOME of the session child environment
	Path      string // $PATH of the session child environment
	Names     []eval.InteractiveName
	Aliases   map[string][]string
}

// completionCandidate is one completion. Text replaces the word being
// completed; directories end with / and take no trailing space.
type completionCandidate struct {
	Text        string // inserted text, escaped
	Display     string // listed text
	Description string
	Tag         string // group heading
}

const (
	completionDelimiters = " \t\n()[]|;&<>=\"'"
	completionEscapes    = " \t\\()[]{}|;&<>\"'$@#*?"
)

// Completion group headings, in listing order.
const (
	tagSession   = "session commands"
	tagKeywords  = "keywords"
	tagCommands  = "commands"
	tagFunctions = "functions"
	tagBindings  = "bindings"
	tagFiles     = "files"
)

var (
	replSessionCommands = map[string]string{"alias": "define or list command aliases", "unalias": "remove command aliases", "cd": "change the session directory", "export": "set child environment variables", "unset": "remove child environment variables", "jobs": "list jobs", "fg": "resume a job in the foreground", "bg": "resume a job in the background"}
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
	i := pos
	for i > 0 {
		if strings.IndexByte(completionDelimiters, line[i-1]) >= 0 && !(i >= 2 && line[i-2] == '\\') {
			break
		}
		i--
	}
	return i
}

func completionCandidates(word, before string, ctx completionContext) []completionCandidate {
	// Only the current line decides the position of a multi-line entry.
	if newline := strings.LastIndexByte(before, '\n'); newline >= 0 {
		before = before[newline+1:]
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
		return completePaths(word, ctx, false)
	}
}

// completionCommandPosition reports whether a word after text, which has no
// trailing blanks, starts a command.
func completionCommandPosition(text string) bool {
	if text == "" || strings.HasSuffix(text, "$(") || strings.IndexByte("|;&", text[len(text)-1]) >= 0 {
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
	var candidates []completionCandidate
	for _, directory := range commandPathDirectories(ctx) {
		if directory == "" {
			continue
		}
		entries, _ := os.ReadDir(directory)
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, word) || strings.HasPrefix(name, ".") && !strings.HasPrefix(word, ".") {
				continue
			}
			if executableFile(filepath.Join(directory, name)) {
				candidates = append(candidates, completionCandidate{Text: completionEscape(name), Display: name, Description: directory, Tag: tagCommands})
			}
		}
	}
	return candidates
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
	directory := completionDirectory(completionUnescape(typedDir), ctx)
	base = completionUnescape(base)
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
		if dir {
			c.Text += "/"
			c.Display += "/"
		}
		candidates = append(candidates, c)
	}
	return candidates
}

// completionDirectory resolves a typed directory from the session directory,
// with a leading ~/ from $HOME.
func completionDirectory(directory string, ctx completionContext) string {
	switch {
	case ctx.Home != "" && (directory == "~" || strings.HasPrefix(directory, "~/")):
		return filepath.Join(ctx.Home, directory[1:])
	case directory == "":
		return ctx.Directory
	case !filepath.IsAbs(directory):
		return filepath.Join(ctx.Directory, directory)
	}
	return directory
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

func completionUnescape(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) {
			i++
		}
		b.WriteByte(text[i])
	}
	return b.String()
}
