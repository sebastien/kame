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
	s.Kash = true
	p := kashParser{Script: s, Pos: offset}
	s.Items = p.sequence(0)
}

type kashParser struct { Script *Script; Pos int; Style byte; Unit int }

func (p *kashParser) sequence(level int) []ScriptItem {
	s, text := p.Script, p.Script.Source.Text
	var items []ScriptItem
	for p.Pos < len(text) && len(s.Diagnostics) == 0 {
		for p.Pos < len(text) && (space(text[p.Pos]) || text[p.Pos] == '\n' || text[p.Pos] == ';') { p.Pos++ }
		if p.Pos == len(text) { break }
		start := p.Pos
		if text[start] == '#' {
			for p.Pos < len(text) && text[p.Pos] != '\n' { p.Pos++ }
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Comment, Text: text[start:p.Pos], Span: source.Span{Start: start, End: p.Pos}})
			continue
		}
		indent := level
		lineStart := start
		for lineStart > 0 && text[lineStart-1] != '\n' { lineStart-- }
		leading := true
		for i := lineStart; i < start; i++ { if !space(text[i]) { leading = false; break } }
		if leading { indent = p.indentation(start) }
		if indent < level { break }
		if indent != level { s.error(start, start+1, "unexpected Kash indentation"); break }
		wordEnd := start
		for wordEnd < len(text) && !space(text[wordEnd]) && text[wordEnd] != '\n' && text[wordEnd] != ';' { wordEnd++ }
		word := text[start:wordEnd]
		if word == "elif" || word == "else" { if level != 0 { break }; s.error(start, wordEnd, "orphan Kash branch keyword"); break }
		if word == "case" { s.error(start, wordEnd, "orphan Kash case keyword"); break }
		if word == "if" || word == "match" {
			lineStart := start
			for lineStart > 0 && text[lineStart-1] != '\n' { lineStart-- }
			for i := lineStart; i < start; i++ { if !space(text[i]) { s.error(start, wordEnd, "control headers must occupy their own line") } }
			var e *expr.Expr
			if word == "match" { e = p.matchBlock(level, start, wordEnd) } else { e = p.ifBlock(level, start) }
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Command, Span: e.Span, Expression: e})
			continue
		}
		if headerEnd := definition.KashHeaderEnd(s.Alloc, s.Source, start); headerEnd != 0 {
			valueStart := headerEnd
			for valueStart < len(text) && space(text[valueStart]) { valueStart++ }
			if valueStart == len(text) || text[valueStart] == '\n' || text[valueStart] == ';' || text[valueStart] == '#' { s.error(headerEnd, valueStart, "expected Kash definition value"); break }
			part := definition.ParseKashPrefix(s.Alloc, s.Source, start)
			s.takeDiagnostics(part.Diagnostics)
			if part.Definition == nil { break }
			if part.Definition.Name == "env" { s.error(start, part.Definition.Span.End, "env is reserved in Kash") }
			for i := range part.Definition.Parameters { if part.Definition.Parameters[i].Name == "env" { parameter := part.Definition.Parameters[i]; s.error(parameter.Span.Start, parameter.Span.End, "env is reserved in Kash") } }
			p.Pos = part.Definition.Span.End
			for i := range items { if items[i].Kind == Definition && items[i].Definition.Name == part.Definition.Name { s.error(start, p.Pos, "duplicate Kash definition name") } }
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Definition, Span: part.Definition.Span, Definition: part.Definition})
			end := p.Pos
			for end < len(text) && space(text[end]) { end++ }
			if end < len(text) && text[end] != '\n' && text[end] != ';' && text[end] != '#' { s.error(end, end+1, "unexpected Kash definition value; expected statement separator") }
		} else {
			part := expr.ParseCommandPrefix(s.Alloc, s.Source, start)
			s.takeDiagnostics(part.Diagnostics)
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Command, Span: part.Expr.Span, Expression: part.Expr})
			p.Pos = part.End
		}
		if p.Pos <= start { break }
	}
	return items
}

func (p *kashParser) indentation(pos int) int {
	text := p.Script.Source.Text
	start := pos
	for start > 0 && text[start-1] != '\n' { start-- }
	end := start
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') { end++ }
	if end == start { return 0 }
	style := text[start]
	for i := start; i < end; i++ { if text[i] != style { p.Script.error(start, end, "mixed Kash indentation"); return -1 } }
	if p.Style == 0 { p.Style = style; p.Unit = end-start; if style == '\t' { p.Unit = 1 } }
	if p.Style != style || (end-start)%p.Unit != 0 { p.Script.error(start, end, "inconsistent Kash indentation"); return -1 }
	return (end-start)/p.Unit
}

func (p *kashParser) ifBlock(level int, start int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = expr.KashIf, start
	seenElse := false
	for len(s.Diagnostics) == 0 {
		branchStart := p.Pos
		wordEnd := branchStart
		for wordEnd < len(text) && !space(text[wordEnd]) && text[wordEnd] != '\n' { wordEnd++ }
		keyword := text[branchStart:wordEnd]
		if seenElse { s.error(branchStart, wordEnd, "branch after else"); break }
		branch := mem.Alloc[expr.Expr](s.Alloc)
		branch.Kind, branch.Text, branch.Span.Start = expr.KashBranch, keyword, branchStart
		e.Items = slices.Append(s.Alloc, e.Items, branch)
		end := wordEnd
		for end < len(text) && text[end] != '\n' { end++ }
		valueStart := wordEnd
		for valueStart < end && space(text[valueStart]) { valueStart++ }
		if keyword == "else" {
			seenElse = true
			if valueStart < end && text[valueStart] != '#' { s.error(valueStart, end, "else header takes no condition") }
		} else {
			if valueStart == end || text[valueStart] == '#' { s.error(wordEnd, end, "expected Kash condition") } else {
				view := mem.Alloc[source.Source](s.Alloc)
				*view = *s.Source
				view.Text = text[:end]
				var condition expr.Prefix
				if valueStart+1 < end && text[valueStart:valueStart+2] == "@(" { condition = expr.ParseKashValuePrefix(s.Alloc, view, valueStart) } else {
					condition = expr.ParseCommandPrefix(s.Alloc, view, valueStart)
					condition.Expr.Kind = expr.CommandTest
					if condition.Expr.Async || condition.Expr.AcceptExit || len(condition.Expr.Body) != 0 || (valueStart+1 < end && text[valueStart:valueStart+2] == "$(") { s.error(valueStart, end, "command conditions cannot use async, capture, acceptance or recovery") }
				}
				mem.Free(s.Alloc, view)
				s.takeDiagnostics(condition.Diagnostics)
				branch.Items = slices.Append(s.Alloc, branch.Items, condition.Expr)
				for condition.End < end && space(text[condition.End]) { condition.End++ }
				if condition.End < end && text[condition.End] != '#' { s.error(condition.End, end, "condition header must occupy its own line") }
				if condition.Expr != nil && condition.Expr.Kind == expr.ValueRecovery { s.error(valueStart, end, "condition requires one Kame expression") }
			}
		}
		p.Pos = nextLine(text, end)
		p.branchBody(branch, level+1)
		branch.Span.End, e.Span.End = p.Pos, p.Pos
		if p.Pos == len(text) || len(s.Diagnostics) != 0 || p.indentation(p.Pos) != level { break }
		nextEnd := p.Pos
		for nextEnd < len(text) && !space(text[nextEnd]) && text[nextEnd] != '\n' && text[nextEnd] != ';' { nextEnd++ }
		next := text[p.Pos:nextEnd]
		if next != "elif" && next != "else" { break }
	}
	return e
}

func (p *kashParser) branchBody(branch *expr.Expr, level int) {
	s := p.Script
	body := p.sequence(level)
	hasStatement := false
	for i := range body {
		item := body[i]
		var statement *expr.Expr
		if item.Kind == Definition {
			d := item.Definition
			statement = mem.Alloc[expr.Expr](s.Alloc)
			statement.Kind, statement.Text, statement.Bool, statement.Span = expr.KashDefinition, d.Name, d.Function, d.Span
			statement.Items = slices.Append(s.Alloc, statement.Items, d.Expression)
			d.Expression = nil
			for j := range d.Parameters { parameter := d.Parameters[j]; statement.Parameters = slices.Append(s.Alloc, statement.Parameters, expr.Parameter{Name: parameter.Name, Span: parameter.Span, Rest: parameter.Rest}) }
			definition.Free(s.Alloc, d)
		} else if item.Kind == Comment {
			statement = mem.Alloc[expr.Expr](s.Alloc)
			statement.Kind, statement.Text, statement.Span = expr.KashComment, item.Text, item.Span
		} else { statement = item.Expression }
		if statement.Kind != expr.KashComment { hasStatement = true }
		branch.Body = slices.Append(s.Alloc, branch.Body, statement)
	}
	slices.Free(s.Alloc, body)
	if !hasStatement { s.error(branch.Span.Start, p.Pos, "empty Kash control body") }
	branch.Span.End = p.Pos
}

func (p *kashParser) matchBlock(level int, start int, wordEnd int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = expr.KashMatch, start
	end := wordEnd
	for end < len(text) && text[end] != '\n' { end++ }
	valueStart := wordEnd
	for valueStart < end && space(text[valueStart]) { valueStart++ }
	if valueStart+1 >= end || (text[valueStart:valueStart+2] != "@(" && text[valueStart:valueStart+2] != "$(") { s.error(valueStart, end, "match subject requires @(...) or $(...)") } else {
		subject := p.valueHeader(valueStart, end, true)
		e.Body = slices.Append(s.Alloc, e.Body, subject)
	}
	p.Pos = nextLine(text, end)
	seenElse := false
	for p.Pos < len(text) && len(s.Diagnostics) == 0 {
		for p.Pos < len(text) && (space(text[p.Pos]) || text[p.Pos] == '\n') { p.Pos++ }
		if p.Pos == len(text) { break }
		armStart := p.Pos
		if text[p.Pos] == '#' {
			for p.Pos < len(text) && text[p.Pos] != '\n' { p.Pos++ }
			comment := mem.Alloc[expr.Expr](s.Alloc)
			comment.Kind, comment.Text, comment.Span = expr.KashComment, text[armStart:p.Pos], source.Span{Start: armStart, End: p.Pos}
			e.Body = slices.Append(s.Alloc, e.Body, comment)
			continue
		}
		indent := p.indentation(p.Pos)
		if indent <= level { break }
		if indent != level+1 { s.error(p.Pos, p.Pos+1, "match arms must be one indentation level deeper"); break }
		armEnd := p.Pos
		for armEnd < len(text) && !space(text[armEnd]) && text[armEnd] != '\n' { armEnd++ }
		keyword := text[p.Pos:armEnd]
		if keyword != "case" && keyword != "else" { s.error(p.Pos, armEnd, "match requires case or else arms"); break }
		if seenElse { s.error(p.Pos, armEnd, "branch after else"); break }
		branch := mem.Alloc[expr.Expr](s.Alloc)
		branch.Kind, branch.Text, branch.Span.Start = expr.KashBranch, keyword, p.Pos
		e.Items = slices.Append(s.Alloc, e.Items, branch)
		end = armEnd
		for end < len(text) && text[end] != '\n' { end++ }
		valueStart = armEnd
		for valueStart < end && space(text[valueStart]) { valueStart++ }
		if keyword == "else" {
			seenElse = true
			if valueStart < end && text[valueStart] != '#' { s.error(valueStart, end, "else header takes no pattern") }
		} else if valueStart == end || text[valueStart] == '#' { s.error(armEnd, end, "case requires one pattern expression") } else { branch.Items = slices.Append(s.Alloc, branch.Items, p.valueHeader(valueStart, end, false)) }
		p.Pos = nextLine(text, end)
		p.branchBody(branch, level+2)
	}
	if len(e.Items) == 0 { s.error(start, p.Pos, "empty Kash match block") }
	e.Span.End = p.Pos
	return e
}

func (p *kashParser) valueHeader(start int, end int, kash bool) *expr.Expr {
	s := p.Script
	view := mem.Alloc[source.Source](s.Alloc)
	*view = *s.Source
	view.Text = s.Source.Text[:end]
	var value expr.Prefix
	if kash { value = expr.ParseKashValuePrefix(s.Alloc, view, start) } else { value = expr.ParseKashExpressionPrefix(s.Alloc, view, start) }
	mem.Free(s.Alloc, view)
	s.takeDiagnostics(value.Diagnostics)
	for value.End < end && space(s.Source.Text[value.End]) { value.End++ }
	if (value.End < end && s.Source.Text[value.End] != '#') || topLevel(s.Source.Text[start:end], ';') >= 0 || (value.Expr != nil && value.Expr.Kind == expr.ValueRecovery) { s.error(start, end, "control header requires exactly one expression") }
	return value.Expr
}
