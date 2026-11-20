package template

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"

	"strings"
)

type PartKind int

const (
	Literal PartKind = iota
	Expression
	Reference
	Tool
)

type Part struct {
	Kind PartKind
	Span source.Span
	Text string
	Expr *expr.Expr
}

// String is an unquoted standalone or recipe template.
type String struct {
	Alloc       mem.Allocator
	Source      *source.Source
	OwnSource   bool
	Span        source.Span
	Parts       []Part
	Diagnostics []source.Diagnostic
	marker      byte
}

func (t *String) Free() {
	if t == nil {
		return
	}
	for i := range t.Parts {
		if t.Parts[i].Kind == Literal {
			mem.FreeString(t.Alloc, t.Parts[i].Text)
		}
		expr.Free(t.Alloc, t.Parts[i].Expr)
	}
	mem.FreeSlice(nil, t.Parts)
	mem.FreeSlice(nil, t.Diagnostics)
	if t.OwnSource {
		t.Source.Free(t.Alloc)
	}
	a := t.Alloc
	*t = String{}
	mem.Free(a, t)
}

type parser struct {
	a      mem.Allocator
	s      *source.Source
	pos    int
	end    int
	parts  []Part
	diags  []source.Diagnostic
	marker byte
}

func ParseString(a mem.Allocator, name string, text string) *String {
	p := parser{a: a, s: source.New(a, name, text), end: len(text), marker: '@'}
	p.stringParts()
	t := mem.Alloc[String](a)
	t.Alloc, t.Source, t.OwnSource, t.Span, t.Parts, t.Diagnostics = a, p.s, true, source.Span{Start: 0, End: len(text)}, p.parts, p.diags
	return t
}

// ParseStringRange parses a template within a caller-owned Source.
func ParseStringRange(a mem.Allocator, s *source.Source, start int, end int) *String {
	return parseStringRange(a, s, start, end, '@')
}

// ParseSubstitutionRange parses a Kame rule header operand with ${…}.
func ParseSubstitutionRange(a mem.Allocator, s *source.Source, start int, end int) *String {
	return parseStringRange(a, s, start, end, '$')
}

func parseStringRange(a mem.Allocator, s *source.Source, start int, end int, marker byte) *String {
	view := mem.Alloc[source.Source](a)
	*view = *s
	view.Text = s.Text[:end]
	p := parser{a: a, s: view, pos: start, end: end, marker: marker}
	p.stringParts()
	mem.Free(a, view)
	t := mem.Alloc[String](a)
	t.Alloc, t.Span, t.Parts, t.Diagnostics = a, source.Span{Start: start, End: end}, p.parts, p.diags
	t.marker = marker
	return t
}

func (p *parser) diagnostic(severity source.Severity, start int, end int, message string) {
	if end > len(p.s.Text) {
		end = len(p.s.Text)
	}
	p.diags = append(p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: severity, Span: source.Span{Start: start, End: end}, Message: message})
}

func (p *parser) literal(text *strings.Builder, start int, end int) {
	if text.Len() == 0 {
		return
	}
	p.parts = append(p.parts, Part{Kind: Literal, Text: owned(p.a, text.String()), Span: source.Span{Start: start, End: end}})
	text.Reset()
}

func (p *parser) stringParts() {
	var literal strings.Builder
	literal = strings.Builder{}
	literalStart := p.pos
	for p.pos < p.end {
		if p.marker == '$' && p.s.Text[p.pos] == '\\' && p.pos+1 < p.end {
			literal.WriteString(p.s.Text[p.pos : p.pos+2])
			p.pos += 2
			continue
		}
		if p.pos+4 < p.end && p.s.Text[p.pos] == p.marker && p.s.Text[p.pos+1:p.pos+4] == "{x/" {
			end := p.pos + 4
			for end < p.end && (isToolNameByte(p.s.Text[end])) {
				end++
			}
			if end > p.pos+4 && end < p.end && p.s.Text[end] == '}' {
				p.literal(&literal, literalStart, p.pos)
				p.parts = append(p.parts, Part{Kind: Tool, Text: p.s.Text[p.pos+4 : end], Span: source.Span{Start: p.pos, End: end + 1}})
				p.pos, literalStart = end+1, end+1
				continue
			}
		}
		if p.s.Text[p.pos] != p.marker {
			literal.WriteByte(p.s.Text[p.pos])
			p.pos++
			continue
		}
		if p.marker == '@' && p.pos+1 < p.end && p.s.Text[p.pos+1] == '@' {
			literal.WriteByte('@')
			p.pos += 2
			continue
		}
		if p.pos+1 < p.end && p.s.Text[p.pos+1] == '{' {
			start := p.pos
			prefix := expr.ParseExpansionPrefix(p.a, p.s, p.pos)
			closed := prefix.Closed
			if prefix.Expr == nil && closed {
				p.literal(&literal, literalStart, start)
				for i := range prefix.Diagnostics {
					p.diags = append(p.diags, prefix.Diagnostics[i])
				}
				mem.FreeSlice(nil, prefix.Diagnostics)
				p.pos = prefix.End
				p.parts = append(p.parts, Part{Kind: Literal, Text: owned(p.a, p.s.Text[start:p.pos]), Span: source.Span{Start: start, End: p.pos}})
				literalStart = p.pos
				continue
			}
			if prefix.Expr == nil || !closed {
				expr.Free(p.a, prefix.Expr)
				mem.FreeSlice(nil, prefix.Diagnostics)
				p.diagnostic(source.Error, start, start+2, "unclosed template interpolation; expected '}'")
				literal.WriteByte(p.marker)
				p.pos++
				continue
			}
			p.literal(&literal, literalStart, start)
			for i := range prefix.Diagnostics {
				p.diags = append(p.diags, prefix.Diagnostics[i])
			}
			mem.FreeSlice(nil, prefix.Diagnostics)
			p.pos = prefix.End
			p.parts = append(p.parts, Part{Kind: Reference, Expr: prefix.Expr, Span: source.Span{Start: start, End: p.pos}})
			literalStart = p.pos
			continue
		}
		literal.WriteByte(p.marker)
		p.pos++
	}
	p.literal(&literal, literalStart, p.pos)
	literal.Reset()
}

func isToolNameByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func templateSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

// FormatString returns allocator-owned canonical standalone template text.
func FormatString(a mem.Allocator, t *String) string {
	b := strings.Builder{}
	marker := t.marker
	if marker == 0 {
		marker = '@'
	}
	for i := range t.Parts {
		part := t.Parts[i]
		if part.Kind == Literal {
			for j := 0; j < len(part.Text); j++ {
				if marker == '@' && part.Text[j] == '@' {
					b.WriteByte('@')
				}
				b.WriteByte(part.Text[j])
			}
			continue
		}
		b.WriteByte(marker)
		b.WriteByte('{')
		if part.Kind == Tool {
			b.WriteString("x/")
			b.WriteString(part.Text)
			b.WriteByte('}')
			continue
		}
		value := expr.Compact(a, part.Expr)
		b.WriteString(value)
		mem.FreeString(a, value)
		b.WriteByte('}')
	}
	value := owned(a, b.String())
	b.Reset()
	return value
}
