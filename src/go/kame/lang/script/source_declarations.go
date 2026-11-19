package script

import (
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/rule"
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

func (s *Script) sourceDeclaration(lineStart, start, end int, inBlock bool) (ScriptItem, int, bool) {
	text := s.Source.Text
	word, operand := declarationWord(text, start, end)
	switch word {
	case "include", "include?":
		end = physicalLineEnd(text, start)
		return s.includeDeclaration(start, operand, end, word == "include?"), nextLine(text, end), true
	case "import", "from", "export":
		item := s.moduleDeclaration(word, start, operand, end)
		if lineStart != start {
			s.error(start, operand, "module declarations require a top-level line")
		}
		return item, nextLine(text, end), true
	case "if":
		item, next := s.conditionalDeclaration(lineStart, start, operand, end)
		return item, next, true
	case "for":
		item, next := s.loopDeclaration(lineStart, start, operand, end)
		return item, next, true
	case "let":
		if !inBlock {
			s.error(start, start+len(word), "let requires an enclosing declaration or action block")
		}
		item, next := s.letDeclaration(start, operand, end)
		return item, next, true
	case "generate":
		value, comment, finish := s.declarationValue(operand)
		return ScriptItem{Kind: Generate, Expression: value, Text: comment, Span: source.Span{Start: start, End: finish}}, nextLine(text, finish), true
	case "return":
		s.error(start, start+len(word), "return requires an enclosing def, value block or rule body")
		return ScriptItem{Kind: Expression, Span: source.Span{Start: start, End: end}}, nextLine(text, end), true
	}
	return ScriptItem{}, start, false
}

func (s *Script) letDeclaration(start, operand, end int) (ScriptItem, int) {
	text := s.Source.Text
	item := ScriptItem{Kind: DeclarationLet, Span: source.Span{Start: start, End: end}}
	headerEnd := definition.ValueHeaderEnd(s.Alloc, s.Source, operand)
	if headerEnd == 0 || headerEnd > end {
		s.error(operand, end, "let requires NAME = VALUE")
		return item, nextLine(text, end)
	}
	part := definition.ParseBindingHeader(s.Alloc, s.Source, operand, headerEnd)
	s.takeDiagnostics(part.Diagnostics)
	d := part.Definition
	if d.ExplicitDef || d.Function || d.Default || d.Contribution || !expr.ValidBindingName(d.Name) || d.Name == "env" {
		s.error(operand, headerEnd, "let requires one non-reserved value name and =")
	}
	value, comment, finish := s.declarationValue(trimStart(text, headerEnd, end))
	d.Eager, d.Span.End, d.Expression = true, finish, value
	item.Definition, item.Text, item.Span.End = d, comment, finish
	return item, nextLine(text, finish)
}

func (s *Script) declarationValue(start int) (*expr.Expr, string, int) {
	text := s.Source.Text
	if start == len(text) || text[start] == '\n' || text[start] == '#' {
		s.error(start, start, "expected a value expression")
		return nil, "", physicalLineEnd(text, start)
	}
	part := expr.ParseValuePrefix(s.Alloc, s.Source, start)
	s.takeDiagnostics(part.Diagnostics)
	end := physicalLineEnd(text, part.End)
	tail := trimStart(text, part.End, end)
	if tail == end {
		return part.Expr, "", end
	}
	if text[tail] == '#' {
		return part.Expr, text[tail:end], end
	}
	s.error(tail, end, "expected end of declaration statement")
	return part.Expr, "", end
}

func (s *Script) loopDeclaration(lineStart, start, operand, end int) (ScriptItem, int) {
	text := s.Source.Text
	name, inStart := declarationWord(text, operand, end)
	keyword, valueStart := declarationWord(text, inStart, end)
	loop := mem.Alloc[DeclarationLoop](s.Alloc)
	loop.Name, loop.NameSpan = name, source.Span{Start: operand, End: operand + len(name)}
	item := ScriptItem{Kind: DeclarationFor, Loop: loop, Span: source.Span{Start: start, End: end}}
	if !expr.ValidBindingName(name) || name == "env" {
		s.error(loop.NameSpan.Start, loop.NameSpan.End, "expected non-reserved loop binding name")
	}
	if keyword != "in" {
		s.error(inStart, inStart+len(keyword), "expected in followed by a value expression")
	} else {
		part := expr.ParseRange(s.Alloc, s.Source, valueStart, end, "")
		s.takeDiagnostics(part.Diagnostics)
		item.Expression = part.Expr
		if part.Expr != nil {
			tail := trimStart(text, part.Expr.Span.End, end)
			if tail < end && text[tail] == '#' {
				loop.Comment = text[tail:end]
			}
		}
	}
	body := DeclarationBranch{Span: item.Span}
	next := s.declarationBody(text[lineStart:start], end, &body)
	loop.Items, item.Span.End = body.Items, body.Span.End
	return item, next
}

func (s *Script) includeDeclaration(start, operand, end int, optional bool) ScriptItem {
	text := s.Source.Text
	item := ScriptItem{Kind: Include, OptionalInclude: optional, Span: source.Span{Start: start, End: end}}
	path, span, ok := s.literalSourcePath(operand, end)
	if ok {
		tail := trimStart(text, span.End, end)
		if tail == end || text[tail] == '#' {
			item.Include, item.Text = path, text[operand:end]
			return item
		}
		mem.FreeString(s.Alloc, path)
	}
	s.error(operand, end, "expected one literal rooted include path without globs or interpolation")
	return item
}

func (s *Script) literalSourcePath(start, end int) (string, source.Span, bool) {
	bounded := *s.Source
	bounded.Text = bounded.Text[:end]
	part := expr.ParsePrefix(s.Alloc, &bounded, start)
	defer expr.Free(s.Alloc, part.Expr)
	defer mem.FreeSlice(nil, part.Diagnostics)
	if len(part.Diagnostics) != 0 || part.Expr == nil || part.Expr.Kind != expr.Path ||
		part.Expr.Pattern != nil || len(part.Expr.Parts) != 0 ||
		strings.ContainsRune(part.Expr.Text, 0) || expr.PathHasGlob(part.Expr.Text) {
		return "", source.Span{}, false
	}
	var decoded strings.Builder
	expr.AppendPathText(&decoded, part.Expr.Text, false)
	return owned(s.Alloc, decoded.String()), part.Expr.Span, true
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
		pos := s.declarationBody(indent, end, &branch)
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

func (s *Script) declarationBody(indent string, end int, branch *DeclarationBranch) int {
	text := s.Source.Text
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
		child, next, ok := s.sourceDeclaration(pos, childStart, childEnd, true)
		if !ok && rule.DeclarationStart(text[childStart:childEnd]) {
			part := rule.ParseDeclaration(s.Alloc, s.Source, childStart, len(text))
			s.takeDiagnostics(part.Diagnostics)
			child = ScriptItem{Kind: Rule, Span: part.Rule.Span, Rule: part.Rule}
			next, ok = nextLine(text, part.Rule.Span.End), true
		}
		if !ok {
			s.error(childStart, childEnd, "expected a declaration statement")
			pos = nextLine(text, childEnd)
			continue
		}
		hasDeclaration = true
		branch.Items = append(branch.Items, child)
		branch.Span.End = child.Span.End
		pos = next
	}
	if !hasDeclaration {
		s.error(branch.Span.Start, end, "expected an indented declaration")
	}
	return pos
}

func writeSourceDeclaration(a mem.Allocator, b *strings.Builder, text string, item ScriptItem, indent string, depth int) {
	prefix := strings.Repeat(indent, depth)
	switch item.Kind {
	case Rule:
		value := FormatInspectedRule(a, item.Rule, item.KashBody, indent)
		writeDeclarationLines(b, value, prefix)
		mem.FreeString(a, value)
		return
	case Include:
		b.WriteString("include")
		if item.OptionalInclude {
			b.WriteByte('?')
		}
		b.WriteByte(' ')
		b.WriteString(strings.TrimRight(item.Text, " \t\r"))
		return
	case DeclarationFor:
		b.WriteString("for ")
		b.WriteString(item.Loop.Name)
		b.WriteString(" in ")
		value := expr.Compact(a, item.Expression)
		writeDeclarationLines(b, value, prefix)
		mem.FreeString(a, value)
		if item.Loop.Comment != "" {
			b.WriteByte(' ')
			b.WriteString(strings.TrimRight(item.Loop.Comment, " \t\r"))
		}
		writeDeclarationBody(a, b, text, item.Loop.Items, indent, depth, item.Expression.Span.End)
		return
	case DeclarationLet, Generate:
		value := item.Expression
		if item.Kind == DeclarationLet {
			b.WriteString("let ")
			b.WriteString(item.Definition.Name)
			b.WriteString(" = ")
			value = item.Definition.Expression
		} else {
			b.WriteString("generate ")
		}
		formatted := expr.Compact(a, value)
		writeDeclarationLines(b, formatted, prefix)
		mem.FreeString(a, formatted)
		if item.Text != "" {
			b.WriteByte(' ')
			b.WriteString(strings.TrimRight(item.Text, " \t\r"))
		}
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
		writeDeclarationBody(a, b, text, branch.Items, indent, depth, previous)
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

func writeDeclarationLines(b *strings.Builder, text, prefix string) {
	first := true
	for line := range strings.SplitSeq(text, "\n") {
		if !first {
			b.WriteByte('\n')
			if line != "" {
				b.WriteString(prefix)
			}
		}
		b.WriteString(line)
		first = false
	}
}

func writeDeclarationBody(a mem.Allocator, b *strings.Builder, text string, items []ScriptItem, indent string, depth int, previous int) {
	prefix := strings.Repeat(indent, depth)
	for _, child := range items {
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
}
