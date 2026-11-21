package main

import (
	"fmt"
	"kame/core"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/script"
	"kame/program"
	"path/filepath"
	"slices"
	"strings"
)

// replShell owns whole-entry aliases and the session's presentation context.
type replShell struct {
	Session *program.Session
	aliases map[string][]core.Value
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

func replCommandAlias(state any, name string) []core.Value {
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

func (s *replSession) alias(command, sourceName, text, sourceText string) (int, bool) {
	run := s.run
	fail := func(message string) (int, bool) {
		fmt.Fprintf(run.Err, "%s: %s\n", command, message)
		return 2, false
	}
	parsed := script.ParseKashSessionText(run.Session.Program.Alloc, sourceName, sourceText)
	defer func() {
		if parsed != nil {
			parsed.Free()
		}
	}()
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
	aliases := s.shell.aliases
	if command == "unalias" {
		names := words[1:]
		for _, name := range names {
			if _, ok := aliases[name]; !ok {
				return fail("unknown alias: " + name)
			}
		}
		for _, name := range names {
			delete(aliases, name)
		}
		return 0, false
	}
	name := ""
	if len(words) == 2 {
		name = words[1]
	}
	if declaration.Expression == nil {
		names := []string{name}
		if name == "" {
			names = make([]string, 0, len(aliases))
			for key := range aliases {
				names = append(names, key)
			}
			slices.Sort(names)
		} else if _, ok := aliases[name]; !ok {
			return fail("unknown alias: " + name)
		}
		for _, key := range names {
			fmt.Fprintf(run.Out, "alias %s = [", key)
			for i, argument := range aliases[key] {
				if i != 0 {
					fmt.Fprint(run.Out, ", ")
				}
				fmt.Fprint(run.Out, kameString(argument.Text))
			}
			fmt.Fprintln(run.Out, "]")
		}
		return 0, false
	}
	if !validAliasName(name) {
		return fail("expected an unreserved command name")
	}
	declaration.Kind = script.Expression
	first, diagnostics := run.Session.AppendKashScript(parsed)
	parsed = nil
	if s.reportDiagnostics(diagnostics) {
		return 1, false
	}
	run.Session.Work[first].Display = false
	job := s.newJob(text, first, false)
	job.onValue = func(value core.Value) int {
		if message := s.shell.setAlias(name, value); message != "" {
			status, _ := fail(message)
			return status
		}
		return 0
	}
	return s.foreground(job, false)
}

func (s *replShell) setAlias(name string, value core.Value) string {
	if value.Kind == core.Nil {
		delete(s.aliases, name)
		return ""
	}
	prefix := value.List
	if value.Kind == core.String {
		prefix = []core.Value{value}
	}
	if len(prefix) == 0 {
		return "expected nil, a string, or a nonempty list of strings"
	}
	for _, argument := range prefix {
		if argument.Kind != core.String || strings.ContainsRune(argument.Text, 0) {
			return "expected strings without NUL bytes"
		}
	}
	if prefix[0].Text == "" {
		return "expected a nonempty executable"
	}
	if s.aliases == nil {
		s.aliases = map[string][]core.Value{}
	}
	stored := make([]core.Value, len(prefix))
	for i, argument := range prefix {
		stored[i] = argument.Clone(mem.System)
	}
	s.aliases[name] = stored
	return ""
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
