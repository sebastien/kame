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
	When
	Otherwise
	EndWhen
	Generate
)

type ScriptItem struct {
	Kind            ScriptItemKind
	Span            source.Span
	Text            string
	Definition      *definition.Definition
	Rule            *rule.Rule
	Expression      *expr.Expr
	Include         string
	OptionalInclude bool
	GenerateName    string
}

type Script struct {
	Alloc          mem.Allocator
	Source         *source.Source
	Items          []ScriptItem
	Diagnostics    []source.Diagnostic
	BorrowedSource bool
	Kash           bool
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
	if s.BorrowedSource {
		mem.Free(s.Alloc, s.Source)
	} else {
		s.Source.Free(s.Alloc)
	}
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

// ParseBorrowed parses without copying name or text. The caller must keep both
// strings alive and immutable until the returned Script is freed.
func ParseBorrowed(a mem.Allocator, name string, text string) *Script {
	s := mem.Alloc[Script](a)
	s.Alloc, s.Source, s.BorrowedSource = a, source.Borrow(a, name, text), true
	parseScript(s, 0)
	return s
}

func parseScript(s *Script, offset int) {
	a, text := s.Alloc, s.Source.Text
	for pos := offset; pos < len(text); {
		lineEnd := source.LogicalLineEnd(text, pos)
		start, end := trim(text, pos, lineEnd)
		if start == end {
			pos = nextLine(text, lineEnd)
			continue
		}
		if comment(text, start, end) {
			lineEnd = pos
			for lineEnd < len(text) && text[lineEnd] != '\n' {
				lineEnd++
			}
			start, end = trim(text, pos, lineEnd)
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Comment, Text: text[start:end], Span: source.Span{Start: start, End: end}})
			pos = nextLine(text, lineEnd)
			continue
		}
		if start == pos {
			if following := parseConditional(s, start, end); following != 0 {
				pos = following
				continue
			}
		}
		if includePath, ok := include(text[start:end]); ok {
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Include, Span: source.Span{Start: start, End: end}, Include: includePath, OptionalInclude: strings.HasPrefix(text[start:end], "include?")})
			pos = nextLine(text, lineEnd)
			continue
		}
		generated := generatedHeader(text, start, end)
		if generated.OK {
			part := expr.ParsePrefix(a, s.Source, generated.ExpressionStart)
			s.takeDiagnostics(part.Diagnostics)
			if part.Expr == nil {
				s.error(start, end, "generated declaration requires an expression")
				pos = nextLine(text, lineEnd)
				continue
			}
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Generate, GenerateName: generated.Name, Expression: part.Expr, Span: source.Span{Start: start, End: part.End}})
			pos = nextLine(text, part.End)
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
		// A canonical long function header can span physical lines.
		headerEnd := definition.ValueHeaderEnd(a, s.Source, start)
		if text[start] == '(' && headerEnd > end {
			definitionEnd := multilineDefinitionEnd(a, s.Source, start, source.LogicalLineEnd(text, headerEnd))
			part := definition.ParseRange(a, s.Source, start, definitionEnd)
			s.takeDiagnostics(part.Diagnostics)
			s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Definition, Span: source.Span{Start: start, End: definitionEnd}, Definition: part.Definition})
			pos = nextLine(text, definitionEnd)
			continue
		}
		if rule.HasRuleSeparator(text[start:end]) {
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
		if prefix.End > end {
			lineEnd = source.LogicalLineEnd(text, prefix.End)
			_, end = trim(text, start, lineEnd)
		}
		if prefix.End != end {
			s.error(prefix.End, end, "unexpected top-level input")
		}
		s.Items = slices.Append(a, s.Items, ScriptItem{Kind: Expression, Span: source.Span{Start: start, End: end}, Expression: prefix.Expr})
		pos = nextLine(text, lineEnd)
	}
	validateConditionals(s)
}

type generatedHeaderResult struct {
	Name            string
	ExpressionStart int
	OK              bool
}

func generatedHeader(text string, start int, end int) generatedHeaderResult {
	if end-start < len("generate ") || text[start:start+len("generate")] != "generate" || !space(text[start+len("generate")]) {
		return generatedHeaderResult{}
	}
	declarationStart := trimStart(text, start+len("generate"), end)
	equal := topLevel(text[declarationStart:end], '=')
	if equal < 0 {
		return generatedHeaderResult{}
	}
	nameStart, nameEnd := trim(text, declarationStart, declarationStart+equal)
	name := text[nameStart:nameEnd]
	if !validGeneratedName(name) {
		return generatedHeaderResult{}
	}
	expressionStart, _ := trim(text, declarationStart+equal+1, end)
	return generatedHeaderResult{Name: name, ExpressionStart: expressionStart, OK: true}
}

func validGeneratedName(name string) bool {
	if name == "" {
		return false
	}
	for i := range name {
		b := name[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || (i != 0 && ((b >= '0' && b <= '9') || b == '-')) {
			continue
		}
		return false
	}
	return true
}

func trimStart(text string, start int, end int) int {
	for start < end && space(text[start]) {
		start++
	}
	return start
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
	prefix := 7
	if strings.HasPrefix(text, "include?") {
		prefix = 8
	}
	if len(text) < prefix+2 || text[:7] != "include" || !space(text[prefix]) {
		return "", false
	}
	path := text[prefix+1:]
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
	for pos < len(text) && (space(text[pos]) || text[pos] == '\n') {
		pos++
	}
	if pos == len(text) {
		return lineEnd
	}
	if pos > lineEnd {
		lineEnd = source.LogicalLineEnd(text, pos)
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
	if end < lineEnd {
		return lineEnd
	}
	for end < len(text) && text[end] != '\n' {
		end++
	}
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
		if item.Kind == When {
			b.WriteString("when ")
			value := expr.FormatAt(a, item.Expression, 5)
			b.WriteString(value)
			mem.FreeString(a, value)
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Otherwise || item.Kind == EndWhen {
			if item.Kind == Otherwise {
				b.WriteString("otherwise")
			} else {
				b.WriteString("end")
			}
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Include {
			if item.OptionalInclude {
				b.WriteString("include? ")
			} else {
				b.WriteString("include ")
			}
			b.WriteString(item.Include)
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Generate {
			b.WriteString("generate ")
			b.WriteString(item.GenerateName)
			b.WriteString(" = ")
			value := expr.FormatAt(a, item.Expression, len("generate ")+len(item.GenerateName)+3)
			b.WriteString(value)
			mem.FreeString(a, value)
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Definition {
			value := ""
			if s.Kash {
				value = definition.FormatCompact(a, item.Definition)
			} else {
				value = definition.Format(a, item.Definition)
			}
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
		if item.Expression != nil && (item.Expression.Kind == expr.KashIf || item.Expression.Kind == expr.KashMatch) {
			value := expr.FormatKashControl(a, item.Expression, indent)
			b.WriteString(value)
			mem.FreeString(a, value)
			previousEnd = item.Span.End
			continue
		}
		value := ""
		if s.Kash {
			value = expr.Compact(a, item.Expression)
		} else {
			value = expr.Format(a, item.Expression)
		}
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
