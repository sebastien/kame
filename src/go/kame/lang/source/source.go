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
	// Expanded text combines authored include segments and is not a file excerpt.
	Expanded bool
}

func clone(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
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

// Borrow creates a source view over caller-owned name and text strings. The
// caller must keep both immutable until the Source is no longer in use.
func Borrow(a mem.Allocator, name string, text string) *Source {
	s := mem.Alloc[Source](a)
	s.Name, s.Text = name, text
	return s
}

func (s *Source) Free(a mem.Allocator) {
	if s == nil {
		return
	}
	mem.FreeString(a, s.Name)
	mem.FreeString(a, s.Text)
	mem.Free(a, s)
}

// Position returns the display position at offset. Out-of-range offsets clamp
// to the source boundary so malformed input still has a renderable location.
func (s *Source) Position(offset int) Position {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s.Text) {
		offset = len(s.Text)
	}
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
		r, width := utf8.DecodeRuneInString(s.Text[i:])
		if width == 0 {
			break
		}
		if i+width > offset {
			break
		}
		column, i = column+displayWidth(r), i+width
	}
	return Position{Line: line, Column: column}
}

// DisplayWidth reports terminal cells for one rune. It intentionally uses a
// stable East-Asian-width subset rather than the host locale so diagnostics are
// reproducible in CI and editors.
func DisplayWidth(r rune) int { return displayWidth(r) }

func displayWidth(r rune) int {
	if (r >= 0x0300 && r <= 0x036f) || (r >= 0x1ab0 && r <= 0x1aff) || (r >= 0x1dc0 && r <= 0x1dff) || (r >= 0x20d0 && r <= 0x20ff) || (r >= 0xfe00 && r <= 0xfe0f) || (r >= 0xfe20 && r <= 0xfe2f) {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a || (r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe10 && r <= 0xfe19) || (r >= 0xfe30 && r <= 0xfe6f) || (r >= 0xff00 && r <= 0xff60) || (r >= 0xffe0 && r <= 0xffe6) || (r >= 0x20000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}

// LineBounds returns byte offsets for the line containing offset. The offset
// is clamped so zero-width EOF diagnostics still have an excerpt when one is
// available.
func (s *Source) LineBounds(offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s.Text) {
		offset = len(s.Text)
	}
	start := offset
	for start > 0 && s.Text[start-1] != '\n' {
		start--
	}
	end := offset
	for end < len(s.Text) && s.Text[end] != '\n' && s.Text[end] != '\r' {
		end++
	}
	return start, end
}
