package script

import (
	"kame/internal/mem"
	"kame/lang/expr"
)

func (p *kashParser) loopTransferStatement(start int, wordEnd int, kind expr.Kind) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = kind, start
	if p.Loops == 0 {
		s.error(start, wordEnd, "loop transfer requires a lexically enclosing loop in the same function or script")
	}
	pos := wordEnd
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	if pos < len(text) && text[pos] != '\n' && text[pos] != ';' && text[pos] != '#' {
		s.error(pos, pos+1, "loop transfers take no operands, acceptance, process operators or redirections")
	}
	p.Pos, e.Span.End = pos, pos
	return e
}
