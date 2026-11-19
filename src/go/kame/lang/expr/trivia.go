package expr

import (
	"kame/internal/mem"
	"kame/lang/source"
	"slices"
	"sort"
	"strings"
)

type expressionTrivia struct {
	text   string
	indent string
	spans  []source.Span
}

func (p *parser) retainTrivia(e *Expr) {
	if e == nil || len(p.diags) != 0 || len(p.triviaSpans) == 0 {
		return
	}
	// Lookahead can visit the same gap twice or read past the expression.
	slices.SortFunc(p.triviaSpans, func(a, b source.Span) int { return a.Start - b.Start })
	var spans []source.Span
	for _, span := range p.triviaSpans {
		if span.Start < e.Span.Start || span.End > e.Span.End {
			continue
		}
		span.Start -= e.Span.Start
		span.End -= e.Span.Start
		if len(spans) == 0 || spans[len(spans)-1] != span {
			spans = append(spans, span)
		}
	}
	if len(spans) != 0 {
		e.trivia = mem.Alloc[expressionTrivia](p.a)
		*e.trivia = expressionTrivia{
			text:   sourceText(p.a, p.s.Text[e.Span.Start:e.Span.End]),
			indent: sourceText(p.a, p.indent),
			spans:  spans,
		}
	}
}

func restoreTrivia(e *Expr, formatted string) string {
	if e == nil || e.trivia == nil {
		return formatted
	}
	c := e.trivia
	// Value layout changes whitespace and trailing commas. Match authored bytes
	// to that layout while skipping only those separators and parser trivia.
	var authored, canonical []int
	remaining := c.spans
	next := func(text string, pos int, comments *[]source.Span) int {
		for pos < len(text) {
			if comments != nil && len(*comments) != 0 && pos == (*comments)[0].Start {
				pos = (*comments)[0].End
				*comments = (*comments)[1:]
			} else if strings.ContainsRune(" \t\r\n", rune(text[pos])) {
				pos++
			} else if after := source.ContinuationEnd(text, pos, len(text)); after > pos {
				pos = after
			} else {
				break
			}
		}
		return pos
	}
	i, j := next(c.text, 0, &remaining), next(formatted, 0, nil)
	for i < len(c.text) || j < len(formatted) {
		switch {
		case i < len(c.text) && j < len(formatted) && c.text[i] == formatted[j]:
			authored, canonical = append(authored, i), append(canonical, j)
			i, j = next(c.text, i+1, &remaining), next(formatted, j+1, nil)
		case i < len(c.text) && c.text[i] == ',':
			i = next(c.text, i+1, &remaining)
		case j < len(formatted) && formatted[j] == ',':
			j = next(formatted, j+1, nil)
		default:
			// A command word may also change its quoting. Keep the authored
			// expression if the comment cannot be placed at a matching boundary.
			if c.indent != "" {
				return strings.ReplaceAll(c.text, "\n"+c.indent, "\n")
			}
			return c.text
		}
	}
	var out strings.Builder
	start := 0
	for k := 0; k < len(c.spans); {
		anchor := sort.SearchInts(authored, c.spans[k].Start)
		if anchor == 0 || anchor == len(authored) {
			return c.text
		}
		for k < len(c.spans) && c.spans[k].End <= authored[anchor] {
			k++
		}
		left, right := canonical[anchor-1]+1, canonical[anchor]
		out.WriteString(formatted[start:left])
		writeTriviaGap(&out, c.text[authored[anchor-1]+1:authored[anchor]], formatted[left:right], c.indent, formatted[right])
		start = right
	}
	out.WriteString(formatted[start:])
	return out.String()
}

func writeTriviaGap(out *strings.Builder, authored, formatted, structuralIndent string, next byte) {
	if strings.Contains(formatted, ",") {
		out.WriteByte(',')
	}
	indent, hasIndent := "", false
	if last := strings.LastIndexByte(formatted, '\n'); last >= 0 {
		indent, hasIndent = formatted[last+1:], true
	}
	lineNumber := 0
	lastLine := strings.Count(authored, "\n")
	for line := range strings.SplitSeq(authored, "\n") {
		if lineNumber != 0 {
			out.WriteByte('\n')
		}
		leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if lineNumber != 0 {
			leading = strings.TrimPrefix(leading, structuralIndent)
		}
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			if lineNumber == 0 {
				out.WriteByte(' ')
			} else if hasIndent {
				out.WriteString(indent)
				if strings.ContainsRune(")]}", rune(next)) {
					out.WriteByte('\t')
				}
			} else {
				out.WriteString(leading)
			}
			out.WriteString(strings.TrimRight(line[comment:], " \t\r"))
		} else if lineNumber == lastLine {
			if hasIndent {
				out.WriteString(indent)
			} else {
				out.WriteString(leading)
			}
		}
		lineNumber++
	}
}
