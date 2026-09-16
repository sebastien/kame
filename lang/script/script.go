// Package script composes independently parsed language forms into a script.
package script

import (
	"littlemake/lang/definition"
	"littlemake/lang/expr"
	"littlemake/lang/rule"
	"littlemake/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type ScriptItemKind int

const (
	Comment ScriptItemKind = iota
	Definition
	Rule
	Expression
)

type ScriptItem struct {
	Kind       ScriptItemKind
	Span       source.Span
	Text       string
	Definition *definition.Definition
	Rule       *rule.Rule
	Expression *expr.Expr
}

type Script struct {
	Alloc       mem.Allocator
	Source      *source.Source
	Items       []ScriptItem
	Diagnostics []source.Diagnostic
}

func (s *Script) Free() {
	if s == nil { return }
	for i := range s.Items {
		definition.Free(s.Alloc, s.Items[i].Definition)
		rule.FreeRule(s.Alloc, s.Items[i].Rule)
		expr.Free(s.Alloc, s.Items[i].Expression)
	}
	slices.Free(s.Alloc, s.Items)
	slices.Free(s.Alloc, s.Diagnostics)
	s.Source.Free(s.Alloc)
	a := s.Alloc
	*s = Script{}
	mem.Free(a, s)
}

func Parse(a mem.Allocator, name string, text string) *Script {
	s := mem.Alloc[Script](a)
	s.Alloc, s.Source = a, source.New(a, name, text)
	for pos := 0; pos < len(text); {
		lineEnd := pos
		for lineEnd < len(text) && text[lineEnd] != '\n' { lineEnd++ }
		start, end := trim(text, pos, lineEnd)
		if start == end { pos = nextLine(text, lineEnd); continue }
		if comment(text, start, end) {
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Comment, Text: text[start:end], Span: source.Span{Start: start, End: end}})
			pos = nextLine(text, lineEnd); continue
		}
		if text[pos] == ' ' || text[pos] == '\t' {
			s.error(pos, lineEnd, "indented text has no preceding rule")
			pos = nextLine(text, lineEnd); continue
		}
		if topLevel(text[start:end], '=') >= 0 {
			definitionEnd := multilineDefinitionEnd(text, start, end)
			part := definition.ParseRange(a, s.Source, start, definitionEnd)
			s.takeDiagnostics(part.Diagnostics)
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Definition, Span: source.Span{Start: start, End: definitionEnd}, Definition: part.Definition})
			pos = nextLine(text, definitionEnd); continue
		}
		if topLevel(text[start:end], ':') >= 0 {
			part := rule.ParseRuleRange(a, s.Source, start, len(text))
			s.takeDiagnostics(part.Diagnostics)
			if part.Rule == nil { pos = nextLine(text, lineEnd); continue }
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Rule, Span: part.Rule.Span, Rule: part.Rule})
			pos = part.Rule.Span.End
			if pos <= lineEnd { pos = lineEnd }
			pos = nextLine(text, pos)
			continue
		}
		prefix := expr.ParsePrefix(a, s.Source, start)
		s.takeDiagnostics(prefix.Diagnostics)
		if prefix.End != end { s.error(prefix.End, end, "unexpected top-level input") }
		s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Expression, Span: source.Span{Start: start, End: end}, Expression: prefix.Expr})
		pos = nextLine(text, lineEnd)
	}
	return s
}

func (s *Script) takeDiagnostics(diags []source.Diagnostic) {
	for i := range diags { s.Diagnostics = slices.Append(s.Alloc, s.Diagnostics, diags[i]) }
	slices.Free(s.Alloc, diags)
}

func (s *Script) error(start int, end int, message string) {
	s.Diagnostics = slices.Append(s.Alloc, s.Diagnostics, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

func nextLine(text string, pos int) int { if pos < len(text) && text[pos] == '\n' { return pos+1 }; return pos }
func space(b byte) bool { return b == ' ' || b == '\t' || b == '\r' }
func trim(text string, start int, end int) (int, int) { for start < end && space(text[start]) { start++ }; for start < end && space(text[end-1]) { end-- }; return start, end }
func comment(text string, start int, end int) bool { return text[start] == '#' || (start+1 < end && text[start] == '/' && text[start+1] == '/') }

func multilineDefinitionEnd(text string, start int, lineEnd int) int {
	equals := topLevel(text[start:lineEnd], '=')
	if equals < 0 { return lineEnd }
	pos := start + equals + 1
	for pos < len(text) && space(text[pos]) { pos++ }
	if pos == len(text) || (text[pos] != '(' && text[pos] != '[') { return lineEnd }
	depth, quote := 0, false
	for pos < len(text) {
		b := text[pos]
		if quote {
			if b == '\\' { pos += 2; continue }
			if b == '"' { quote = false }
		} else if b == '"' { quote = true
		} else if b == '(' || b == '[' { depth++
		} else if b == ')' || b == ']' {
			depth--
			if depth == 0 {
				pos++
				for pos < len(text) && text[pos] != '\n' { pos++ }
				return pos
			}
		}
		pos++
	}
	return lineEnd
}

func topLevel(text string, want byte) int {
	depth, quote := 0, false
	for i := 0; i < len(text); i++ {
		if quote { if text[i] == '\\' { i++ } else if text[i] == '"' { quote = false }; continue }
		if text[i] == '"' { quote = true
		} else if text[i] == '(' || text[i] == '[' || text[i] == '{' { depth++
		} else if text[i] == ')' || text[i] == ']' || text[i] == '}' { depth--
		} else if text[i] == want && depth == 0 { return i }
	}
	return -1
}

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 { return "" }
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

// Format returns allocator-owned canonical script text with one terminal LF.
func Format(a mem.Allocator, s *Script) string {
	b := strings.NewBuilder(a)
	for i := range s.Items {
		if i != 0 { b.WriteByte('\n') }
		item := s.Items[i]
		if item.Kind == Comment { b.WriteString(item.Text); continue }
		if item.Kind == Definition {
			value := definition.Format(a, item.Definition); b.WriteString(value); mem.FreeString(a, value); continue
		}
		if item.Kind == Rule {
			value := rule.FormatRule(a, item.Rule); b.WriteString(value); mem.FreeString(a, value); continue
		}
		value := expr.Format(a, item.Expression); b.WriteString(value); mem.FreeString(a, value)
	}
	b.WriteByte('\n')
	value := owned(a, b.String()); b.Free(); return value
}
