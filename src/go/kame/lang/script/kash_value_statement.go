package script

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
)

func (p *kashParser) valueStatement(word string, start, operand int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	end := physicalLineEnd(text, operand)
	pos := trimStart(text, operand, end)
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Span = source.Span{Start: start, End: end}
	p.Pos = end
	var target *expr.Expr
	if word == "yield" {
		e.Kind = expr.KashYield
		if p.Functions == 0 || p.ValueBlock {
			s.error(start, operand, "yield requires an enclosing function body")
			return e
		}
	} else if strings.HasPrefix(text[pos:end], "env/${") {
		e.Kind, e.Text = expr.KashSet, "env/"
		name := expr.ParseKashInterpolation(s.Alloc, s.Source, pos+len("env/"))
		s.takeDiagnostics(name.Diagnostics)
		p.Pos = physicalLineEnd(text, name.End)
		equals := trimStart(text, name.End, p.Pos)
		if len(name.Diagnostics) != 0 {
			expr.Free(s.Alloc, name.Expr)
			return e
		}
		if equals == p.Pos || text[equals] != '=' {
			s.error(equals, equals, "set requires NAME = EXPR")
			expr.Free(s.Alloc, name.Expr)
			return e
		}
		target = name.Expr
		pos = bindingValueStart(text, start, equals+1)
	} else {
		e.Kind = expr.KashSet
		equals := strings.IndexByte(text[pos:end], '=')
		if equals < 0 {
			s.error(pos, end, "set requires NAME = EXPR")
			return e
		}
		equals += pos
		_, targetEnd := trim(text, pos, equals)
		e.Text = text[pos:targetEnd]
		name, environment := strings.CutPrefix(e.Text, "env/")
		if environment && !expr.ValidEnvironmentName(name) || !environment && !expr.ValidBindingName(name) {
			s.error(pos, targetEnd, "invalid set target")
			return e
		}
		pos = bindingValueStart(text, start, equals+1)
	}
	if pos == len(text) || text[pos] == '\n' || text[pos] == '#' || text[pos] == ';' {
		expr.Free(s.Alloc, target)
		s.error(pos, pos, word+" requires an expression")
		return e
	}
	value := expr.ParseValuePrefix(s.Alloc, s.Source, pos)
	s.takeDiagnostics(value.Diagnostics)
	e.Items = append(e.Items, value.Expr)
	if target != nil {
		e.Items = append(e.Items, target)
	}
	e.Span.End, p.Pos = value.End, value.End
	tail := trimStart(text, p.Pos, len(text))
	if tail < len(text) && text[tail] != '\n' && text[tail] != ';' && text[tail] != '#' {
		s.error(tail, tail+1, "unexpected text after "+word+" expression")
	}
	return e
}

// A nested function owns its returns and yields, even when it closes over this
// function. Inspect the statement tree of the current invocation only.
func (p *kashParser) checkGenerator(function *expr.Expr) {
	var returns []source.Span
	var visit func(*expr.Expr)
	visit = func(e *expr.Expr) {
		if e == nil || e.Kind == expr.KashDefinition {
			return
		}
		switch e.Kind {
		case expr.KashYield:
			function.Bool = true
		case expr.ValueReturn:
			if len(e.Items) != 0 {
				returns = append(returns, e.Span)
			}
		}
		for _, child := range e.Items {
			visit(child)
		}
		for _, child := range e.Body {
			visit(child)
		}
	}
	for _, statement := range function.Body {
		visit(statement)
	}
	if function.Bool {
		for _, span := range returns {
			p.Script.error(span.Start, span.End, "a generator return takes no value")
		}
	}
}
