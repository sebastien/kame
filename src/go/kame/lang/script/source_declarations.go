package script

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
)

// IsKashSource reports whether the path names a Kash source file.
func IsKashSource(path string) bool {
	return strings.HasSuffix(path, ".kash") || strings.HasSuffix(path, ".ksh")
}

func declarationWord(text string, start, end int) (string, int) {
	stop := start
	for stop < end && !space(text[stop]) && text[stop] != '#' && text[stop] != '\n' {
		stop++
	}
	return text[start:stop], trimStart(text, stop, end)
}

func physicalLineEnd(text string, start int) int {
	if size := strings.IndexByte(text[start:], '\n'); size >= 0 {
		return start + size
	}
	return len(text)
}

func (s *Script) sourceDeclaration(lineStart, start, end int) (ScriptItem, int, bool) {
	text := s.Source.Text
	word, operand := declarationWord(text, start, end)
	switch word {
	case "include", "include?":
		end = physicalLineEnd(text, start)
		return s.includeDeclaration(start, operand, end, word == "include?"), nextLine(text, end), true
	case "if":
		item, next := s.conditionalDeclaration(lineStart, start, operand, end)
		return item, next, true
	}
	return ScriptItem{}, start, false
}

func (s *Script) includeDeclaration(start, operand, end int, optional bool) ScriptItem {
	text := s.Source.Text
	item := ScriptItem{Kind: Include, OptionalInclude: optional, Span: source.Span{Start: start, End: end}}
	bounded := *s.Source
	bounded.Text = text[:end]
	part := expr.ParsePrefix(s.Alloc, &bounded, operand)
	defer expr.Free(s.Alloc, part.Expr)
	tail := trimStart(text, part.End, end)
	valid := len(part.Diagnostics) == 0 && part.Expr != nil && part.Expr.Kind == expr.Path &&
		part.Expr.Pattern == nil && len(part.Expr.Parts) == 0 && (tail == end || text[tail] == '#')
	mem.FreeSlice(nil, part.Diagnostics)
	if valid && !strings.ContainsRune(part.Expr.Text, '\x00') && !expr.PathHasGlob(part.Expr.Text) {
		var decoded strings.Builder
		expr.AppendPathText(&decoded, part.Expr.Text, false)
		item.Include = owned(s.Alloc, decoded.String())
		item.Text = text[operand:end]
		return item
	}
	s.error(operand, end, "expected one literal rooted include path without globs or interpolation")
	return item
}

func (s *Script) conditionalDeclaration(lineStart, start, operand, end int) (ScriptItem, int) {
	text := s.Source.Text
	indent := text[lineStart:start]
	item := ScriptItem{Kind: Composition, Span: source.Span{Start: start}}
	keyword := "if"
	for {
		branch := DeclarationBranch{Span: source.Span{Start: start, End: end}}
		if keyword != "else" {
			part := expr.ParseRange(s.Alloc, s.Source, operand, end, "")
			s.takeDiagnostics(part.Diagnostics)
			branch.Condition = part.Expr
			if part.Expr != nil {
				tail := trimStart(text, part.Expr.Span.End, end)
				if tail < end && text[tail] == '#' {
					branch.Comment = text[tail:end]
				}
			}
		} else if operand < end {
			if text[operand] == '#' {
				branch.Comment = text[operand:end]
			} else {
				s.error(operand, end, "else takes no condition")
			}
		}
		pos := nextLine(text, end)
		bodyIndent := ""
		hasDeclaration := false
		for pos < len(text) {
			lineEnd := physicalLineEnd(text, pos)
			childStart, childEnd := trim(text, pos, lineEnd)
			if childStart == childEnd {
				pos = nextLine(text, lineEnd)
				continue
			}
			prefix := text[pos:childStart]
			if len(prefix) <= len(indent) || !strings.HasPrefix(prefix, indent) {
				break
			}
			if text[childStart] == '#' {
				branch.Items = append(branch.Items, ScriptItem{Kind: Comment, Text: text[childStart:childEnd], Span: source.Span{Start: childStart, End: childEnd}})
				branch.Span.End = childEnd
				pos = nextLine(text, lineEnd)
				continue
			}
			if bodyIndent == "" {
				bodyIndent = prefix
			}
			if prefix != bodyIndent || strings.Contains(prefix, " ") && strings.Contains(prefix, "\t") {
				s.error(pos, childStart, "unexpected declaration indentation")
			}
			childEnd = source.LogicalLineEnd(text, pos)
			child, next, ok := s.sourceDeclaration(pos, childStart, childEnd)
			if !ok {
				s.error(childStart, childEnd, "expected an include or conditional declaration")
				pos = nextLine(text, childEnd)
				continue
			}
			hasDeclaration = true
			branch.Items = append(branch.Items, child)
			branch.Span.End = child.Span.End
			pos = next
		}
		if !hasDeclaration {
			s.error(start, end, "expected an indented declaration")
		}
		item.Branches = append(item.Branches, branch)
		item.Span.End = branch.Span.End
		if keyword == "else" || pos >= len(text) {
			return item, pos
		}
		end = source.LogicalLineEnd(text, pos)
		start, _ = trim(text, pos, end)
		if text[pos:start] != indent {
			return item, pos
		}
		keyword, operand = declarationWord(text, start, end)
		if keyword != "elif" && keyword != "else" {
			return item, pos
		}
	}
}

func writeSourceDeclaration(a mem.Allocator, b *strings.Builder, text string, item ScriptItem, indent string, depth int) {
	prefix := strings.Repeat(indent, depth)
	if item.Kind == Include {
		b.WriteString("include")
		if item.OptionalInclude {
			b.WriteByte('?')
		}
		b.WriteByte(' ')
		b.WriteString(strings.TrimRight(item.Text, " \t\r"))
		return
	}
	for i, branch := range item.Branches {
		if i == 0 {
			b.WriteString("if ")
		} else if branch.Condition == nil {
			b.WriteString("else")
		} else {
			b.WriteString("elif ")
		}
		previous := branch.Span.Start
		if branch.Condition != nil {
			value := expr.Compact(a, branch.Condition)
			b.WriteString(value)
			mem.FreeString(a, value)
			previous = branch.Condition.Span.End
		}
		if branch.Comment != "" {
			b.WriteByte(' ')
			b.WriteString(strings.TrimRight(branch.Comment, " \t\r"))
		}
		for _, child := range branch.Items {
			for j := previous; j < child.Span.Start; j++ {
				if text[j] == '\n' {
					b.WriteByte('\n')
				}
			}
			b.WriteString(prefix)
			b.WriteString(indent)
			if child.Kind == Comment {
				b.WriteString(child.Text)
			} else {
				writeSourceDeclaration(a, b, text, child, indent, depth+1)
			}
			previous = child.Span.End
		}
		if i+1 < len(item.Branches) {
			for j := branch.Span.End; j < item.Branches[i+1].Span.Start; j++ {
				if text[j] == '\n' {
					b.WriteByte('\n')
				}
			}
			b.WriteString(prefix)
		}
	}
}
