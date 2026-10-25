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
	validateValueScript(s)
}

type kashParser struct {
	Script   *Script
	Pos      int
	Style    byte
	Unit     int
	Channels []string
}

func (p *kashParser) sequence(level int) []ScriptItem {
	s, text := p.Script, p.Script.Source.Text
	previousChannels := p.Channels
	p.Channels = slices.Clone(s.Alloc, previousChannels)
	var items []ScriptItem
	for p.Pos < len(text) && len(s.Diagnostics) == 0 {
		for p.Pos < len(text) && (space(text[p.Pos]) || text[p.Pos] == '\n' || text[p.Pos] == ';') {
			p.Pos++
		}
		if p.Pos == len(text) {
			break
		}
		start := p.Pos
		if text[start] == '#' {
			for p.Pos < len(text) && text[p.Pos] != '\n' {
				p.Pos++
			}
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Comment, Text: text[start:p.Pos], Span: source.Span{Start: start, End: p.Pos}})
			continue
		}
		indent := level
		lineStart := start
		for lineStart > 0 && text[lineStart-1] != '\n' {
			lineStart--
		}
		leading := true
		for i := lineStart; i < start; i++ {
			if !space(text[i]) {
				leading = false
				break
			}
		}
		if leading {
			indent = p.indentation(start)
		}
		if indent < level {
			break
		}
		if indent != level {
			s.error(start, start+1, "unexpected Kash indentation")
			break
		}
		wordEnd := start
		for wordEnd < len(text) && !space(text[wordEnd]) && text[wordEnd] != '\n' && text[wordEnd] != ';' {
			wordEnd++
		}
		word := text[start:wordEnd]
		if word == "stream" {
			p.Pos = wordEnd
			for p.Pos < len(text) && space(text[p.Pos]) {
				p.Pos++
			}
			nameStart := p.Pos
			for p.Pos < len(text) && !space(text[p.Pos]) && text[p.Pos] != '\n' && text[p.Pos] != ';' && text[p.Pos] != '#' {
				p.Pos++
			}
			name := text[nameStart:p.Pos]
			if !expr.ChannelName(name) || slices.Contains(p.Channels, name) {
				s.error(nameStart, p.Pos, "invalid, duplicate, or reserved stream declaration")
				break
			}
			declaration := mem.Alloc[expr.Expr](s.Alloc)
			declaration.Kind, declaration.Text, declaration.Span = expr.CommandStream, name, source.Span{Start: start, End: p.Pos}
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Expression, Span: declaration.Span, Expression: declaration})
			p.Channels = slices.Append(s.Alloc, p.Channels, name)
			for p.Pos < len(text) && space(text[p.Pos]) {
				p.Pos++
			}
			if p.Pos < len(text) && text[p.Pos] != '\n' && text[p.Pos] != ';' && text[p.Pos] != '#' {
				s.error(p.Pos, p.Pos+1, "stream declaration takes one channel name")
			}
			continue
		}
		if word == "elif" || word == "else" {
			if level != 0 {
				break
			}
			s.error(start, wordEnd, "orphan Kash branch keyword")
			break
		}
		if word == "case" {
			s.error(start, wordEnd, "orphan Kash case keyword")
			break
		}
		if word == "if" || word == "match" || word == "for" || word == "while" {
			lineStart := start
			for lineStart > 0 && text[lineStart-1] != '\n' {
				lineStart--
			}
			for i := lineStart; i < start; i++ {
				if !space(text[i]) {
					s.error(start, wordEnd, "control headers must occupy their own line")
				}
			}
			var e *expr.Expr
			if word == "match" {
				e = p.matchBlock(level, start, wordEnd)
			} else if word == "for" {
				e = p.forBlock(level, start, wordEnd)
			} else {
				e = p.conditionalBlock(level, start)
			}
			expr.SetCommandChannels(s.Alloc, e, p.Channels)
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Command, Span: e.Span, Expression: e})
			continue
		}
		if headerEnd := definition.KashHeaderEnd(s.Alloc, s.Source, start); headerEnd != 0 {
			valueStart := headerEnd
			for valueStart < len(text) && space(text[valueStart]) {
				valueStart++
			}
			if valueStart == len(text) || text[valueStart] == '\n' || text[valueStart] == ';' || text[valueStart] == '#' {
				s.error(headerEnd, valueStart, "expected Kash definition value")
				break
			}
			part := definition.ParseKashPrefix(s.Alloc, s.Source, start)
			s.takeDiagnostics(part.Diagnostics)
			if part.Definition == nil {
				break
			}
			if part.Definition.Name == "env" {
				s.error(start, part.Definition.Span.End, "env is reserved in Kash")
			}
			for i := range part.Definition.Parameters {
				if part.Definition.Parameters[i].Name == "env" {
					parameter := part.Definition.Parameters[i]
					s.error(parameter.Span.Start, parameter.Span.End, "env is reserved in Kash")
				}
			}
			p.Pos = part.Definition.Span.End
			expr.SetCommandChannels(s.Alloc, part.Definition.Expression, p.Channels)
			for i := range items {
				if items[i].Kind == Definition && items[i].Definition.Name == part.Definition.Name {
					s.error(start, p.Pos, "duplicate Kash definition name")
				}
			}
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Definition, Span: part.Definition.Span, Definition: part.Definition})
			end := p.Pos
			for end < len(text) && space(text[end]) {
				end++
			}
			if end < len(text) && text[end] != '\n' && text[end] != ';' && text[end] != '#' {
				s.error(end, end+1, "unexpected Kash definition value; expected statement separator")
			}
		} else if word == "defer" {
			p.Pos = wordEnd
			for p.Pos < len(text) && space(text[p.Pos]) {
				p.Pos++
			}
			if p.Pos == len(text) || text[p.Pos] == '\n' || text[p.Pos] == ';' || text[p.Pos] == '#' {
				s.error(start, p.Pos, "defer requires a command or parenthesized expression")
				break
			}
			var part expr.Prefix
			if text[p.Pos] == '(' && !expr.IsCommandGroup(text, p.Pos) {
				part = expr.ParseKashValuePrefix(s.Alloc, s.Source, p.Pos)
			} else {
				part = expr.ParseCommandPrefix(s.Alloc, s.Source, p.Pos)
			}
			s.takeDiagnostics(part.Diagnostics)
			if part.Expr != nil && part.Expr.Kind == expr.ValueRecovery {
				s.error(start, part.End, "defer requires one parenthesized Kame expression, not a Kash value recovery")
			}
			if part.Expr != nil && part.Expr.Kind == expr.CommandList {
				s.error(start, part.End, "defer requires one process graph; defer a Kash callable for a command list")
			}
			if part.Expr != nil && part.Expr.Kind == expr.CommandGraph && (part.Expr.Async || len(part.Expr.Body) != 0) {
				s.error(start, part.End, "deferred commands cannot use & or command fallback; defer a Kash callable for compound cleanup")
			}
			e := mem.Alloc[expr.Expr](s.Alloc)
			e.Kind, e.Span = expr.KashDefer, source.Span{Start: start, End: part.End}
			e.Items = slices.Append(s.Alloc, e.Items, part.Expr)
			expr.SetCommandChannels(s.Alloc, e, p.Channels)
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Command, Span: e.Span, Expression: e})
			p.Pos = part.End
			end := p.Pos
			for end < len(text) && space(text[end]) {
				end++
			}
			if end < len(text) && text[end] != '\n' && text[end] != ';' && text[end] != '#' {
				s.error(end, end+1, "defer requires one command or expression")
			}
		} else if text[start] == '(' && !expr.IsCommandGroup(text, start) {
			part := expr.ParseKashValuePrefix(s.Alloc, s.Source, start)
			s.takeDiagnostics(part.Diagnostics)
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Expression, Span: source.Span{Start: start, End: part.End}, Expression: part.Expr})
			p.Pos = part.End
			expr.SetCommandChannels(s.Alloc, part.Expr, p.Channels)
			end := p.Pos
			for end < len(text) && space(text[end]) {
				end++
			}
			if end < len(text) && text[end] != '\n' && text[end] != ';' && text[end] != '#' {
				s.error(end, end+1, "expression statement requires a statement separator")
			}
		} else {
			part := expr.ParseCommandPrefix(s.Alloc, s.Source, start)
			s.takeDiagnostics(part.Diagnostics)
			items = slices.Append(s.Alloc, items, ScriptItem{Kind: Command, Span: part.Expr.Span, Expression: part.Expr})
			p.Pos = part.End
			expr.SetCommandChannels(s.Alloc, part.Expr, p.Channels)
		}
		if p.Pos <= start {
			break
		}
	}
	slices.Free(s.Alloc, p.Channels)
	p.Channels = previousChannels
	return items
}

func (p *kashParser) indentation(pos int) int {
	text := p.Script.Source.Text
	start := pos
	for start > 0 && text[start-1] != '\n' {
		start--
	}
	end := start
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	if end == start {
		return 0
	}
	style := text[start]
	for i := start; i < end; i++ {
		if text[i] != style {
			p.Script.error(start, end, "mixed Kash indentation")
			return -1
		}
	}
	if p.Style == 0 {
		p.Style = style
		p.Unit = end - start
		if style == '\t' {
			p.Unit = 1
		}
	}
	if p.Style != style || (end-start)%p.Unit != 0 {
		p.Script.error(start, end, "inconsistent Kash indentation")
		return -1
	}
	return (end - start) / p.Unit
}

func (p *kashParser) conditionalBlock(level int, start int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = expr.KashIf, start
	seenElse := false
	for len(s.Diagnostics) == 0 {
		branchStart := p.Pos
		wordEnd := branchStart
		for wordEnd < len(text) && !space(text[wordEnd]) && text[wordEnd] != '\n' {
			wordEnd++
		}
		keyword := text[branchStart:wordEnd]
		if keyword == "while" {
			e.Kind = expr.KashWhile
		}
		if seenElse {
			s.error(branchStart, wordEnd, "branch after else")
			break
		}
		branch := mem.Alloc[expr.Expr](s.Alloc)
		branch.Kind, branch.Text, branch.Span.Start = expr.KashBranch, keyword, branchStart
		e.Items = slices.Append(s.Alloc, e.Items, branch)
		end := wordEnd
		for end < len(text) && text[end] != '\n' {
			end++
		}
		valueStart := wordEnd
		for valueStart < end && space(text[valueStart]) {
			valueStart++
		}
		if keyword == "else" {
			seenElse = true
			if valueStart < end && text[valueStart] != '#' {
				s.error(valueStart, end, "else header takes no condition")
			}
		} else {
			if valueStart == end || text[valueStart] == '#' {
				s.error(wordEnd, end, "expected Kash condition")
			} else {
				var condition expr.Prefix
				if text[valueStart] == '(' {
					condition = expr.ParseKashValuePrefix(s.Alloc, s.Source, valueStart)
					if condition.End > end {
						end = condition.End
						for end < len(text) && text[end] != '\n' {
							end++
						}
					}
				} else {
					condition = expr.ParseCommandPrefix(s.Alloc, s.Source, valueStart)
					if condition.Expr.Kind == expr.CommandList {
						condition.Expr.CommandCondition = true
						if condition.End > end {
							end = condition.End
							for end < len(text) && text[end] != '\n' {
								end++
							}
						}
					} else {
						condition.Expr.Kind = expr.CommandTest
					}
					if hasCommandConditionControl(condition.Expr) || (valueStart+1 < end && text[valueStart:valueStart+2] == "$(") {
						s.error(valueStart, end, "command conditions cannot use async, capture, acceptance or recovery")
					}
				}
				s.takeDiagnostics(condition.Diagnostics)
				branch.Items = slices.Append(s.Alloc, branch.Items, condition.Expr)
				for condition.End < end && space(text[condition.End]) {
					condition.End++
				}
				if condition.End < end && text[condition.End] != '#' {
					s.error(condition.End, end, "condition header must occupy its own line")
				}
				if condition.Expr != nil && condition.Expr.Kind == expr.ValueRecovery {
					s.error(valueStart, end, "condition requires one Kame expression")
				}
			}
		}
		p.Pos = nextLine(text, end)
		p.branchBody(branch, level+1)
		branch.Span.End, e.Span.End = p.Pos, p.Pos
		if e.Kind == expr.KashWhile {
			break
		}
		if p.Pos == len(text) || len(s.Diagnostics) != 0 || p.indentation(p.Pos) != level {
			break
		}
		nextEnd := p.Pos
		for nextEnd < len(text) && !space(text[nextEnd]) && text[nextEnd] != '\n' && text[nextEnd] != ';' {
			nextEnd++
		}
		next := text[p.Pos:nextEnd]
		if next != "elif" && next != "else" {
			break
		}
	}
	return e
}

func (p *kashParser) forBlock(level int, start int, wordEnd int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = expr.KashFor, start
	branch := mem.Alloc[expr.Expr](s.Alloc)
	branch.Kind, branch.Span.Start = expr.KashBranch, start
	e.Items = slices.Append(s.Alloc, e.Items, branch)
	pos := wordEnd
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	nameStart := pos
	for pos < len(text) && !space(text[pos]) && text[pos] != '\n' {
		pos++
	}
	name := text[nameStart:pos]
	if !definition.ValidName(name) || name == "env" {
		s.error(nameStart, pos, "expected non-reserved loop binding name")
		return e
	}
	e.Text = name
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	if pos+2 >= len(text) || text[pos:pos+2] != "in" || !space(text[pos+2]) {
		s.error(pos, pos, "expected in followed by one list expression")
		return e
	}
	pos += 2
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	if pos == len(text) || text[pos] == '\n' || text[pos] == '#' {
		s.error(pos, pos, "expected loop iterable expression")
		return e
	}
	part := expr.ParseKashValuePrefix(s.Alloc, s.Source, pos)
	s.takeDiagnostics(part.Diagnostics)
	branch.Items = slices.Append(s.Alloc, branch.Items, part.Expr)
	if part.Expr != nil && part.Expr.Kind == expr.ValueRecovery {
		s.error(pos, part.End, "loop iterable requires one Kame expression")
	}
	end := part.End
	for end < len(text) && space(text[end]) {
		end++
	}
	if end < len(text) && text[end] != '\n' && text[end] != '#' {
		s.error(end, end+1, "loop header must occupy its own line")
	}
	for end < len(text) && text[end] != '\n' {
		end++
	}
	p.Pos = nextLine(text, end)
	p.branchBody(branch, level+1)
	for i := range branch.Body {
		if branch.Body[i].Kind == expr.KashDefinition && branch.Body[i].Text == name {
			s.error(branch.Body[i].Span.Start, branch.Body[i].Span.End, "body definition conflicts with loop binding")
		}
	}
	branch.Text = owned(s.Alloc, "for "+name+" in")
	branch.TextOwned = true
	e.Span.End = p.Pos
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
			for j := range d.Parameters {
				parameter := d.Parameters[j]
				statement.Parameters = slices.Append(s.Alloc, statement.Parameters, expr.Parameter{Name: parameter.Name, Span: parameter.Span, Rest: parameter.Rest})
			}
			definition.Free(s.Alloc, d)
		} else if item.Kind == Comment {
			statement = mem.Alloc[expr.Expr](s.Alloc)
			statement.Kind, statement.Text, statement.Span = expr.KashComment, item.Text, item.Span
		} else {
			statement = item.Expression
		}
		if statement == nil {
			continue
		}
		if statement.Kind != expr.KashComment {
			hasStatement = true
		}
		branch.Body = slices.Append(s.Alloc, branch.Body, statement)
	}
	slices.Free(s.Alloc, body)
	if !hasStatement {
		s.error(branch.Span.Start, p.Pos, "empty Kash control body")
	}
	branch.Span.End = p.Pos
}

func (p *kashParser) matchBlock(level int, start int, wordEnd int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = expr.KashMatch, start
	end := wordEnd
	for end < len(text) && text[end] != '\n' {
		end++
	}
	valueStart := wordEnd
	for valueStart < end && space(text[valueStart]) {
		valueStart++
	}
	if valueStart >= end {
		s.error(valueStart, end, "match subject requires a Kame expression, reference or capture")
	} else {
		if text[valueStart] == '(' {
			value := expr.ParseKashValuePrefix(s.Alloc, s.Source, valueStart)
			s.takeDiagnostics(value.Diagnostics)
			e.Body = slices.Append(s.Alloc, e.Body, value.Expr)
			if value.End > end {
				end = value.End
				for end < len(text) && text[end] != '\n' {
					end++
				}
			}
			following := value.End
			for following < end && space(text[following]) {
				following++
			}
			if following < end && text[following] != '#' {
				s.error(following, end, "control header requires exactly one expression")
			}
		} else {
			subject := p.valueHeader(valueStart, end, true)
			e.Body = slices.Append(s.Alloc, e.Body, subject)
		}
	}
	p.Pos = nextLine(text, end)
	seenElse := false
	for p.Pos < len(text) && len(s.Diagnostics) == 0 {
		for p.Pos < len(text) && (space(text[p.Pos]) || text[p.Pos] == '\n') {
			p.Pos++
		}
		if p.Pos == len(text) {
			break
		}
		armStart := p.Pos
		if text[p.Pos] == '#' {
			for p.Pos < len(text) && text[p.Pos] != '\n' {
				p.Pos++
			}
			comment := mem.Alloc[expr.Expr](s.Alloc)
			comment.Kind, comment.Text, comment.Span = expr.KashComment, text[armStart:p.Pos], source.Span{Start: armStart, End: p.Pos}
			e.Body = slices.Append(s.Alloc, e.Body, comment)
			continue
		}
		indent := p.indentation(p.Pos)
		if indent <= level {
			break
		}
		if indent != level+1 {
			s.error(p.Pos, p.Pos+1, "match arms must be one indentation level deeper")
			break
		}
		armEnd := p.Pos
		for armEnd < len(text) && !space(text[armEnd]) && text[armEnd] != '\n' {
			armEnd++
		}
		keyword := text[p.Pos:armEnd]
		if keyword != "case" && keyword != "else" {
			s.error(p.Pos, armEnd, "match requires case or else arms")
			break
		}
		if seenElse {
			s.error(p.Pos, armEnd, "branch after else")
			break
		}
		branch := mem.Alloc[expr.Expr](s.Alloc)
		branch.Kind, branch.Text, branch.Span.Start = expr.KashBranch, keyword, p.Pos
		e.Items = slices.Append(s.Alloc, e.Items, branch)
		end = armEnd
		for end < len(text) && text[end] != '\n' {
			end++
		}
		valueStart = armEnd
		for valueStart < end && space(text[valueStart]) {
			valueStart++
		}
		if keyword == "else" {
			seenElse = true
			if valueStart < end && text[valueStart] != '#' {
				s.error(valueStart, end, "else header takes no pattern")
			}
		} else if valueStart == end || text[valueStart] == '#' {
			s.error(armEnd, end, "case requires one pattern expression")
		} else {
			branch.Items = slices.Append(s.Alloc, branch.Items, p.valueHeader(valueStart, end, false))
		}
		p.Pos = nextLine(text, end)
		p.branchBody(branch, level+2)
	}
	if len(e.Items) == 0 {
		s.error(start, p.Pos, "empty Kash match block")
	}
	e.Span.End = p.Pos
	return e
}

func (p *kashParser) valueHeader(start int, end int, kash bool) *expr.Expr {
	s := p.Script
	view := mem.Alloc[source.Source](s.Alloc)
	*view = *s.Source
	view.Text = s.Source.Text[:end]
	var value expr.Prefix
	if kash {
		value = expr.ParseKashValuePrefix(s.Alloc, view, start)
	} else {
		value = expr.ParseKashExpressionPrefix(s.Alloc, view, start)
	}
	mem.Free(s.Alloc, view)
	s.takeDiagnostics(value.Diagnostics)
	for value.End < end && space(s.Source.Text[value.End]) {
		value.End++
	}
	if (value.End < end && s.Source.Text[value.End] != '#') || source.TopLevelByte(s.Source.Text[start:end], ';') >= 0 || (value.Expr != nil && value.Expr.Kind == expr.ValueRecovery) {
		s.error(start, end, "control header requires exactly one expression")
	}
	return value.Expr
}

func hasCommandConditionControl(e *expr.Expr) bool {
	if e == nil {
		return false
	}
	if e.Async || e.AcceptExit || e.Kind == expr.CommandCapture || ((e.Kind == expr.CommandGraph || e.Kind == expr.CommandTest) && len(e.Body) != 0) {
		return true
	}
	// Argument expressions have their own control boundaries. Only list
	// operands belong to the command condition's control syntax.
	if e.Kind != expr.CommandList {
		return false
	}
	for i := range e.Items {
		if hasCommandConditionControl(e.Items[i]) {
			return true
		}
	}
	return false
}
