package script

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
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
	if lang == "kash" { parseKash(s, start); return s }
	if lang == "kmk" { parseScript(s, start); return s }
	if lang == "expr" {
		for start < end && (space(original.Text[start]) || original.Text[start] == '\n') { start++ }
		part := expr.ParsePrefix(a, s.Source, start)
		s.takeDiagnostics(part.Diagnostics)
		s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Expression, Span: source.Span{Start: start, End: part.End}, Expression: part.Expr})
		for part.End < end && (space(original.Text[part.End]) || original.Text[part.End] == '\n') { part.End++ }
		if part.End != end { s.error(part.End, end, "expected exactly one expression") }
		return s
	}
	// Value programs retain Kame's definition RHS classification, but top-level
	// statements are expressions (including atoms) and never recipe syntax.
	text := s.Source.Text
	for pos := start; pos < end; {
		for pos < end && (space(text[pos]) || text[pos] == '\n') { pos++ }
		if pos == end { break }
		from := pos
		lineEnd := pos
		for lineEnd < end && text[lineEnd] != '\n' { lineEnd++ }
		if comment(text, pos, lineEnd) { pos = nextLine(text, lineEnd); continue }
		if name, ok := include(text[pos:lineEnd]); ok {
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Include, Span: source.Span{Start: pos, End: lineEnd}, Include: name})
			pos = nextLine(text, lineEnd)
			continue
		}
		if definition.KashHeaderEnd(a, s.Source, pos) != 0 {
			definitionEnd := multilineDefinitionEnd(a, s.Source, pos, lineEnd)
			part := definition.ParseRange(a, s.Source, pos, definitionEnd)
			s.takeDiagnostics(part.Diagnostics)
			if part.Definition == nil { break }
			pos = part.Definition.Span.End
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Definition, Span: part.Definition.Span, Definition: part.Definition})
		} else {
			part := expr.ParsePrefix(a, s.Source, pos)
			s.takeDiagnostics(part.Diagnostics)
			pos = part.End
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Expression, Span: source.Span{Start: from, End: pos}, Expression: part.Expr})
		}
		for pos < end && space(text[pos]) { pos++ }
		if pos < end && text[pos] != '\n' { s.error(pos, pos+1, "expected statement separator") }
		if len(s.Diagnostics) != 0 || pos <= from { break }
	}
	return s
}
