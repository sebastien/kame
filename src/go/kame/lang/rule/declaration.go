package rule

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
)

// DeclarationStart identifies rule, task and service keywords at a statement's start.
func DeclarationStart(text string) bool {
	_, _, ok := declarationHead(text)
	return ok
}

func declarationHead(text string) (Kind, int, bool) {
	end := strings.IndexAny(text, " \t\r\n")
	if end < 0 {
		end = len(text)
	}
	switch text[:end] {
	case "rule":
		return FileRule, end, true
	case "task":
		return TaskRule, end, true
	case "service":
		return ServiceRule, end, true
	}
	return FileRule, 0, false
}

// ParseDeclaration parses a keyword-led rule and its body, retaining source spans.
func ParseDeclaration(a mem.Allocator, s *source.Source, start, end int) Part {
	p := parser{a: a, s: s, start: start, end: end, deferRecipeDiagnostics: true}
	return Part{Rule: p.declaration(), Diagnostics: p.diags}
}

func init() {
	expr.ParseRuleValue = parseRuleValue
}

func parseRuleValue(a mem.Allocator, s *source.Source, start int, delimited bool) (*expr.RuleSyntax, int, []source.Diagnostic) {
	bounded := *s
	end := len(s.Text)
	if delimited {
		if stop := ruleValueDelimiter(s.Text, start); stop >= 0 {
			end = stop
		}
	}
	bounded.Text = s.Text[:end]
	part := ParseDeclaration(a, &bounded, start, end)
	syntax := &expr.RuleSyntax{Declaration: part.Rule, Alloc: a, References: 1,
		Release: func(a mem.Allocator, value any) { FreeRule(a, value.(*Rule)) },
		Format:  func(a mem.Allocator, value any) string { return FormatRule(a, value.(*Rule)) }}
	return syntax, part.Rule.Span.End, part.Diagnostics
}

func ruleValueDelimiter(text string, start int) int {
	depth := 0
	var quote byte
	end := source.LogicalLineEnd(text, start)
	for i := start; i < end; i++ {
		b := text[i]
		if b == '\\' && quote != '\'' {
			i++
			continue
		}
		if quote != 0 {
			if b == quote {
				quote = 0
			}
			continue
		}
		switch b {
		case '"', '\'':
			quote = b
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return i
			}
			depth--
		case ',':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func (p *parser) declaration() *Rule {
	text := p.s.Text[:p.end]
	lineEnd := source.LogicalLineEnd(text, p.start)
	start, end := trim(text, p.start, lineEnd)
	kind, width, ok := declarationHead(text[start:end])
	r := mem.Alloc[Rule](p.a)
	r.Kind = kind
	r.Header = source.Span{Start: start, End: end}
	r.Span = source.Span{Start: p.start, End: lineEnd}
	if !ok {
		p.error(start, end, "expected rule, task or service")
		return r
	}

	head, _ := trim(text, start+width, end)
	items := ranges(p.a, text, head, end)
	defer mem.FreeSlice(nil, items)
	if len(items) != 0 {
		modifier := text[items[0].Start:items[0].End]
		if kind == FileRule && modifier == "always" || kind == TaskRule && modifier == "cached" {
			r.Always = kind == FileRule
			if modifier == "cached" {
				r.Kind = CachedTaskRule
			}
			head = items[0].End
			items = items[1:]
		}
	}
	targetEnd, inputStart := -1, -1
	tail := end
	for _, token := range items {
		switch text[token.Start] {
		case ':':
			if text[token.Start:token.End] != ":" {
				continue
			}
			if inputStart >= 0 {
				p.error(token.Start, token.End, "expected one prerequisite separator")
				return r
			}
			targetEnd, inputStart = token.Start, token.End
		case ';', '=', '#':
			tail = token.Start
		}
		if tail != end {
			break
		}
	}
	if targetEnd < 0 {
		targetEnd = tail
	}
	p.ruleTargets(r, head, targetEnd)
	if inputStart >= 0 {
		p.ruleInputs(r, inputStart, tail)
	}
	p.classify(r)
	p.validateArguments(r)

	pos := tail
	if pos < end && text[pos] == ';' {
		metaStart, _ := trim(text, pos+1, end)
		if metaStart == end || text[metaStart] != '{' {
			p.error(metaStart, end, "rule metadata must be a record")
			return r
		}
		metadata := expr.ParsePrefix(p.a, p.s, metaStart)
		p.takeDiagnostics(metadata.Diagnostics)
		mem.FreeSlice(nil, metadata.Diagnostics)
		r.Metadata = metadata.Expr
		if r.Metadata == nil || r.Metadata.Kind != expr.Record {
			p.error(metaStart, metadata.End, "rule metadata must be a record")
			return r
		}
		pos = metadata.End
		lineEnd = source.LogicalLineEnd(text, pos)
	}
	for pos < lineEnd && space(text[pos]) {
		pos++
	}
	if pos < lineEnd && text[pos] == '=' {
		pos++
		for pos < lineEnd && space(text[pos]) {
			pos++
		}
		if pos == lineEnd && pos+1 < p.end {
			next := pos + 1
			if text[next] == ' ' || text[next] == '\t' {
				for next < p.end && (text[next] == ' ' || text[next] == '\t') {
					next++
				}
				pos = next
			}
		}
		if pos == lineEnd {
			p.error(pos, pos, "expected rule body expression")
			return r
		}
		value := expr.ParseValuePrefix(p.a, p.s, pos)
		p.takeDiagnostics(value.Diagnostics)
		mem.FreeSlice(nil, value.Diagnostics)
		r.BodyExpression = value.Expr
		pos = value.End
		lineEnd = source.LogicalLineEnd(text, pos)
	}
	pos, end = trim(text, pos, lineEnd)
	if pos < end {
		if text[pos] == '#' {
			r.HeaderComment = text[pos:end]
		} else {
			p.error(pos, end, "unexpected declaration header text")
		}
	}
	r.Header.End, r.Span.End = end, lineEnd
	if r.BodyExpression == nil {
		headerLine := strings.LastIndexByte(text[:start], '\n') + 1
		headerIndent := text[headerLine:start]
		if strings.Trim(headerIndent, " \t") != "" {
			headerIndent = ""
		}
		p.recipe(r, lineEnd, headerIndent)
	}
	return r
}
