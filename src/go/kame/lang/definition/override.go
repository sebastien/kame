package definition

import "strings"

// Override is a borrowed view of normalized configuration. NAME=TEXT remains
// literal text; NAME:=LITERAL is the internal spelling of --set.
// Keeping both in one ordered list preserves precedence across the two options
// and across native, WASM and embedding configuration paths.
type Override struct {
	Name  string
	Text  string
	Typed bool
}

func ParseOverride(assignment string) Override {
	equal := strings.IndexByte(assignment, '=')
	if equal <= 0 {
		return Override{}
	}
	end := equal
	typed := assignment[end-1] == ':'
	if typed {
		end--
	}
	if !ValidName(assignment[:end]) {
		return Override{}
	}
	return Override{Name: assignment[:end], Text: assignment[equal+1:], Typed: typed}
}

// SelectOverride resolves one value definition using the invocation's precedence.
func SelectOverride(name string, defines, environment []string) Override {
	var selected Override
	prefix := "KAME_" + name + "="
	for _, entry := range environment {
		if text, ok := strings.CutPrefix(entry, prefix); ok {
			selected = Override{Name: name, Text: text}
		}
	}
	for _, entry := range defines {
		if override := ParseOverride(entry); override.Name == name {
			selected = override
		}
	}
	return selected
}
