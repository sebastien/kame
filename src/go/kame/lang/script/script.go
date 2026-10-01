// Package script composes independently parsed language forms into a script.
package script

import (
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/source"
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
	Include
	Command
)

type ScriptItem struct {
	Kind       ScriptItemKind
	Span       source.Span
	Text       string
	Definition *definition.Definition
	Rule       *rule.Rule
	Expression *expr.Expr
	Include    string
}

type Script struct {
	Alloc       mem.Allocator
	Source      *source.Source
	Items       []ScriptItem
	Diagnostics []source.Diagnostic
	BorrowedSource bool
}

func (s *Script) Free() {
	if s == nil {
		return
	}
	for i := range s.Items {
		definition.Free(s.Alloc, s.Items[i].Definition)
		rule.FreeRule(s.Alloc, s.Items[i].Rule)
		expr.Free(s.Alloc, s.Items[i].Expression)
	}
	slices.Free(s.Alloc, s.Items)
	slices.Free(s.Alloc, s.Diagnostics)
	if s.BorrowedSource { mem.Free(s.Alloc, s.Source) } else { s.Source.Free(s.Alloc) }
	a := s.Alloc
	*s = Script{}
	mem.Free(a, s)
}

func Parse(a mem.Allocator, name string, text string) *Script {
	s := mem.Alloc[Script](a)
	s.Alloc, s.Source = a, source.New(a, name, text)
	parseScript(s, 0)
	return s
}

func parseScript(s *Script, offset int) {
	a, text := s.Alloc, s.Source.Text
	for pos := offset; pos < len(text); {
		lineEnd := pos
		for lineEnd < len(text) && text[lineEnd] != '\n' {
			lineEnd++
		}
		start, end := trim(text, pos, lineEnd)
		if start == end {
			pos = nextLine(text, lineEnd)
			continue
		}
		if comment(text, start, end) {
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Comment, Text: text[start:end], Span: source.Span{Start: start, End: end}})
			pos = nextLine(text, lineEnd)
			continue
		}
		if includePath, ok := include(text[start:end]); ok {
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Include, Span: source.Span{Start: start, End: end}, Include: includePath})
			pos = nextLine(text, lineEnd)
			continue
		}
		if text[pos] == ' ' || text[pos] == '\t' {
			s.error(pos, lineEnd, "indented text has no preceding rule")
			pos = nextLine(text, lineEnd)
			continue
		}
		if topLevel(text[start:end], '=') >= 0 {
			definitionEnd := multilineDefinitionEnd(a, s.Source, start, end)
			part := definition.ParseRange(a, s.Source, start, definitionEnd)
			s.takeDiagnostics(part.Diagnostics)
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Definition, Span: source.Span{Start: start, End: definitionEnd}, Definition: part.Definition})
			pos = nextLine(text, definitionEnd)
			continue
		}
		if topLevel(text[start:end], ':') >= 0 {
			part := rule.ParseRuleRange(a, s.Source, start, len(text))
			s.takeDiagnostics(part.Diagnostics)
			if part.Rule == nil {
				pos = nextLine(text, lineEnd)
				continue
			}
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Rule, Span: part.Rule.Span, Rule: part.Rule})
			pos = part.Rule.Span.End
			if pos <= lineEnd {
				pos = lineEnd
			}
			pos = nextLine(text, pos)
			continue
		}
		prefix := expr.ParsePrefix(a, s.Source, start)
		s.takeDiagnostics(prefix.Diagnostics)
		if prefix.End != end {
			s.error(prefix.End, end, "unexpected top-level input")
		}
		s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Expression, Span: source.Span{Start: start, End: end}, Expression: prefix.Expr})
		pos = nextLine(text, lineEnd)
	}
}

func (s *Script) takeDiagnostics(diags []source.Diagnostic) {
	for i := range diags {
		s.Diagnostics = slices.Append(s.Alloc, s.Diagnostics, diags[i])
	}
	slices.Free(s.Alloc, diags)
}

func (s *Script) error(start int, end int, message string) {
	s.Diagnostics = slices.Append(s.Alloc, s.Diagnostics, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

func nextLine(text string, pos int) int {
	if pos < len(text) && text[pos] == '\n' {
		return pos + 1
	}
	return pos
}
func space(b byte) bool { return b == ' ' || b == '\t' || b == '\r' }
func trim(text string, start int, end int) (int, int) {
	for start < end && space(text[start]) {
		start++
	}
	for start < end && space(text[end-1]) {
		end--
	}
	return start, end
}
func comment(text string, start int, end int) bool {
	return text[start] == '#' || (start+1 < end && text[start] == '/' && text[start+1] == '/')
}
func include(text string) (string, bool) {
	if len(text) < 9 || text[:7] != "include" || !space(text[7]) {
		return "", false
	}
	path := text[8:]
	for len(path) != 0 && space(path[0]) {
		path = path[1:]
	}
	if len(path) == 0 {
		return "", false
	}
	if path[0] == '"' && len(path) >= 2 && path[len(path)-1] == '"' {
		path = path[1 : len(path)-1]
	}
	for i := 0; i < len(path); i++ {
		if space(path[i]) {
			return "", false
		}
	}
	return path, true
}

func multilineDefinitionEnd(a mem.Allocator, s *source.Source, start int, lineEnd int) int {
	text := s.Text
	equals := topLevel(text[start:lineEnd], '=')
	if equals < 0 {
		return lineEnd
	}
	pos := start + equals + 1
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	if pos == len(text) {
		return lineEnd
	}
	if text[pos] == '"' {
		// Verbatim multi-line literal: 3+ quotes open raw until same-length run.
		n := 0
		for pos+n < len(text) && text[pos+n] == '"' {
			n++
		}
		if n >= 3 {
			cur := pos + n
			for cur < len(text) {
				if text[cur] != '"' {
					cur++
					continue
				}
				run := 0
				for cur+run < len(text) && text[cur+run] == '"' {
					run++
				}
				if run == n {
					cur += n
					for cur < len(text) && text[cur] != '\n' {
						cur++
					}
					return cur
				}
				cur += run
			}
			return lineEnd
		}
		return lineEnd
	}
	if text[pos] != '(' && text[pos] != '[' && !(pos+1 < len(text) && text[pos:pos+2] == "$(") {
		return lineEnd
	}
	// Delegate nested delimiter ownership to the expression/process parsers;
	// shell-like escaped parentheses are not expression delimiters.
	prefix := expr.ParsePrefix(a, s, pos)
	end := prefix.End
	expr.Free(a, prefix.Expr)
	slices.Free(a, prefix.Diagnostics)
	if end < lineEnd { return lineEnd }
	for end < len(text) && text[end] != '\n' { end++ }
	return end
}

func topLevel(text string, want byte) int {
	depth, quote := 0, false
	for i := 0; i < len(text); i++ {
		if quote {
			if text[i] == '\\' {
				i++
			} else if text[i] == '"' {
				quote = false
			}
			continue
		}
		if text[i] == '"' {
			quote = true
		} else if text[i] == '(' || text[i] == '[' || text[i] == '{' {
			depth++
		} else if text[i] == ')' || text[i] == ']' || text[i] == '}' {
			depth--
		} else if text[i] == want && depth == 0 {
			return i
		}
	}
	return -1
}

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

// Format returns allocator-owned canonical script text with a terminal LF and
// the source's blank-line counts between items and at the script edges.
func Format(a mem.Allocator, s *Script) string {
	return FormatWithIndent(a, s, "\t")
}

// FormatWithIndent returns allocator-owned canonical script text with a
// terminal LF and the source's blank-line counts between items and at the
// script edges.
func FormatWithIndent(a mem.Allocator, s *Script, indent string) string {
	b := strings.NewBuilder(a)
	text := s.Source.Text
	previousEnd := 0
	for i := range s.Items {
		item := s.Items[i]
		gapStart, gapEnd := previousEnd, item.Span.Start
		if gapStart < 0 {
			gapStart = 0
		}
		if gapEnd > len(text) {
			gapEnd = len(text)
		}
		if gapStart > gapEnd {
			gapStart = gapEnd
		}
		breaks := 0
		for j := gapStart; j < gapEnd; j++ {
			if text[j] == '\n' {
				breaks++
			}
		}
		if i != 0 && breaks == 0 {
			breaks = 1
		}
		for j := 0; j < breaks; j++ {
			b.WriteByte('\n')
		}
		if item.Kind == Comment {
			b.WriteString(item.Text)
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Include {
			b.WriteString("include ")
			b.WriteString(item.Include)
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Definition {
			value := definition.Format(a, item.Definition)
			b.WriteString(value)
			mem.FreeString(a, value)
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Rule {
			value := rule.FormatRuleWithIndent(a, item.Rule, indent)
			b.WriteString(value)
			mem.FreeString(a, value)
			previousEnd = item.Span.End
			continue
		}
		value := expr.Format(a, item.Expression)
		b.WriteString(value)
		mem.FreeString(a, value)
		previousEnd = item.Span.End
	}
	trailing := 0
	for j := previousEnd; j < len(text); j++ {
		if text[j] == '\n' {
			trailing++
		}
	}
	if trailing < 1 {
		trailing = 1
	}
	for j := 0; j < trailing; j++ {
		b.WriteByte('\n')
	}
	value := owned(a, b.String())
	b.Free()
	return value
}
