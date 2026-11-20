package script

import (
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
)

// bindingValueStart leaves an unindented statement for the block parser.
func bindingValueStart(text string, start, afterEquals int) int {
	end := physicalLineEnd(text, afterEquals)
	value := trimStart(text, afterEquals, end)
	for {
		next := source.ContinuationEnd(text, value, len(text))
		if next == value {
			break
		}
		value = trimStart(text, next, len(text))
	}
	if value != end || end == len(text) {
		return value
	}
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	indentEnd := trimStart(text, lineStart, start)
	next := trimStart(text, end+1, len(text))
	if expr.Columns(text[end+1:next], 0) > expr.Columns(text[lineStart:indentEnd], 0) {
		return next
	}
	return value
}

// Reuse the definition RHS parser, but retain a reached runtime statement.
func (p *kashParser) eagerBinding(start int, wordEnd int) *definition.Definition {
	s, text := p.Script, p.Script.Source.Text
	pos := wordEnd
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	headerEnd := definition.ValueHeaderEnd(s.Alloc, s.Source, pos)
	if headerEnd == 0 {
		s.error(start, pos, "let requires NAME = VALUE")
		return nil
	}
	valueStart := bindingValueStart(text, start, headerEnd)
	if valueStart == len(text) || text[valueStart] == '\n' || text[valueStart] == ';' || text[valueStart] == '#' {
		s.error(headerEnd, valueStart, "let requires an initializer")
		return nil
	}
	part := definition.ParseBindingHeader(s.Alloc, s.Source, pos, headerEnd)
	s.takeDiagnostics(part.Diagnostics)
	d := part.Definition
	if d == nil {
		return nil
	}
	if d.ExplicitDef || d.Function || d.Default || d.Contribution {
		s.error(start, d.Span.End, "let requires one non-reserved value name and =")
	}
	value := expr.ParseKashValueExpressionPrefix(s.Alloc, s.Source, valueStart)
	s.takeDiagnostics(value.Diagnostics)
	d.Expression, d.Span.End = value.Expr, value.End
	d.Eager, d.Span.Start = true, start
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Text, e.Span, e.Kash = expr.KashLet, d.Name, d.Span, true
	e.Items = append(e.Items, d.Expression)
	d.Expression = e
	expr.SetCommandChannels(s.Alloc, e, p.Channels)
	p.Pos = d.Span.End
	end := p.Pos
	for end < len(text) && space(text[end]) {
		end++
	}
	if end < len(text) && text[end] != '\n' && text[end] != ';' && text[end] != '#' {
		s.error(end, end+1, "unexpected let initializer; expected statement separator")
	}
	return d
}
