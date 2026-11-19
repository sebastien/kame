package script

import (
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
)

func (s *Script) blockValue(start, afterEquals int) (ScriptItem, int, bool) {
	p := kashParser{Script: s, Pos: start}
	d := p.valueDefinition(start, afterEquals)
	if d == nil {
		return ScriptItem{}, start, false
	}
	return ScriptItem{Kind: Definition, Span: d.Span, Definition: d}, p.Pos, true
}

func (p *kashParser) valueDefinition(start, afterEquals int) *definition.Definition {
	s, text := p.Script, p.Script.Source.Text
	if afterEquals == 0 {
		return nil
	}
	end := physicalLineEnd(text, afterEquals)
	tail := trimStart(text, afterEquals, end)
	if tail != end && text[tail] != '#' {
		return nil
	}
	bodyStart := nextLine(text, end)
	for pos := bodyStart; pos < len(text); {
		lineEnd := physicalLineEnd(text, pos)
		content := trimStart(text, pos, lineEnd)
		if content == lineEnd || text[content] == '#' {
			pos = nextLine(text, lineEnd)
			continue
		}
		if content == pos || !s.startsValueBlock(content, lineEnd) {
			return nil
		}
		part := definition.ParseBindingHeader(s.Alloc, s.Source, start, afterEquals)
		d := part.Definition
		if d.Function || d.Default || d.Contribution || (s.Kash && !d.ExplicitDef) {
			definition.Free(s.Alloc, d)
			mem.FreeSlice(nil, part.Diagnostics)
			return nil
		}
		s.takeDiagnostics(part.Diagnostics)
		body := mem.Alloc[expr.Expr](s.Alloc)
		body.Kind = expr.ValueBlock
		body.Span.Start = content
		if tail != end {
			body.Text = strings.TrimRight(text[tail:end], " \t\r")
		}
		d.Expression = body
		previous, loops := p.ValueBlock, p.Loops
		p.ValueBlock, p.Loops = true, 0
		p.Functions++
		p.Pos = bodyStart
		p.branchBody(body, 1)
		p.Functions--
		p.ValueBlock, p.Loops = previous, loops
		body.Span.End = body.Span.Start + len(strings.TrimRight(text[body.Span.Start:max(body.Span.Start, body.Span.End)], " \t\r\n"))
		d.Span.End = body.Span.End
		return d
	}
	return nil
}

func (s *Script) startsValueBlock(start, end int) bool {
	text := s.Source.Text
	word, operand := declarationWord(text, start, end)
	switch word {
	case "let", "set", "return", "for", "while", "defer", "match":
		return true
	case "if":
		bounded := *s.Source
		bounded.Text = text[:end]
		condition := expr.ParseValuePrefix(s.Alloc, &bounded, operand)
		defer expr.Free(s.Alloc, condition.Expr)
		defer mem.FreeSlice(nil, condition.Diagnostics)
		return !standaloneKeyword(text[:end], trimStart(text, condition.End, end), "then")
	default:
		return false
	}
}

func (p *kashParser) valueReturn(start, operand int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	if p.Functions == 0 && !p.ValueBlock && !p.RuleBody {
		s.error(start, operand, "return requires an enclosing def, value block or rule body")
	}
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind = expr.ValueReturn
	e.Span = source.Span{Start: start, End: operand}
	pos := trimStart(text, operand, len(text))
	if pos < len(text) && text[pos] != '\n' && text[pos] != ';' && text[pos] != '#' {
		part := expr.ParseValuePrefix(s.Alloc, s.Source, pos)
		s.takeDiagnostics(part.Diagnostics)
		e.Items = append(e.Items, part.Expr)
		pos, e.Span.End = part.End, part.End
		end := trimStart(text, pos, len(text))
		if end < len(text) && text[end] != '\n' && text[end] != ';' && text[end] != '#' {
			s.error(end, end+1, "unexpected text after return expression")
		}
	}
	p.Pos = pos
	return e
}
