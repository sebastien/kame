// Package template parses Kame string and target templates.
package template

import (
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type PartKind int

const (
	Literal PartKind = iota
	Expression
	Reference
	Selector
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
	slices.Free(t.Alloc, t.Parts)
	slices.Free(t.Alloc, t.Diagnostics)
	if t.OwnSource {
		t.Source.Free(t.Alloc)
	}
	a := t.Alloc
	*t = String{}
	mem.Free(a, t)
}

type parser struct {
	a     mem.Allocator
	s     *source.Source
	pos   int
	end   int
	parts []Part
	diags []source.Diagnostic
}

func ParseString(a mem.Allocator, name string, text string) *String {
	p := parser{a: a, s: source.New(a, name, text), end: len(text)}
	p.stringParts()
	t := mem.Alloc[String](a)
	t.Alloc, t.Source, t.OwnSource, t.Span, t.Parts, t.Diagnostics = a, p.s, true, source.Span{Start: 0, End: len(text)}, p.parts, p.diags
	return t
}

// ParseStringRange parses a template within a caller-owned Source.
func ParseStringRange(a mem.Allocator, s *source.Source, start int, end int) *String {
	p := parser{a: a, s: s, pos: start, end: end}
	p.stringParts()
	t := mem.Alloc[String](a)
	t.Alloc, t.Span, t.Parts, t.Diagnostics = a, source.Span{Start: start, End: end}, p.parts, p.diags
	return t
}

func (p *parser) diagnostic(severity source.Severity, start int, end int, message string) {
	if end > len(p.s.Text) {
		end = len(p.s.Text)
	}
	p.diags = slices.Append(p.a, p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: severity, Span: source.Span{Start: start, End: end}, Message: message})
}

func (p *parser) literal(text *strings.Builder, start int, end int) {
	if text.Len() == 0 {
		return
	}
	p.parts = slices.Append(p.a, p.parts, Part{Kind: Literal, Text: owned(p.a, text.String()), Span: source.Span{Start: start, End: end}})
	text.Reset()
}

func (p *parser) stringParts() {
	var literal strings.Builder
	literal = strings.NewBuilder(p.a)
	literalStart := p.pos
	for p.pos < p.end {
		if p.pos+4 < p.end && p.s.Text[p.pos:p.pos+4] == "@(x/" {
			end := p.pos + 4
			for end < p.end && (isToolNameByte(p.s.Text[end])) { end++ }
			if end > p.pos+4 && end < p.end && p.s.Text[end] == ')' {
				p.literal(&literal, literalStart, p.pos)
				p.parts = slices.Append(p.a, p.parts, Part{Kind: Tool, Text: p.s.Text[p.pos+4:end], Span: source.Span{Start: p.pos, End: end+1}})
				p.pos, literalStart = end+1, end+1
				continue
			}
		}
		if p.s.Text[p.pos] == '\\' && p.pos+1 < p.end && (p.s.Text[p.pos+1] == '@' || p.s.Text[p.pos+1] == '\\') {
			literal.WriteByte(p.s.Text[p.pos+1])
			p.pos += 2
			continue
		}
		if p.s.Text[p.pos] != '@' {
			literal.WriteByte(p.s.Text[p.pos])
			p.pos++
			continue
		}
		if p.pos+1 < p.end && (p.s.Text[p.pos+1] == '(' || p.s.Text[p.pos+1] == '{') {
			start, close := p.pos, byte(')')
			kind := Expression
			if p.s.Text[p.pos+1] == '{' {
				close, kind = '}', Reference
			}
			prefix := p.embeddedExpression(p.pos+2, close)
			closed := prefix.End < p.end && p.s.Text[prefix.End] == close
			if prefix.Expr == nil && closed {
				p.literal(&literal, literalStart, start)
				for i := range prefix.Diagnostics {
					p.diags = slices.Append(p.a, p.diags, prefix.Diagnostics[i])
				}
				slices.Free(p.a, prefix.Diagnostics)
				p.pos = prefix.End + 1
				p.parts = slices.Append(p.a, p.parts, Part{Kind: Literal, Text: owned(p.a, p.s.Text[start:p.pos]), Span: source.Span{Start: start, End: p.pos}})
				literalStart = p.pos
				continue
			}
			if prefix.Expr == nil || !closed {
				expr.Free(p.a, prefix.Expr)
				slices.Free(p.a, prefix.Diagnostics)
				p.diagnostic(source.Warning, start, start+2, "malformed template expansion")
				literal.WriteByte('@')
				p.pos++
				continue
			}
			p.literal(&literal, literalStart, start)
			for i := range prefix.Diagnostics {
				p.diags = slices.Append(p.a, p.diags, prefix.Diagnostics[i])
			}
			slices.Free(p.a, prefix.Diagnostics)
			if kind == Reference && prefix.Expr.Kind != expr.Name && prefix.Expr.Kind != expr.Reference {
				p.diagnostic(source.Error, p.pos+2, prefix.End, "expected reference")
			}
			p.pos = prefix.End + 1
			p.parts = slices.Append(p.a, p.parts, Part{Kind: kind, Expr: prefix.Expr, Span: source.Span{Start: start, End: p.pos}})
			literalStart = p.pos
			continue
		}
		end := selectorEnd(p.s.Text, p.pos)
		if end > p.pos && expr.ValidSelector(p.s.Text[p.pos:end]) {
			p.literal(&literal, literalStart, p.pos)
			p.parts = slices.Append(p.a, p.parts, Part{Kind: Selector, Text: p.s.Text[p.pos:end], Span: source.Span{Start: p.pos, End: end}})
			p.pos, literalStart = end, end
			continue
		}
		literal.WriteByte('@')
		p.pos++
	}
	p.literal(&literal, literalStart, p.pos)
	literal.Free()
}

func isToolNameByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func (p *parser) embeddedExpression(start int, close byte) expr.Prefix {
	prefix := expr.ParsePrefix(p.a, p.s, start)
	if prefix.Expr == nil {
		return prefix
	}
	items := slices.Append(p.a, []*expr.Expr(nil), prefix.Expr)
	for prefix.End < p.end && p.s.Text[prefix.End] != close {
		if !templateSpace(p.s.Text[prefix.End]) {
			break
		}
		pos := prefix.End
		for pos < p.end && templateSpace(p.s.Text[pos]) {
			pos++
		}
		if pos == p.end || p.s.Text[pos] == close {
			prefix.End = pos
			break
		}
		next := expr.ParsePrefix(p.a, p.s, pos)
		for i := range next.Diagnostics {
			prefix.Diagnostics = slices.Append(p.a, prefix.Diagnostics, next.Diagnostics[i])
		}
		slices.Free(p.a, next.Diagnostics)
		if next.Expr == nil {
			break
		}
		items = slices.Append(p.a, items, next.Expr)
		prefix.End = next.End
	}
	if len(items) == 1 {
		slices.Free(p.a, items)
		return prefix
	}
	application := mem.Alloc[expr.Expr](p.a)
	application.Kind = expr.Application
	application.Span = source.Span{Start: start, End: prefix.End}
	application.Items = items
	prefix.Expr = application
	return prefix
}

func templateSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

func selectorEnd(text string, start int) int {
	if start+1 >= len(text) {
		return start
	}
	b := text[start+1]
	if b == '*' || b == '#' || b == '_' {
		return start + 2
	}
	if b != '<' && b != '>' && !isDigit(b) && b != '-' {
		return start
	}
	i := start + 2
	if b == '<' || b == '>' {
		if i < len(text) && (text[i] == '*' || text[i] == '#' || (b == '<' && text[i] == '?')) {
			return i + 1
		}
	}
	for i < len(text) && (isDigit(text[i]) || text[i] == '-' || text[i] == '.') {
		i++
	}
	return i
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

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
	b := strings.NewBuilder(a)
	for i := range t.Parts {
		part := t.Parts[i]
		if part.Kind == Literal {
			for j := 0; j < len(part.Text); j++ {
				if part.Text[j] == '@' || part.Text[j] == '\\' {
					b.WriteByte('\\')
				}
				b.WriteByte(part.Text[j])
			}
			continue
		}
		if part.Kind == Selector {
			b.WriteString(part.Text)
			continue
		}
		if part.Kind == Tool {
			b.WriteString("@(x/")
			b.WriteString(part.Text)
			b.WriteByte(')')
			continue
		}
		if part.Kind == Expression {
			b.WriteString("@(")
		} else {
			b.WriteString("@{")
		}
		value := expr.Format(a, part.Expr)
		b.WriteString(value)
		mem.FreeString(a, value)
		if part.Kind == Expression {
			b.WriteByte(')')
		} else {
			b.WriteByte('}')
		}
	}
	value := owned(a, b.String())
	b.Free()
	return value
}
