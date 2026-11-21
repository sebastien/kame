// Package script composes independently parsed language forms into a script.
package script

import (
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/source"

	"strings"
)

type ScriptItemKind int

const (
	Comment ScriptItemKind = iota
	Definition
	Rule
	Expression
	Include
	Command
	Generate
	Composition
	SessionCommand
	ModuleImport
	ModuleFrom
	ModuleExport
	DeclarationFor
	DeclarationLet
)

type ScriptItem struct {
	Kind            ScriptItemKind
	Span            source.Span
	SourceOrigin    int
	Text            string
	Definition      *definition.Definition
	Rule            *rule.Rule
	KashBody        *Script
	Expression      *expr.Expr
	Include         string
	OptionalInclude bool
	BatchInclude    bool
	Branches        []DeclarationBranch
	Loop            *DeclarationLoop
	Module          *ModuleDeclaration
	commentDepth    int
}

type DeclarationBranch struct {
	Condition *expr.Expr
	Comment   string
	Items     []ScriptItem
	Span      source.Span
}

type DeclarationLoop struct {
	Name     string
	NameSpan source.Span
	Comment  string
	Items    []ScriptItem
}

type Script struct {
	Alloc          mem.Allocator
	Source         *source.Source
	Items          []ScriptItem
	Diagnostics    []source.Diagnostic
	BorrowedSource bool
	Kash           bool
	RuleBody       bool
	start          int
}

func (s *Script) Free() {
	if s == nil {
		return
	}
	freeItems(s.Alloc, s.Items)
	mem.FreeSlice(nil, s.Diagnostics)
	if s.BorrowedSource {
		mem.Free(s.Alloc, s.Source)
	} else {
		s.Source.Free(s.Alloc)
	}
	a := s.Alloc
	*s = Script{}
	mem.Free(a, s)
}

func freeItems(a mem.Allocator, items []ScriptItem) {
	for _, item := range items {
		definition.Free(a, item.Definition)
		rule.FreeRule(a, item.Rule)
		item.KashBody.Free()
		expr.Free(a, item.Expression)
		mem.FreeString(a, item.Include)
		if item.Module != nil {
			mem.FreeString(a, item.Module.Path)
			mem.FreeSlice(nil, item.Module.Names)
		}
		for _, branch := range item.Branches {
			expr.Free(a, branch.Condition)
			freeItems(a, branch.Items)
		}
		mem.FreeSlice(nil, item.Branches)
		if item.Loop != nil {
			freeItems(a, item.Loop.Items)
			mem.Free(a, item.Loop)
		}
	}
	mem.FreeSlice(nil, items)
}

func trimStart(text string, start int, end int) int {
	for start < end && space(text[start]) {
		start++
	}
	return start
}

func (s *Script) takeDiagnostics(diags []source.Diagnostic) {
	for i := range diags {
		s.Diagnostics = append(s.Diagnostics, diags[i])
	}
	mem.FreeSlice(nil, diags)
}

func (s *Script) error(start int, end int, message string) {
	s.Diagnostics = append(s.Diagnostics, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
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

func multilineDefinitionEnd(a mem.Allocator, s *source.Source, start int, lineEnd int) int {
	text := s.Text
	equals := source.TopLevelByte(text[start:lineEnd], '=')
	if equals < 0 {
		return lineEnd
	}
	pos := start + equals + 1
	for pos < len(text) {
		if next := source.ContinuationEnd(text, pos, len(text)); next != pos {
			pos = next
		} else if space(text[pos]) || text[pos] == '\n' {
			pos++
		} else {
			break
		}
	}
	if pos == len(text) {
		return lineEnd
	}
	if pos > lineEnd {
		lineEnd = source.LogicalLineEnd(text, pos)
	}
	// Let the expression parser own all value boundaries, including multiline
	// strings and nested process captures, without another RHS classifier.
	// This probe discovers the range and discards its AST/diagnostics; definition
	// parsing then enforces exactly one expression inside that bounded range.
	prefix := expr.ParseValuePrefix(a, s, pos)
	end := prefix.End
	expr.Free(a, prefix.Expr)
	mem.FreeSlice(nil, prefix.Diagnostics)
	if end < lineEnd {
		return lineEnd
	}
	for end < len(text) && text[end] != '\n' {
		end++
	}
	return end
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
	b := strings.Builder{}
	text := s.Source.Text
	previousEnd := s.start
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
		if item.Module != nil {
			writeModuleDeclaration(&b, item)
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Include || item.Kind == Composition || item.Kind == DeclarationFor || item.Kind == Generate {
			writeSourceDeclaration(a, &b, text, item, indent, 0)
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == SessionCommand {
			words := strings.Fields(item.Text)
			column := 0
			for j := range words {
				if j != 0 {
					b.WriteByte(' ')
					column++
				}
				b.WriteString(words[j])
				column += len(words[j])
			}
			mem.FreeSlice(nil, words)
			if item.Expression != nil {
				b.WriteString(" = ")
				value := expr.FormatAt(a, item.Expression, column+3)
				b.WriteString(value)
				mem.FreeString(a, value)
			}
			previousEnd = item.Span.End
			continue
		}
		if item.Kind == Definition {
			value := ""
			if item.Definition.Expression != nil && item.Definition.Expression.Kind == expr.ValueBlock {
				if item.Definition.ExplicitDef {
					b.WriteString("def ")
				}
				b.WriteString(item.Definition.Name)
				b.WriteString(" =")
				value = expr.FormatKashControl(a, item.Definition.Expression, indent)
			} else if item.Definition.Expression != nil && item.Definition.Expression.Kind == expr.KashFunction {
				value = expr.FormatKashControl(a, item.Definition.Expression, indent)
			} else if item.Definition.Eager {
				value = expr.FormatKashStatement(a, item.Definition.Expression, indent)
			} else if s.Kash && indent != "\t" {
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
			value := FormatInspectedRule(a, item.Rule, item.KashBody, indent)
			b.WriteString(value)
			mem.FreeString(a, value)
			previousEnd = item.Span.End
			continue
		}
		if item.Expression != nil && expr.IsKashControl(item.Expression.Kind) {
			value := expr.FormatKashControl(a, item.Expression, indent)
			b.WriteString(value)
			mem.FreeString(a, value)
			previousEnd = item.Span.End
			continue
		}
		value := ""
		if s.Kash {
			value = expr.FormatKashStatement(a, item.Expression, indent)
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
	b.Reset()
	return value
}
