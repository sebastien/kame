package script

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *kashParser) functionBlock(level int, start int, wordEnd int) *definition.Definition {
	s, text := p.Script, p.Script.Source.Text
	d := mem.Alloc[definition.Definition](s.Alloc)
	d.Function, d.Span.Start = true, start
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start, d.Expression = expr.KashFunction, start, e
	pos := wordEnd
	for pos < len(text) && space(text[pos]) && text[pos] != '\n' {
		pos++
	}
	nameStart := pos
	for pos < len(text) && !space(text[pos]) && text[pos] != '\n' && text[pos] != ';' {
		pos++
	}
	d.Name, d.NameSpan = text[nameStart:pos], source.Span{Start: nameStart, End: pos}
	e.Text = d.Name
	if !definition.ValidName(d.Name) || d.Name == "env" {
		s.error(nameStart, pos, "expected non-reserved function name")
	}
	for pos < len(text) && text[pos] != '\n' && text[pos] != '#' && len(s.Diagnostics) == 0 {
		if !space(text[pos]) {
			s.error(pos, pos+1, "function parameters need whitespace")
			break
		}
		for pos < len(text) && space(text[pos]) && text[pos] != '\n' {
			pos++
		}
		if pos == len(text) || text[pos] == '\n' || text[pos] == '#' {
			break
		}
		parameterStart := pos
		for pos < len(text) && !space(text[pos]) && text[pos] != '\n' {
			pos++
		}
		name := text[parameterStart:pos]
		rest := strings.HasSuffix(name, "...")
		if rest {
			name = name[:len(name)-3]
		}
		if !definition.ValidName(name) || name == "env" {
			s.error(parameterStart, pos, "invalid or reserved function parameter")
		}
		for i := range d.Parameters {
			if d.Parameters[i].Name == name || d.Parameters[i].Rest {
				s.error(parameterStart, pos, "duplicate parameter or non-final rest parameter")
			}
		}
		span := source.Span{Start: parameterStart, End: pos}
		d.Parameters = slices.Append(s.Alloc, d.Parameters, definition.Parameter{Name: name, Span: span, Rest: rest})
		e.Parameters = slices.Append(s.Alloc, e.Parameters, expr.Parameter{Name: name, Span: span, Rest: rest})
	}
	for pos < len(text) && text[pos] != '\n' {
		pos++
	}
	p.Pos = nextLine(text, pos)
	p.Functions++
	previousLoops := p.Loops
	p.Loops = 0
	p.branchBody(e, level+1)
	p.Loops = previousLoops
	p.Functions--
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

func (p *kashParser) transferStatement(start int, wordEnd int, kind expr.Kind) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	missing := "return requires a status expression"
	single := "return requires one status expression"
	trailing := "return has unexpected trailing operands or process syntax"
	if kind == expr.KashExit {
		missing = "exit requires a status expression"
		single = "exit requires one status expression"
		trailing = "exit has unexpected trailing operands or process syntax"
	}
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = kind, start
	if kind == expr.KashReturn && p.Functions == 0 {
		s.error(start, wordEnd, "return requires an enclosing def")
	}
	pos := wordEnd
	for pos < len(text) && space(text[pos]) && text[pos] != '\n' {
		pos++
	}
	if pos == len(text) || text[pos] == '\n' || text[pos] == ';' || text[pos] == '#' {
		s.error(wordEnd, pos, missing)
		p.Pos, e.Span.End = pos, pos
		return e
	}
	part := expr.ParseKashValuePrefix(s.Alloc, s.Source, pos)
	s.takeDiagnostics(part.Diagnostics)
	e.Items = slices.Append(s.Alloc, e.Items, part.Expr)
	if part.Expr != nil && part.Expr.Kind == expr.ValueRecovery {
		s.error(pos, part.End, single)
	}
	pos = part.End
	for pos < len(text) && space(text[pos]) && text[pos] != '\n' {
		pos++
	}
	if kind == expr.KashReturn && pos < len(text) && text[pos] == '?' {
		e.AcceptExit = true
		pos++
	}
	for pos < len(text) && space(text[pos]) && text[pos] != '\n' {
		pos++
	}
	if pos < len(text) && text[pos] != '\n' && text[pos] != ';' && text[pos] != '#' {
		s.error(pos, pos+1, trailing)
	}
	p.Pos, e.Span.End = pos, pos
	return e
}
