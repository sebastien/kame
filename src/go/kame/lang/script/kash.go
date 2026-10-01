package script

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// ParseKash composes strict Kame definitions and structured Kash command graphs.
// Existing .kmk parsing and shell recipe text remain entirely separate.
func ParseKash(a mem.Allocator, name string, text string) *Script {
	s := mem.Alloc[Script](a)
	s.Alloc, s.Source = a, source.New(a, name, text)
	parseKash(s, 0)
	return s
}

func parseKash(s *Script, offset int) {
	a, text := s.Alloc, s.Source.Text
	for pos := offset; pos < len(text); {
		for pos < len(text) && (space(text[pos]) || text[pos] == '\n' || text[pos] == ';') { pos++ }
		if pos == len(text) { break }
		if text[pos] == '#' {
			end := pos
			for end < len(text) && text[end] != '\n' { end++ }
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Comment, Text: text[pos:end], Span: source.Span{Start: pos, End: end}})
			pos = end
			continue
		}
		start := pos
		if headerEnd := definition.KashHeaderEnd(a, s.Source, start); headerEnd != 0 {
			valueStart := headerEnd
			for valueStart < len(text) && space(text[valueStart]) { valueStart++ }
			if valueStart == len(text) || text[valueStart] == '\n' || text[valueStart] == ';' || text[valueStart] == '#' {
				s.error(headerEnd, valueStart, "expected Kash definition value")
				break
			}
			part := definition.ParseKashPrefix(a, s.Source, start)
			s.takeDiagnostics(part.Diagnostics)
			if part.Definition == nil { break }
			pos = part.Definition.Span.End
			for i := range s.Items {
				if s.Items[i].Kind == Definition && s.Items[i].Definition.Name == part.Definition.Name { s.error(start, pos, "duplicate Kash definition name") }
			}
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Definition, Span: part.Definition.Span, Definition: part.Definition})
			for pos < len(text) && space(text[pos]) { pos++ }
			if pos < len(text) && text[pos] != '\n' && text[pos] != ';' && text[pos] != '#' {
				s.error(pos, pos+1, "unexpected Kash definition value; expected statement separator")
			}
		} else {
			wordEnd := start
			for wordEnd < len(text) && !space(text[wordEnd]) && text[wordEnd] != '\n' && text[wordEnd] != ';' { wordEnd++ }
			if text[start:wordEnd] == "if" || text[start:wordEnd] == "match" {
				s.error(start, wordEnd, "Kash control blocks are not yet supported")
				break
			}
			part := expr.ParseCommandPrefix(a, s.Source, start)
			s.takeDiagnostics(part.Diagnostics)
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Command, Span: part.Expr.Span, Expression: part.Expr})
			pos = part.End
		}
		if len(s.Diagnostics) != 0 || pos <= start { break }
	}
}
