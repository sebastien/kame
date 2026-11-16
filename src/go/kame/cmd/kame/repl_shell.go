package main

import (
	"fmt"
	"io"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/program"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// replShell owns the REPL's session state that Kash statements cannot change:
// the working directory and the child environment. Its commands are
// recognized only as whole single-line entries.
type replShell struct {
	Program      *program.Program
	FollowGrants bool
	previous     string
	aliases      map[string][]string
}

// prompt is the default prompt.
func (s *replShell) prompt() string { return "kash " + s.directoryName() + "> " }

// directoryName displays the session directory: ~ for home, / for the root,
// otherwise its base name.
func (s *replShell) directoryName() string {
	directory := s.Program.Options.Directory
	home, _ := s.lookup("HOME")
	switch {
	case home != "" && directory == home:
		return "~"
	case directory != "/":
		return filepath.Base(directory)
	}
	return directory
}

// run handles aliases and whole-line directory/environment commands.
// Other entries run as Kash.
func (s *replShell) run(text string, out, errOut io.Writer) (bool, int) {
	trimmed := strings.TrimSpace(text)
	end := strings.IndexAny(trimmed, " \t\r\n")
	if end < 0 {
		end = len(trimmed)
	}
	name, rest := trimmed[:end], strings.TrimSpace(trimmed[end:])
	if name == "alias" || name == "unalias" {
		return true, s.alias(name, rest, out, errOut)
	}
	if strings.ContainsRune(text, '\n') {
		return false, 0
	}
	prefix := s.aliases[name]
	if len(prefix) != 0 {
		name = prefix[0]
	}
	if name != "cd" && name != "export" && name != "unset" {
		return false, 0
	}
	words, err := replWords(rest, func(key string) string {
		value, _ := s.lookup(key)
		return value
	})
	if err != nil {
		fmt.Fprintf(errOut, "%s: %v\n", name, err)
		return true, 2
	}
	if len(prefix) != 0 {
		words = append(slices.Clone(prefix[1:]), words...)
	}
	switch name {
	case "cd":
		return true, s.cd(words, errOut)
	case "export":
		return true, s.export(words, errOut)
	default:
		for _, key := range words {
			s.Program.SetEnvironment(key, "", false)
		}
		return true, 0
	}
}

func replCommandAlias(state any, name string) []string {
	return state.(*replShell).aliases[name]
}

func replSessionTool(state any, a mem.Allocator, name, cwd string) string {
	s := state.(*replShell)
	prefix := name + "="
	for i := len(s.Program.Options.ToolOverrides) - 1; i >= 0; i-- {
		if strings.HasPrefix(s.Program.Options.ToolOverrides[i], prefix) {
			name = s.Program.Options.ToolOverrides[i][len(prefix):]
			break
		}
	}
	environment := s.Program.Options.Environment
	if environment == nil {
		environment = []string{}
	}
	return posix.ResolveTool(a, name, cwd, environment)
}

// alias accepts closed string lists, never executable expressions or shell text.
func (s *replShell) alias(command, text string, out, errOut io.Writer) int {
	fail := func(message string) int {
		fmt.Fprintf(errOut, "%s: %s\n", command, message)
		return 2
	}
	parsed := script.ParseKash(mem.System, "<alias>", command+" "+text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 {
		return fail(parsed.Diagnostics[0].Message)
	}
	var declaration *script.ScriptItem
	for i := range parsed.Items {
		if parsed.Items[i].Kind == script.Comment {
			continue
		}
		if declaration != nil || parsed.Items[i].Kind != script.SessionCommand {
			return fail("expected one whole session entry")
		}
		declaration = &parsed.Items[i]
	}
	if declaration == nil {
		return fail("expected one whole session entry")
	}
	words := strings.Fields(declaration.Text)
	if command == "unalias" {
		names := words[1:]
		for _, name := range names {
			if _, ok := s.aliases[name]; !ok {
				return fail("unknown alias: " + name)
			}
		}
		for _, name := range names {
			delete(s.aliases, name)
		}
		return 0
	}
	name := ""
	if len(words) == 2 {
		name = words[1]
	}
	if declaration.Expression == nil {
		names := []string{name}
		if name == "" {
			names = make([]string, 0, len(s.aliases))
			for key := range s.aliases {
				names = append(names, key)
			}
			slices.Sort(names)
		} else if _, ok := s.aliases[name]; !ok {
			return fail("unknown alias: " + name)
		}
		for _, key := range names {
			fmt.Fprintf(out, "alias %s = [", key)
			for i, argument := range s.aliases[key] {
				if i != 0 {
					fmt.Fprint(out, " ")
				}
				fmt.Fprint(out, kameString(argument))
			}
			fmt.Fprintln(out, "]")
		}
		return 0
	}
	if !validAliasName(name) {
		return fail("expected an unreserved command name")
	}
	var prefix []string
	for _, item := range declaration.Expression.Items {
		if item.Kind != expr.String {
			return fail("expected literal strings without NUL bytes")
		}
		var argument strings.Builder
		for _, part := range item.Parts {
			if part.Expr != nil || strings.ContainsRune(part.Text, 0) {
				return fail("expected literal strings without NUL bytes")
			}
			argument.WriteString(part.Text)
		}
		prefix = append(prefix, argument.String())
	}
	if prefix[0] == "" {
		return fail("expected a nonempty executable")
	}
	if s.aliases == nil {
		s.aliases = map[string][]string{}
	}
	s.aliases[name] = prefix
	return 0
}

func validAliasName(name string) bool {
	if name == "" || replSessionCommands[name] != "" || name == "kill" {
		return false
	}
	for _, keyword := range replKeywords {
		if name == keyword {
			return false
		}
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.", r)) {
			return false
		}
	}
	return name[0] != '-'
}

func (s *replShell) cd(words []string, errOut io.Writer) int {
	if len(words) > 1 {
		fmt.Fprintln(errOut, "cd: expected one directory")
		return 2
	}
	current := s.Program.Options.Directory
	target, _ := s.lookup("HOME")
	if len(words) == 1 {
		target = words[0]
	}
	if target == "-" {
		if s.previous == "" {
			fmt.Fprintln(errOut, "cd: no previous directory")
			return 1
		}
		target = s.previous
	}
	if home, _ := s.lookup("HOME"); home != "" && (target == "~" || strings.HasPrefix(target, "~/")) {
		target = home + target[1:]
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(current, target)
	}
	target = filepath.Clean(target)
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		fmt.Fprintf(errOut, "cd: not a directory: %s\n", target)
		return 1
	}
	// Kame's own cwd follows, so host paths it resolves stay consistent.
	if err := os.Chdir(target); err != nil {
		fmt.Fprintf(errOut, "cd: %v\n", err)
		return 1
	}
	s.previous = current
	s.Program.SetDirectory(target, s.FollowGrants)
	return 0
}

func (s *replShell) export(words []string, errOut io.Writer) int {
	if len(words) == 0 {
		fmt.Fprintln(errOut, "export: expected NAME=VALUE")
		return 2
	}
	// Validate every operand first, so a malformed one changes nothing.
	for _, word := range words {
		if key, _, ok := strings.Cut(word, "="); !ok || !validEnvironmentName(key) {
			fmt.Fprintf(errOut, "export: expected NAME=VALUE: %s\n", word)
			return 2
		}
	}
	for _, word := range words {
		key, value, _ := strings.Cut(word, "=")
		s.Program.SetEnvironment(key, value, true)
	}
	return 0
}

func (s *replShell) lookup(key string) (string, bool) {
	environment := s.Program.Options.Environment
	for i := len(environment) - 1; i >= 0; i-- {
		if name, value, ok := strings.Cut(environment[i], "="); ok && name == key {
			return value, true
		}
	}
	return "", false
}

func validEnvironmentName(name string) bool {
	for i, r := range name {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return name != ""
}

// replWords splits on blanks. Single quotes are literal; unquoted and
// double-quoted text expand $NAME and ${NAME} through expand.
func replWords(text string, expand func(string) string) ([]string, error) {
	var words []string
	var word, pending strings.Builder
	inWord := false
	var quote rune
	flush := func() {
		word.WriteString(os.Expand(pending.String(), expand))
		pending.Reset()
	}
	for _, r := range text {
		switch {
		case quote == '\'' && r == '\'':
			quote = 0
		case quote == '\'':
			word.WriteRune(r)
		case quote == '"' && r == '"':
			quote = 0
		case quote == '"':
			pending.WriteRune(r)
		case r == '\'':
			flush()
			quote, inWord = r, true
		case r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				flush()
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			pending.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote")
	}
	if inWord {
		flush()
		words = append(words, word.String())
	}
	return words, nil
}
