// Package template parses LittleMake string and target templates.
package template

import (
	"littlemake/lang/expr"
	"littlemake/lang/source"
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
			prefix := p.embeddedExpression(p.pos + 2, close)
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

func (p *parser) embeddedExpression(start int, close byte) expr.Prefix {
	prefix := expr.ParsePrefix(p.a, p.s, start)
	if prefix.Expr == nil { return prefix }
	items := slices.Append(p.a, []*expr.Expr(nil), prefix.Expr)
	for prefix.End < p.end && p.s.Text[prefix.End] != close {
		if !templateSpace(p.s.Text[prefix.End]) { break }
		pos := prefix.End
		for pos < p.end && templateSpace(p.s.Text[pos]) { pos++ }
		if pos == p.end || p.s.Text[pos] == close { prefix.End = pos; break }
		next := expr.ParsePrefix(p.a, p.s, pos)
		for i := range next.Diagnostics { prefix.Diagnostics = slices.Append(p.a, prefix.Diagnostics, next.Diagnostics[i]) }
		slices.Free(p.a, next.Diagnostics)
		if next.Expr == nil { break }
		items = slices.Append(p.a, items, next.Expr)
		prefix.End = next.End
	}
	if len(items) == 1 { slices.Free(p.a, items); return prefix }
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
		if i < len(text) && (text[i] == '*' || text[i] == '#') {
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

type TargetPartKind int

const (
	TargetLiteral TargetPartKind = iota
	Capture
)

type TargetPart struct {
	Kind    TargetPartKind
	Text    string
	Name    string
	Pattern string
	Span    source.Span
}

type Target struct {
	Alloc       mem.Allocator
	Source      *source.Source
	Parts       []TargetPart
	Diagnostics []source.Diagnostic
}

func (t *Target) Free() {
	if t == nil {
		return
	}
	for i := range t.Parts {
		if t.Parts[i].Kind == TargetLiteral {
			mem.FreeString(t.Alloc, t.Parts[i].Text)
		}
	}
	slices.Free(t.Alloc, t.Parts)
	slices.Free(t.Alloc, t.Diagnostics)
	t.Source.Free(t.Alloc)
	a := t.Alloc
	*t = Target{}
	mem.Free(a, t)
}

func ParseTarget(a mem.Allocator, name string, text string) *Target {
	s := source.New(a, name, text)
	t := mem.Alloc[Target](a)
	t.Alloc, t.Source = a, s
	start, pos := 0, 0
	for pos < len(text) {
		if text[pos] == '\\' && pos+1 < len(text) {
			pos += 2
			continue
		}
		if text[pos] != '{' {
			pos++
			continue
		}
		if start < pos {
			t.Parts = slices.Append(a, t.Parts, TargetPart{Kind: TargetLiteral, Text: targetLiteral(a, text[start:pos]), Span: source.Span{Start: start, End: pos}})
		}
		groupStart := pos
		pos++
		nameStart := pos
		for pos < len(text) && isCaptureContinue(text[pos]) {
			pos++
		}
		capture := text[nameStart:pos]
		if !validCapture(capture) {
			t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(groupStart, pos, "invalid capture name"))
		}
		pattern := "*"
		if pos < len(text) && text[pos] == ':' {
			pos++
			patternStart := pos
			for pos < len(text) && text[pos] != '}' {
				pos++
			}
			pattern = text[patternStart:pos]
			if !validPattern(pattern) {
				t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(patternStart, pos, "invalid capture pattern"))
			}
		}
		if pos == len(text) || text[pos] != '}' {
			t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(groupStart, pos, "unclosed capture group"))
			start = groupStart
			break
		}
		pos++
		t.Parts = slices.Append(a, t.Parts, TargetPart{Kind: Capture, Name: capture, Pattern: pattern, Span: source.Span{Start: groupStart, End: pos}})
		start = pos
	}
	if start < len(text) {
		t.Parts = slices.Append(a, t.Parts, TargetPart{Kind: TargetLiteral, Text: targetLiteral(a, text[start:]), Span: source.Span{Start: start, End: len(text)}})
	}
	return t
}

func targetLiteral(a mem.Allocator, text string) string {
	var out strings.Builder
	out = strings.NewBuilder(a)
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) {
			i++
			out.WriteByte(text[i])
		} else {
			out.WriteByte(text[i])
		}
	}
	value := owned(a, out.String())
	out.Free()
	return value
}

func parseDiagnostic(start int, end int, message string) source.Diagnostic {
	return source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message}
}

func validCapture(name string) bool {
	if len(name) == 0 || !isCaptureStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isCaptureContinue(name[i]) {
			return false
		}
	}
	return true
}

func isCaptureStart(b byte) bool    { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func isCaptureContinue(b byte) bool { return isCaptureStart(b) || isDigit(b) || b == '-' }

func validPattern(pattern string) bool {
	if len(pattern) == 0 {
		return false
	}
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			if i == len(pattern) {
				return false
			}
			continue
		}
		if pattern[i] == '{' || pattern[i] == '}' || pattern[i] == ':' {
			return false
		}
		if pattern[i] == '[' {
			end := i + 1
			if end < len(pattern) && pattern[end] == '!' {
				end++
			}
			if end == len(pattern) || pattern[end] == ']' {
				return false
			}
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end == len(pattern) {
				return false
			}
			if !validClass(pattern[i+1 : end]) {
				return false
			}
			i = end
		}
	}
	return true
}

func validClass(class string) bool {
	start := 0
	if class[0] == '!' {
		start = 1
	}
	for i := start; i < len(class); {
		if i+2 < len(class) && class[i+1] == '-' {
			if class[i] > class[i+2] {
				return false
			}
			i += 3
		} else {
			i++
		}
	}
	return true
}

type CaptureValue struct {
	Name string
	Text string
}

type Match struct{ Captures []CaptureValue }

func (m *Match) Free(a mem.Allocator) {
	if m != nil {
		slices.Free(a, m.Captures)
		mem.Free(a, m)
	}
}

// MatchTarget applies anchored, leftmost-shortest target matching.
func (t *Target) MatchTarget(a mem.Allocator, target string) *Match {
	var captures []CaptureValue
	if !t.match(a, target, 0, 0, &captures) {
		slices.Free(a, captures)
		return nil
	}
	m := mem.Alloc[Match](a)
	m.Captures = slices.Clone(a, captures)
	slices.Free(a, captures)
	return m
}

func (t *Target) match(a mem.Allocator, target string, part int, offset int, captures *[]CaptureValue) bool {
	if part == len(t.Parts) {
		return offset == len(target)
	}
	p := t.Parts[part]
	if p.Kind == TargetLiteral {
		if len(target)-offset < len(p.Text) || target[offset:offset+len(p.Text)] != p.Text {
			return false
		}
		return t.match(a, target, part+1, offset+len(p.Text), captures)
	}
	previous := ""
	found := false
	for i := range *captures {
		if (*captures)[i].Name == p.Name {
			previous, found = (*captures)[i].Text, true
			break
		}
	}
	for end := offset + 1; end <= len(target); end++ {
		value := target[offset:end]
		if found && value != previous {
			continue
		}
		if !matchPattern(p.Pattern, value) {
			continue
		}
		if !found {
			*captures = slices.Append(a, *captures, CaptureValue{Name: p.Name, Text: value})
		}
		if t.match(a, target, part+1, end, captures) {
			return true
		}
		if !found {
			*captures = (*captures)[:len(*captures)-1]
		}
	}
	return false
}

func matchPattern(pattern string, value string) bool { return matchPatternAt(pattern, value, 0, 0) }

func matchPatternAt(pattern string, value string, pi int, vi int) bool {
	if pi == len(pattern) {
		return vi == len(value)
	}
	if pattern[pi] == '*' {
		cross := pi+1 < len(pattern) && pattern[pi+1] == '*'
		next := pi + 1
		if cross {
			next++
		}
		for end := vi + 1; end <= len(value); end++ {
			if !cross && value[end-1] == '/' {
				break
			}
			if matchPatternAt(pattern, value, next, end) {
				return true
			}
		}
		return false
	}
	if pattern[pi] == '?' {
		return vi < len(value) && value[vi] != '/' && matchPatternAt(pattern, value, pi+1, vi+1)
	}
	if pattern[pi] == '\\' {
		return pi+1 < len(pattern) && vi < len(value) && pattern[pi+1] == value[vi] && matchPatternAt(pattern, value, pi+2, vi+1)
	}
	if pattern[pi] == '[' {
		end := pi + 1
		for end < len(pattern) && pattern[end] != ']' {
			end++
		}
		return end < len(pattern) && vi < len(value) && classMatches(pattern[pi+1:end], value[vi]) && matchPatternAt(pattern, value, end+1, vi+1)
	}
	return vi < len(value) && pattern[pi] == value[vi] && matchPatternAt(pattern, value, pi+1, vi+1)
}

func classMatches(class string, value byte) bool {
	invert, i, matched := false, 0, false
	if len(class) != 0 && class[0] == '!' {
		invert, i = true, 1
	}
	for i < len(class) {
		if i+2 < len(class) && class[i+1] == '-' {
			if value >= class[i] && value <= class[i+2] {
				matched = true
			}
			i += 3
		} else {
			if value == class[i] {
				matched = true
			}
			i++
		}
	}
	if invert {
		return !matched
	}
	return matched
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

// FormatTarget returns allocator-owned canonical target-template text.
func FormatTarget(a mem.Allocator, t *Target) string {
	b := strings.NewBuilder(a)
	for i := range t.Parts {
		part := t.Parts[i]
		if part.Kind == TargetLiteral {
			for j := 0; j < len(part.Text); j++ {
				if part.Text[j] == '\\' || part.Text[j] == '{' || part.Text[j] == '}' {
					b.WriteByte('\\')
				}
				b.WriteByte(part.Text[j])
			}
			continue
		}
		b.WriteByte('{')
		b.WriteString(part.Name)
		if part.Pattern != "*" {
			b.WriteByte(':')
			b.WriteString(part.Pattern)
		}
		b.WriteByte('}')
	}
	value := owned(a, b.String())
	b.Free()
	return value
}
