// Package usage models command-line interfaces for completion: subcommands,
// flags and arguments, read from Usage KDL specs, man pages and fish
// completion files (spec 058).
package usage

import (
	"slices"
	"strings"
)

// Spec describes one command: the executable at the root, or a subcommand.
type Spec struct {
	Name     string   `json:"name,omitempty"`
	Help     string   `json:"help,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
	Flags    []Flag   `json:"flags,omitempty"`
	Args     []Arg    `json:"args,omitempty"`
	Commands []Spec   `json:"commands,omitempty"`
}

// Flag is one option with all its spellings, dashes included. A one-dash
// name longer than one letter is an old-style option matched whole.
type Flag struct {
	Names    []string `json:"names"`
	Help     string   `json:"help,omitempty"`
	Value    string   `json:"value,omitempty"`    // placeholder; empty for a switch
	Optional bool     `json:"optional,omitempty"` // the value can only be attached
	Global   bool     `json:"global,omitempty"`   // inherited by subcommands
	Choices  []string `json:"choices,omitempty"`
	Complete string   `json:"complete,omitempty"` // file, dir, or none
}

// Arg is a positional argument.
type Arg struct {
	Name     string   `json:"name"`
	Help     string   `json:"help,omitempty"`
	Optional bool     `json:"optional,omitempty"`
	Variadic bool     `json:"variadic,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Complete string   `json:"complete,omitempty"`
}

// Merge combines specs in precedence order: a higher spec wins for every
// subcommand, flag and argument it declares; lower specs add what is missing.
func Merge(specs ...Spec) Spec {
	var merged Spec
	for i, spec := range specs {
		if i == 0 {
			merged = clone(spec)
			continue
		}
		mergeInto(&merged, spec)
	}
	return merged
}

func clone(s Spec) Spec {
	s.Aliases = slices.Clone(s.Aliases)
	s.Flags = slices.Clone(s.Flags)
	for i := range s.Flags {
		s.Flags[i].Names = slices.Clone(s.Flags[i].Names)
	}
	s.Args = slices.Clone(s.Args)
	s.Commands = slices.Clone(s.Commands)
	for i := range s.Commands {
		s.Commands[i] = clone(s.Commands[i])
	}
	return s
}

func mergeInto(dst *Spec, src Spec) {
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Help == "" {
		dst.Help = src.Help
	}
	for _, alias := range src.Aliases {
		if !slices.Contains(dst.Aliases, alias) {
			dst.Aliases = append(dst.Aliases, alias)
		}
	}
	for _, flag := range src.Flags {
		i := slices.IndexFunc(dst.Flags, func(f Flag) bool { return overlaps(f.Names, flag.Names) })
		if i < 0 {
			flag.Names = slices.DeleteFunc(slices.Clone(flag.Names), func(name string) bool { return dst.flag(name) != nil })
			if len(flag.Names) != 0 {
				dst.Flags = append(dst.Flags, flag)
			}
			continue
		}
		// The higher flag keeps its meaning; it gains missing spellings and help.
		for _, name := range flag.Names {
			if dst.flag(name) == nil {
				dst.Flags[i].Names = append(dst.Flags[i].Names, name)
			}
		}
		if dst.Flags[i].Help == "" {
			dst.Flags[i].Help = flag.Help
		}
	}
	// A trailing variadic argument already covers every following position.
	if n := len(dst.Args); n < len(src.Args) && (n == 0 || !dst.Args[n-1].Variadic) {
		dst.Args = append(dst.Args, src.Args[n:]...)
	}
	for _, command := range src.Commands {
		if i := slices.IndexFunc(dst.Commands, func(c Spec) bool { return c.Name == command.Name }); i >= 0 {
			mergeInto(&dst.Commands[i], command)
		} else {
			dst.Commands = append(dst.Commands, clone(command))
		}
	}
}

func overlaps(a, b []string) bool {
	return slices.ContainsFunc(a, func(name string) bool { return slices.Contains(b, name) })
}

func (s *Spec) flag(name string) *Flag {
	for i := range s.Flags {
		if slices.Contains(s.Flags[i].Names, name) {
			return &s.Flags[i]
		}
	}
	return nil
}

func (s *Spec) command(name string) *Spec {
	for i := range s.Commands {
		if s.Commands[i].Name == name || slices.Contains(s.Commands[i].Aliases, name) {
			return &s.Commands[i]
		}
	}
	return nil
}

// Files says whether paths may follow the candidates of a position.
type Files int

const (
	NoFiles Files = iota
	AnyFiles
	DirFiles
)

// Candidate is one completion. Text replaces the whole current word.
type Candidate struct {
	Text, Help string
	Kind       string // command, flag, or value
}

// Complete returns the candidates for word, the argument being typed, after
// the arguments words that follow the executable.
func (s *Spec) Complete(words []string, word string) ([]Candidate, Files) {
	cmd, globals := s, []Flag(nil)
	flags := func() []Flag { return append(slices.Clone(cmd.Flags), globals...) }
	find := func(name string) *Flag {
		for _, f := range flags() {
			if slices.Contains(f.Names, name) {
				return &f
			}
		}
		return nil
	}
	var pending *Flag
	positional, dashdash := 0, false
	for _, w := range words {
		switch {
		case pending != nil:
			pending = nil
		case !dashdash && w == "--":
			dashdash = true
		case !dashdash && len(w) > 1 && w[0] == '-':
			if f, attached := lookup(w, find); f != nil && f.Value != "" && !f.Optional && !attached {
				pending = f
			}
		case positional == 0 && !dashdash && cmd.command(w) != nil:
			for _, f := range cmd.Flags {
				if f.Global {
					globals = append(globals, f)
				}
			}
			cmd = cmd.command(w)
		default:
			positional++
		}
	}
	if pending != nil {
		return values(pending.Choices, pending.Complete, word, "")
	}
	if !dashdash && strings.HasPrefix(word, "--") && strings.Contains(word, "=") {
		name, value, _ := strings.Cut(word, "=")
		if f := find(name); f != nil && f.Value != "" {
			return values(f.Choices, f.Complete, value, name+"=")
		}
		return nil, NoFiles
	}
	if !dashdash && strings.HasPrefix(word, "-") {
		var candidates []Candidate
		for _, f := range flags() {
			for _, name := range f.Names {
				if strings.HasPrefix(name, word) && (len(word) < 2 || word[1] != '-' || strings.HasPrefix(name, "--")) {
					candidates = append(candidates, Candidate{Text: name, Help: f.Help, Kind: "flag"})
				}
			}
		}
		return candidates, NoFiles
	}
	var candidates []Candidate
	if positional == 0 && !dashdash {
		for _, c := range cmd.Commands {
			if strings.HasPrefix(c.Name, word) {
				candidates = append(candidates, Candidate{Text: c.Name, Help: c.Help, Kind: "command"})
			}
		}
	}
	if n := len(cmd.Args); n != 0 && (positional < n || cmd.Args[n-1].Variadic) {
		arg := cmd.Args[min(positional, n-1)]
		more, files := values(arg.Choices, arg.Complete, word, "")
		return append(candidates, more...), files
	}
	if len(cmd.Commands) != 0 && positional == 0 {
		return candidates, NoFiles
	}
	return candidates, AnyFiles
}

// lookup finds the flag a typed option word uses and whether its value is
// attached: --name=value, a short cluster ending in a valued flag with text
// after it, or an exact old-style spelling.
func lookup(word string, find func(string) *Flag) (*Flag, bool) {
	if strings.HasPrefix(word, "--") {
		name, _, attached := strings.Cut(word, "=")
		return find(name), attached
	}
	if f := find(word); f != nil {
		return f, false
	}
	var last *Flag
	for i := 1; i < len(word); i++ {
		f := find("-" + word[i:i+1])
		if f == nil {
			return last, false
		}
		if f.Value != "" {
			return f, i+1 < len(word)
		}
		last = f
	}
	return last, false
}

func values(choices []string, complete, word, prefix string) ([]Candidate, Files) {
	var candidates []Candidate
	for _, choice := range choices {
		if strings.HasPrefix(choice, word) {
			candidates = append(candidates, Candidate{Text: prefix + choice, Kind: "value"})
		}
	}
	switch {
	case len(choices) != 0 || complete == "none":
		return candidates, NoFiles
	case complete == "dir":
		return candidates, DirFiles
	}
	return candidates, AnyFiles
}
