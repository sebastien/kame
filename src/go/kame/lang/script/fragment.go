package script

import (
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
)

// ParseFragment parses only this bounded range, in the selected language. The
// source is shared storage, not a grammar stream: syntax cannot cross fragments.
// Its owner must outlive the returned AST (whose spans retain session offsets).
func ParseFragment(a mem.Allocator, original *source.Source, lang string, start int, end int) *Script {
	s := mem.Alloc[Script](a)
	s.Alloc, s.BorrowedSource = a, true
	s.Source = mem.Alloc[source.Source](a)
	*s.Source = *original
	s.Source.Text = original.Text[:end]
	if lang == "kash" {
		parseKash(s, start)
		return s
	}
	if lang == "kmk" {
		parseScript(s, start)
		return s
	}
	if lang == "expr" {
		part := expr.ParseRange(a, s.Source, start, end, "")
		s.takeDiagnostics(part.Diagnostics)
		if part.Expr != nil {
			s.Items = append(s.Items, ScriptItem{Kind: Expression, Span: part.Expr.Span, Expression: part.Expr})
		}
		return s
	}
	// Definitions and top-level statements share expression grammar, never
	// recipe syntax.
	text := s.Source.Text
	for pos := start; pos < end; {
		for pos < end && (space(text[pos]) || text[pos] == '\n') {
			pos++
		}
		if pos == end {
			break
		}
		from := pos
		lineEnd := pos
		for lineEnd < end && text[lineEnd] != '\n' {
			lineEnd++
		}
		if text[pos] == '#' {
			_, commentEnd := trim(text, pos, lineEnd)
			s.Items = append(s.Items, ScriptItem{Kind: Comment, Text: text[pos:commentEnd], Span: source.Span{Start: pos, End: commentEnd}})
			pos = nextLine(text, lineEnd)
			continue
		}
		if following := rejectLegacyDirective(s, pos, lineEnd); following != 0 {
			pos = following
			continue
		}
		lineStart := strings.LastIndexByte(text[:pos], '\n') + 1
		if item, next, ok := s.sourceDeclaration(lineStart, pos, source.LogicalLineEnd(text, pos)); ok {
			s.Items = append(s.Items, item)
			pos = next
			continue
		}
		headerEnd := definition.ValueHeaderEnd(a, s.Source, pos)
		if headerEnd != 0 {
			if headerEnd > lineEnd {
				lineEnd = source.LogicalLineEnd(text, headerEnd)
			}
			definitionEnd := multilineDefinitionEnd(a, s.Source, pos, lineEnd)
			part := definition.ParseRange(a, s.Source, pos, definitionEnd)
			s.takeDiagnostics(part.Diagnostics)
			if part.Definition == nil {
				break
			}
			pos = part.Definition.Span.End
			s.Items = append(s.Items, ScriptItem{Kind: Definition, Span: part.Definition.Span, Definition: part.Definition})
		} else {
			part := expr.ParsePrefix(a, s.Source, pos)
			s.takeDiagnostics(part.Diagnostics)
			pos = part.End
			s.Items = append(s.Items, ScriptItem{Kind: Expression, Span: source.Span{Start: from, End: pos}, Expression: part.Expr})
		}
		for pos < end && space(text[pos]) {
			pos++
		}
		if pos < end && text[pos] != '\n' {
			last := s.Items[len(s.Items)-1].Expression
			ruleHead := last != nil && (last.Kind == expr.Name || last.Kind == expr.Path)
			if ruleHead && text[pos] == ':' && (pos+1 == end || space(text[pos+1])) && source.TopLevelByte(text[from:lineEnd], ':') == pos-from {
				s.error(pos, pos+1, "build rule syntax is not accepted in a km value script; use --lang kmk for definitions and build rules")
			} else {
				s.error(pos, pos+1, "expected statement separator")
			}
		}
		if len(s.Diagnostics) != 0 || pos <= from {
			break
		}
	}
	return s
}
