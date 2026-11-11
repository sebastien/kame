package script

import (
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
)

// Reuse the definition RHS parser, but retain a reached runtime statement.
func (p *kashParser) eagerBinding(start int, wordEnd int) *definition.Definition {
	s, text := p.Script, p.Script.Source.Text
	pos := wordEnd
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	headerEnd := definition.KashHeaderEnd(s.Alloc, s.Source, pos)
	if headerEnd == 0 {
		s.error(start, pos, "let requires NAME = VALUE")
		return nil
	}
	valueStart := headerEnd
	for valueStart < len(text) && space(text[valueStart]) {
		valueStart++
	}
	if valueStart == len(text) || text[valueStart] == '\n' || text[valueStart] == ';' || text[valueStart] == '#' {
		s.error(headerEnd, valueStart, "let requires an initializer")
		return nil
	}
	part := definition.ParseKashPrefix(s.Alloc, s.Source, pos)
	s.takeDiagnostics(part.Diagnostics)
	d := part.Definition
	if d == nil {
		return nil
	}
	if d.Function || d.Default || d.Contribution || d.Name == "env" {
		s.error(start, d.Span.End, "let requires one non-reserved value name and =")
	}
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
