package expr

import (
	"kame/internal/mem"
	"kame/lang/source"
	"strings"
)

func (p *parser) match(start int) *Expr {
	previous := p.valueLayout
	p.valueLayout = valueLine
	defer func() { p.valueLayout = previous }()
	match := p.node(Match, start)
	p.inlineSpace()
	subject := p.valueExpression()
	if subject == nil {
		return match
	}
	match.Body = []*Expr{subject}
	match.Span.End = subject.Span.End

	text := p.s.Text
	head := strings.LastIndexByte(text[:start], '\n') + 1
	base := text[head:start]
	base = base[:len(base)-len(strings.TrimLeft(base, " \t"))]
	var armIndent string
	for len(p.diags) == 0 {
		end := p.pos
		p.skipSpace()
		line := strings.LastIndexByte(text[:p.pos], '\n') + 1
		indent := text[line:p.pos]
		if p.pos == len(text) || line <= end || len(indent) <= len(base) ||
			!strings.HasPrefix(indent, base) || strings.Trim(indent, " \t") != "" ||
			strings.ContainsRune(")]},", rune(text[p.pos])) {
			p.pos = end
			break
		}
		if armIndent == "" {
			armIndent = indent
		}
		if indent != armIndent || (strings.ContainsRune(indent, ' ') && strings.ContainsRune(indent, '\t')) {
			p.error(line, p.pos, "inconsistent match arm indentation")
			break
		}
		if len(match.Items) != 0 && len(match.Items[len(match.Items)-1].Items) == 0 {
			p.error(p.pos, p.pos+1, "'else' must be the last match arm")
			break
		}
		arm := p.matchArm(armIndent)
		match.Items = append(match.Items, arm)
		match.Span.End = arm.Span.End
	}
	if len(match.Items) == 0 && len(p.diags) == 0 {
		p.error(p.pos, p.pos, "match needs an indented arm")
	}
	return match
}

func (p *parser) matchArm(indent string) *Expr {
	start := p.pos
	arm := p.node(MatchArm, start)
	if !p.takeKeyword("else") {
		pattern := p.armPattern()
		if pattern == nil {
			return arm
		}
		arm.Items = []*Expr{pattern}
	}
	p.inlineSpace()
	if !strings.HasPrefix(p.s.Text[p.pos:], "=>") {
		p.error(p.pos, p.pos+1, "expected '=>' after match pattern")
		return arm
	}
	p.pos += 2
	arrowEnd := p.pos
	p.skipSpace()
	if newline := strings.LastIndexByte(p.s.Text[arrowEnd:p.pos], '\n'); newline >= 0 {
		line := p.s.Text[arrowEnd+newline+1 : p.pos]
		if len(line) <= len(indent) || !strings.HasPrefix(line, indent) {
			p.error(p.pos, p.pos+1, "expected an indented match arm result")
			return arm
		}
	}
	result := p.valueExpression()
	if result != nil {
		arm.Body = []*Expr{result}
		arm.Span.End = result.Span.End
		if matchResultHasLambda(result) {
			p.error(result.Span.Start, result.Span.End, "parenthesize a lambda in a match arm")
		}
	}
	return arm
}

func (p *parser) invalidMatchPattern(span source.Span, message string) {
	p.diags = append(p.diags, source.Diagnostic{Code: "PAT_INVALID", Severity: source.Error, Span: span, Message: message})
}

// ParseMatchPatternPrefix reads one matcher operand under match-arm grammar.
func ParseMatchPatternPrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start, indent: s.StructuralIndent}
	pattern := p.armPattern()
	if len(p.diags) == 0 {
		p.checkPlaceholders(pattern)
	}
	return Prefix{Expr: pattern, End: p.pos, Diagnostics: p.diags}
}

func (p *parser) armPattern() *Expr {
	start := p.pos
	rest := p.s.Text[start:]
	var pattern *Expr
	switch {
	case strings.HasPrefix(rest, "\"") && !strings.HasPrefix(rest, "\"\"\""):
		pattern = p.quoted(true)
	case strings.HasPrefix(rest, "./"), strings.HasPrefix(rest, "../"),
		strings.HasPrefix(rest, "/"), strings.HasPrefix(rest, "~/"), strings.HasPrefix(rest, "${"):
		pattern = p.pathAtom()
		p.classifyPathPattern(pattern, true)
	default:
		pattern = p.atom()
	}
	if pattern == nil {
		return nil
	}
	switch pattern.Kind {
	case Path, String, Boolean, Nil, Integer, Float, Symbol:
	default:
		p.error(start, p.pos, "expected a literal or pattern in match arm")
	}
	return pattern
}

func matchResultHasLambda(e *Expr) bool {
	if e.Kind == Lambda {
		return true
	}
	if e.Kind == Group || e.Kind == Section || e.Kind == Match || (e.Kind == Application && !e.Juxtaposed && e.Infix == "") {
		return false
	}
	for _, child := range e.Items {
		if matchResultHasLambda(child) {
			return true
		}
	}
	for _, field := range e.Fields {
		if field.Value != nil && matchResultHasLambda(field.Value) {
			return true
		}
	}
	return false
}

func writeMatch(a mem.Allocator, b *strings.Builder, e *Expr, column, indent int) {
	b.WriteString("match")
	if len(e.Body) == 0 {
		return
	}
	b.WriteByte(' ')
	writeLayout(a, b, e.Body[0], column+6, indent, 0)
	next := indent/8 + 1
	prefix := strings.Repeat("\t", next)
	patterns := make([]string, len(e.Items))
	defer mem.FreeSlice(nil, patterns)
	for i, arm := range e.Items {
		if len(arm.Items) == 0 {
			patterns[i] = sourceText(a, "else")
		} else {
			patterns[i] = Compact(a, arm.Items[0])
		}
		defer mem.FreeString(a, patterns[i])
	}
	for first := 0; first < len(e.Items); {
		width, resultsWidth, end := 0, 0, first
		for end < len(e.Items) && len(e.Items[end].Body) != 0 && !strings.ContainsRune(patterns[end], '\n') {
			candidate := max(width, Columns(patterns[end], 0))
			result := Compact(a, e.Items[end].Body[0])
			singleLine := !strings.ContainsRune(result, '\n')
			resultWidth := max(resultsWidth, Columns(result, 0))
			mem.FreeString(a, result)
			if !singleLine || next*8+candidate+4+resultWidth > 80 {
				break
			}
			width, resultsWidth = candidate, resultWidth
			end++
		}
		if end == first {
			b.WriteByte('\n')
			b.WriteString(prefix)
			b.WriteString(patterns[first])
			b.WriteString(" =>")
			if len(e.Items[first].Body) != 0 {
				b.WriteByte('\n')
				b.WriteString(prefix)
				b.WriteByte('\t')
				writeLayout(a, b, e.Items[first].Body[0], (next+1)*8, (next+1)*8, 0)
			}
			first++
			continue
		}
		for i := first; i < end; i++ {
			b.WriteByte('\n')
			b.WriteString(prefix)
			b.WriteString(patterns[i])
			b.WriteString(strings.Repeat(" ", width-Columns(patterns[i], 0)))
			b.WriteString(" => ")
			writeExpr(b, e.Items[i].Body[0])
		}
		first = end
	}
}
