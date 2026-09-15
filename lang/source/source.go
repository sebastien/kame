// Package source owns source text and maps byte spans to display positions.
package source

import (
	"solod.dev/so/mem"
	"solod.dev/so/unicode/utf8"
)

// Span is a zero-based, half-open byte range in one Source.
type Span struct {
	Start int
	End   int
}

// Position is one-based for diagnostic display.
type Position struct {
	Line   int
	Column int
}

type Severity int

const (
	Warning Severity = iota
	Error
)

// Diagnostic refers to the Source owned by its parse result.
type Diagnostic struct {
	Code     string
	Severity Severity
	Span     Span
	Message  string
}

// Source owns its name and UTF-8 text until Free.
type Source struct {
	Name string
	Text string
}

func clone(a mem.Allocator, text string) string {
	if len(text) == 0 { return "" }
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

func New(a mem.Allocator, name string, text string) *Source {
	s := mem.Alloc[Source](a)
	s.Name = clone(a, name)
	s.Text = clone(a, text)
	return s
}

func (s *Source) Free(a mem.Allocator) {
	if s == nil { return }
	mem.FreeString(a, s.Name)
	mem.FreeString(a, s.Text)
	mem.Free(a, s)
}

// Position returns the display position at offset. Out-of-range offsets clamp
// to the source boundary so malformed input still has a renderable location.
func (s *Source) Position(offset int) Position {
	if offset < 0 { offset = 0 }
	if offset > len(s.Text) { offset = len(s.Text) }
	line, column, i := 1, 1, 0
	for i < offset {
		b := s.Text[i]
		if b == '\n' {
			line, column, i = line+1, 1, i+1
			continue
		}
		if b == '\r' && i+1 < len(s.Text) && s.Text[i+1] == '\n' {
			i++
			continue
		}
		if b == '\t' {
			column += 8 - (column-1)%8
			i++
			continue
		}
		_, width := utf8.DecodeRuneInString(s.Text[i:])
		if width == 0 { break }
		if i+width > offset { break }
		column, i = column+1, i+width
	}
	return Position{Line: line, Column: column}
}
