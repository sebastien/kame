package script

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
)

// Session declarations have syntax for parsing/formatting, not batch execution.
func (p *kashParser) sessionCommand(start, wordEnd int) ScriptItem {
	s, text := p.Script, p.Script.Source.Text
	word, pos := text[start:wordEnd], wordEnd
	for pos < len(text) && text[pos] != '\n' && text[pos] != ';' && text[pos] != '#' && text[pos] != '=' {
		pos++
	}
	headEnd := pos
	for headEnd > wordEnd && space(text[headEnd-1]) {
		headEnd--
	}
	item := ScriptItem{Kind: SessionCommand, Text: text[start:headEnd], Span: source.Span{Start: start}}
	words := strings.Fields(item.Text)
	defer mem.FreeSlice(nil, words)
	if word == "alias" && pos < len(text) && text[pos] == '=' {
		if len(words) != 2 {
			s.error(start, pos, "alias declaration expects one name before =")
		}
		pos++
		for pos < len(text) && space(text[pos]) {
			pos++
		}
		part := expr.ParseKashValuePrefix(s.Alloc, s.Source, pos)
		s.takeDiagnostics(part.Diagnostics)
		item.Expression, pos = part.Expr, part.End
		if item.Expression == nil || item.Expression.Kind != expr.List || len(item.Expression.Items) == 0 {
			s.error(start, pos, "alias expects a nonempty literal list of strings")
		}
	} else if word == "alias" && len(words) > 2 || word == "unalias" && len(words) < 2 {
		s.error(start, pos, "alias expects at most one name; unalias expects one or more names")
	}
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	if pos < len(text) && text[pos] != '\n' && text[pos] != '#' {
		s.error(pos, pos+1, "alias and unalias require a whole session entry")
	}
	p.Pos, item.Span.End = pos, pos
	return item
}
