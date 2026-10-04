package script

import (
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// Conditional markers remain in the authored AST. Source composition selects
// branches before registration, preserving the byte offsets of active items.
func parseConditional(s *Script, start int, end int) int {
	text := s.Source.Text[start:end]
	kind := When
	if text == "otherwise" {
		kind = Otherwise
	} else if text == "end" {
		kind = EndWhen
	} else if text != "when" && !strings.HasPrefix(text, "when ") && !strings.HasPrefix(text, "when\t") {
		return 0
	}
	if kind == When && topLevel(text, '=') >= 0 {
		return 0
	}
	item := ScriptItem{Kind: kind, Span: source.Span{Start: start, End: end}}
	if kind == When && len(text) == 4 {
		s.error(start, end, "when requires one predicate expression")
		s.Items = slices.Append(s.Alloc, s.Items, item)
		return nextLine(s.Source.Text, end)
	}
	if kind == When {
		from := start + 4
		for from < end && space(s.Source.Text[from]) {
			from++
		}
		part := expr.ParsePrefix(s.Alloc, s.Source, from)
		s.takeDiagnostics(part.Diagnostics)
		if part.End > end {
			end = source.LogicalLineEnd(s.Source.Text, part.End)
		}
		_, expressionEnd := trim(s.Source.Text, from, end)
		if part.End != expressionEnd {
			s.error(part.End, expressionEnd, "when requires one predicate expression")
		}
		item.Expression, item.Span.End = part.Expr, expressionEnd
	}
	s.Items = slices.Append(s.Alloc, s.Items, item)
	return nextLine(s.Source.Text, end)
}

type conditionalSyntax struct {
	Span      source.Span
	Otherwise bool
}

func validateConditionals(s *Script) {
	var stack []conditionalSyntax
	for i := range s.Items {
		item := s.Items[i]
		if item.Kind == When {
			stack = slices.Append(s.Alloc, stack, conditionalSyntax{Span: item.Span})
			continue
		}
		if item.Kind != Otherwise && item.Kind != EndWhen {
			continue
		}
		if len(stack) == 0 {
			s.error(item.Span.Start, item.Span.End, "conditional marker has no preceding when")
			continue
		}
		if item.Kind == Otherwise {
			if stack[len(stack)-1].Otherwise {
				s.error(item.Span.Start, item.Span.End, "when has more than one otherwise")
			}
			stack[len(stack)-1].Otherwise = true
		} else {
			stack = stack[:len(stack)-1]
		}
	}
	for i := range stack {
		s.error(stack[i].Span.Start, stack[i].Span.End, "when requires a matching end")
	}
	slices.Free(s.Alloc, stack)
}
