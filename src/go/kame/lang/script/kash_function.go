package script

import (
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
)

func (s *Script) blockFunction(start, end int) (ScriptItem, int, bool) {
	word, _ := declarationWord(s.Source.Text, start, end)
	if word != "def" || definition.ValueHeaderEnd(s.Alloc, s.Source, start) != 0 {
		return ScriptItem{}, start, false
	}
	p := kashParser{Script: s, Pos: start}
	d := p.functionBlock(0, start)
	return ScriptItem{Kind: Definition, Span: d.Span, Definition: d}, p.Pos, true
}

func (p *kashParser) functionBlock(level, start int) *definition.Definition {
	s, text := p.Script, p.Script.Source.Text
	end := physicalLineEnd(text, start)
	part := definition.ParseFunctionHeader(s.Alloc, s.Source, start, end)
	s.takeDiagnostics(part.Diagnostics)
	d := part.Definition
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span, e.Text, d.Expression = expr.KashFunction, d.Span, d.Name, e
	if d.Name == "env" {
		s.error(d.NameSpan.Start, d.NameSpan.End, "expected non-reserved function name")
	}
	for _, parameter := range d.Parameters {
		if parameter.Name == "env" {
			s.error(parameter.Span.Start, parameter.Span.End, "invalid or reserved function parameter")
		}
		e.Parameters = append(e.Parameters, expr.Parameter{Name: parameter.Name, Span: parameter.Span, Rest: parameter.Rest})
	}
	p.Pos = nextLine(text, end)
	if len(s.Diagnostics) != 0 {
		return d
	}
	p.Functions++
	previousLoops, valueBlock, ruleBody := p.Loops, p.ValueBlock, p.RuleBody
	p.Loops, p.ValueBlock, p.RuleBody = 0, false, false
	p.branchBody(e, level+1)
	p.Loops, p.ValueBlock, p.RuleBody = previousLoops, valueBlock, ruleBody
	p.Functions--
	p.checkGenerator(e)
	for i := range e.Body {
		statement := e.Body[i]
		if statement.Kind != expr.KashDefinition && statement.Kind != expr.KashLet {
			continue
		}
		for j := range d.Parameters {
			if statement.Text == d.Parameters[j].Name {
				s.error(statement.Span.Start, statement.Span.End, "body definition conflicts with function parameter")
			}
		}
	}
	d.Span.End, e.Span.End = p.Pos, p.Pos
	return d
}

func (p *kashParser) exitStatement(start int, wordEnd int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = expr.KashExit, start
	pos := wordEnd
	for pos < len(text) && space(text[pos]) && text[pos] != '\n' {
		pos++
	}
	if pos == len(text) || text[pos] == '\n' || text[pos] == ';' || text[pos] == '#' {
		p.Pos, e.Span.End = pos, pos
		return e
	}
	part := expr.ParseKashValuePrefix(s.Alloc, s.Source, pos)
	s.takeDiagnostics(part.Diagnostics)
	e.Items = append(e.Items, part.Expr)
	if part.Expr != nil && part.Expr.Kind == expr.ValueRecovery {
		s.error(pos, part.End, "exit requires one status expression")
	}
	pos = part.End
	for pos < len(text) && space(text[pos]) && text[pos] != '\n' {
		pos++
	}
	if pos < len(text) && text[pos] != '\n' && text[pos] != ';' && text[pos] != '#' {
		s.error(pos, pos+1, "exit has unexpected trailing operands or process syntax")
	}
	p.Pos, e.Span.End = pos, pos
	return e
}
