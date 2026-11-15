package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// completionContext is the session state completion reads. Gathering it
// evaluates nothing.
type completionContext struct {
	Directory string   // session directory, absolute
	Home      string   // $HOME of the session child environment
	Path      string   // $PATH of the session child environment
	Callables []string // session functions and library operations
	Values    []string // other top-level bindings
}

type completionCandidate struct {
	Text    string // inserted text, escaped
	Display string // listed text
	Dir     bool   // inserted without a trailing space
}

const (
	completionDelimiters = " \t()[]|;&<>=\"'"
	completionEscapes    = " \t\\()[]{}|;&<>\"'$@#*?"
	completionListLimit  = 100
)

var (
	replSessionCommands = []string{"cd", "export", "unset"}
	replKeywords        = []string{"def", "defer", "elif", "else", "exit", "for", "if", "let", "match", "return", "while", "with"}
)

// completeLine completes the word before pos. It returns the new line and
// cursor, and the candidates to list when the word cannot be extended.
func completeLine(line string, pos int, ctx completionContext) (string, int, []string) {
	start := completionWordStart(line, pos)
	word, before := line[start:pos], line[:start]
	candidates := completionCandidates(word, before, ctx)
	slices.SortFunc(candidates, func(a, b completionCandidate) int { return strings.Compare(a.Text, b.Text) })
	candidates = slices.CompactFunc(candidates, func(a, b completionCandidate) bool { return a.Text == b.Text })
	replace := func(text string) (string, int, []string) {
		return line[:start] + text + line[pos:], start + len(text), nil
	}
	switch len(candidates) {
	case 0:
		return line, pos, nil
	case 1:
		text := candidates[0].Text
		if !candidates[0].Dir {
			text += " "
		}
		return replace(text)
	}
	prefix := candidates[0].Text
	for _, c := range candidates[1:] {
		n := 0
		for n < len(prefix) && n < len(c.Text) && prefix[n] == c.Text[n] {
			n++
		}
		prefix = prefix[:n]
	}
	if len(prefix) > len(word) && strings.HasPrefix(prefix, word) {
		return replace(prefix)
	}
	list := make([]string, len(candidates))
	for i, c := range candidates {
		list[i] = c.Display
	}
	return line, pos, list
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
	trimmed := strings.TrimRight(before, " \t")
	switch {
	case strings.HasPrefix(word, "$"):
		return completeNames(word, "$", ctx.Values)
	case strings.HasSuffix(before, "(") && !strings.HasSuffix(before, "$("):
		return completeNames(word, "", ctx.Callables)
	case completionCommandPosition(trimmed) && !strings.Contains(word, "/"):
		candidates := completeNames(word, "", replSessionCommands)
		candidates = append(candidates, completeNames(word, "", replKeywords)...)
		return append(candidates, completeExecutables(word, ctx.Path)...)
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

func completeNames(word, prefix string, names []string) []completionCandidate {
	var candidates []completionCandidate
	for _, name := range names {
		if text := prefix + name; strings.HasPrefix(text, word) {
			candidates = append(candidates, completionCandidate{Text: text, Display: text})
		}
	}
	return candidates
}

func completeExecutables(word, path string) []completionCandidate {
	var candidates []completionCandidate
	for _, directory := range filepath.SplitList(path) {
		if directory == "" {
			continue
		}
		entries, _ := os.ReadDir(directory)
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, word) || strings.HasPrefix(name, ".") && !strings.HasPrefix(word, ".") {
				continue
			}
			if info, err := os.Stat(filepath.Join(directory, name)); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
				escaped := completionEscape(name)
				candidates = append(candidates, completionCandidate{Text: escaped, Display: name})
			}
		}
	}
	return candidates
}

// completePaths lists entries of the directory named by word's leading part.
// The typed part is kept as typed; entry names are escaped.
func completePaths(word string, ctx completionContext, dirsOnly bool) []completionCandidate {
	typedDir, base := "", word
	if slash := strings.LastIndexByte(word, '/'); slash >= 0 {
		typedDir, base = word[:slash+1], word[slash+1:]
	}
	directory := completionUnescape(typedDir)
	switch {
	case ctx.Home != "" && strings.HasPrefix(directory, "~/"):
		directory = filepath.Join(ctx.Home, directory[2:])
	case directory == "":
		directory = ctx.Directory
	case !filepath.IsAbs(directory):
		directory = filepath.Join(ctx.Directory, directory)
	}
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
		c := completionCandidate{Text: typedDir + completionEscape(name), Display: name, Dir: dir}
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

// formatCompletionList lays candidates out in columns fitting width, at most
// completionListLimit of them, followed by a count of the rest.
func formatCompletionList(list []string, width int) string {
	rest := 0
	if len(list) > completionListLimit {
		list, rest = list[:completionListLimit], len(list)-completionListLimit
	}
	column := 0
	for _, item := range list {
		column = max(column, len(item)+2)
	}
	columns := max(1, width/column)
	rows := (len(list) + columns - 1) / columns
	var b strings.Builder
	for row := 0; row < rows; row++ {
		for col := 0; col < columns; col++ {
			i := col*rows + row
			if i >= len(list) {
				break
			}
			b.WriteString(list[i])
			if col+1 < columns && i+rows < len(list) {
				b.WriteString(strings.Repeat(" ", column-len(list[i])))
			}
		}
		b.WriteByte('\n')
	}
	if rest != 0 {
		b.WriteString("... and " + strconv.Itoa(rest) + " more\n")
	}
	return b.String()
}
