package main

import (
	"fmt"
	"io"
	"kame/program"
	"os"
	"path/filepath"
	"strings"
)

// replShell owns the REPL's session state that Kash statements cannot change:
// the working directory and the child environment. Its commands are
// recognized only as whole single-line entries.
type replShell struct {
	Program      *program.Program
	FollowGrants bool
	previous     string
}

func (s *replShell) prompt() string {
	directory := s.Program.Options.Directory
	home, _ := s.lookup("HOME")
	switch {
	case home != "" && directory == home:
		directory = "~"
	case directory != "/":
		directory = filepath.Base(directory)
	}
	return "kash " + directory + "> "
}

// run executes cd, export, or unset. It reports false for other entries,
// which run as Kash.
func (s *replShell) run(text string, errOut io.Writer) (bool, int) {
	if strings.ContainsRune(text, '\n') {
		return false, 0
	}
	name, rest, _ := strings.Cut(strings.TrimSpace(text), " ")
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
