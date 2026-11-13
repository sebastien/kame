package script

import (
	"kame/internal/mem"
	"kame/lang/source"
	"strings"
)

const emptyKashBody = "empty Kash control body"

// KashInput classifies interactive Kash text before it is executed.
type KashInput int

const (
	// KashComplete text parses, or fails before its end; it can be submitted.
	KashComplete KashInput = iota
	// KashOpen text ends inside an expression, e.g. an unclosed parenthesis.
	KashOpen
	// KashBlock text ends with a control header whose body is still empty.
	KashBlock
)

// ClassifyKashInput reports whether an interactive reader needs more lines.
// Only failures reaching the end of the text count as incomplete, so errors
// earlier in the entry are reported immediately.
func ClassifyKashInput(text string) KashInput {
	if strings.TrimSpace(text) == "" {
		return KashComplete
	}
	// A trailing newline makes an unclosed construct fail at the end.
	s := ParseKash(mem.System, "<input>", text+"\n")
	defer s.Free()
	end := len(strings.TrimRight(text, " \t\r\n"))
	result := KashComplete
	for i := range s.Diagnostics {
		d := s.Diagnostics[i]
		if d.Severity != source.Error {
			continue
		}
		if d.Message == emptyKashBody && d.Span.End >= end {
			return KashBlock
		}
		if d.Span.Start >= end || strings.HasPrefix(d.Message, "unclosed") {
			result = KashOpen
		}
	}
	return result
}
