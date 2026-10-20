package definition

import "solod.dev/so/strings"

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
