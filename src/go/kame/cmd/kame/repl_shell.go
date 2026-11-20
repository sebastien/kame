package main

import (
	"fmt"
	"io"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/program"
	"path/filepath"
	"slices"
	"strings"
)

// replShell owns whole-entry aliases and the session's presentation context.
type replShell struct {
	Session *program.Session
	aliases map[string][]string
}

// prompt is the default prompt.
func (s *replShell) prompt() string { return "kash " + s.directoryName() + "> " }

// directoryName displays the session directory: ~ for home, / for the root,
// otherwise its base name.
func (s *replShell) directoryName() string {
	directory := s.Session.Directory()
	home, _ := s.lookup("HOME")
	switch {
	case home != "" && directory == home:
		return "~"
	case directory != "/":
		return filepath.Base(directory)
	}
	return directory
}

// run handles whole-entry alias declarations and removals.
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
	return false, 0
}

func replCommandAlias(state any, name string) []string {
	return state.(*replShell).aliases[name]
}

func replSessionTool(state any, a mem.Allocator, name, cwd string) string {
	s := state.(*replShell)
	prefix := name + "="
	for i := len(s.Session.Program.Options.ToolOverrides) - 1; i >= 0; i-- {
		if strings.HasPrefix(s.Session.Program.Options.ToolOverrides[i], prefix) {
			name = s.Session.Program.Options.ToolOverrides[i][len(prefix):]
			break
		}
	}
	environment := s.Session.Program.Options.Environment
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
	parsed := script.ParseKashSessionText(mem.System, "<alias>", command+" "+text)
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
					fmt.Fprint(out, ", ")
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

func (s *replShell) lookup(key string) (string, bool) {
	return s.Session.EnvironmentValue(key)
}
