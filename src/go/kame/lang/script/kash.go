package script

import (
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/source"
	"slices"
	"strings"
)

// ParseKash composes strict Kame definitions and structured Kash command graphs.
func ParseKash(a mem.Allocator, name string, text string) *Script {
	s := parseKashText(a, name, text, false, false)
	s.ValidateAssignments()
	return s
}

// ParseKashRuleBody parses a recipe after structural indentation is removed.
func ParseKashRuleBody(a mem.Allocator, name string, text string) *Script {
	s := parseKashText(a, name, text, true, false)
	s.ValidateAssignments()
	return s
}

func parseKashText(a mem.Allocator, name string, text string, ruleBody, interactive bool) *Script {
	s := mem.Alloc[Script](a)
	s.Alloc, s.Source, s.RuleBody = a, source.New(a, name, text), ruleBody
	parseKash(s, 0, interactive)
	return s
}

func parseKash(s *Script, offset int, interactive bool) {
	s.Kash = true
	p := kashParser{Script: s, Pos: offset, RuleBody: s.RuleBody, Interactive: interactive}
	s.Items = p.sequence(0)
}

type kashParser struct {
	Script      *Script
	Pos         int
	Prefixes    []string
	Channels    []string
	Functions   int
	ValueBlock  bool
	RuleBody    bool
	Interactive bool
	Loops       int
}

func (p *kashParser) sequence(level int) []ScriptItem {
	s, text := p.Script, p.Script.Source.Text
	prefixDepth := len(p.Prefixes)
	defer func() { p.Prefixes = p.Prefixes[:prefixDepth] }()
	previousChannels := p.Channels
	p.Channels = slices.Clone(previousChannels)
	var items []ScriptItem
	for p.Pos < len(text) && len(s.Diagnostics) == 0 {
		for p.Pos < len(text) && (space(text[p.Pos]) || text[p.Pos] == '\n' || text[p.Pos] == ';') {
			p.Pos++
		}
		if p.Pos == len(text) {
			break
		}
		start := p.Pos
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
		if text[start] == '#' {
			for p.Pos < len(text) && text[p.Pos] != '\n' {
				p.Pos++
			}
			items = append(items, ScriptItem{Kind: Comment, Text: text[start:p.Pos], Span: source.Span{Start: start, End: p.Pos}, commentDepth: p.commentDepth(text[lineStart:start], level)})
			continue
		}
		indent := level
		if leading {
			indent = p.indentation(lineStart, start, level)
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
		headerEnd := definition.KashHeaderEnd(s.Alloc, s.Source, start)
		if moduleKeyword(word) {
			if p.Script.RuleBody || level != 0 || !leading {
				s.error(start, wordEnd, "module declarations require a top-level line")
				break
			}
			end := source.LogicalLineEnd(text, start)
			items = append(items, s.moduleDeclaration(word, start, wordEnd, end))
			p.Pos = nextLine(text, end)
			continue
		}
		if word == "include" || word == "include?" {
			if p.Script.RuleBody || level != 0 || !leading {
				s.error(start, wordEnd, "include declarations require a whole top-level line")
				break
			}
			end := physicalLineEnd(text, start)
			items = append(items, s.includeDeclaration(start, trimStart(text, wordEnd, end), end, word == "include?"))
			p.Pos = nextLine(text, end)
			continue
		}
		if p.Interactive && (word == "alias" || word == "unalias" || word == "option") && headerEnd == 0 {
			if p.Script.RuleBody || level != 0 || !leading {
				s.error(start, wordEnd, "session commands require a whole top-level session entry")
				break
			}
			items = append(items, p.sessionCommand(start, wordEnd))
			continue
		}
		if word == "let" {
			d := p.eagerBinding(start, wordEnd)
			if d == nil {
				break
			}
			for i := range items {
				if items[i].Kind == Definition && items[i].Definition.Name == d.Name && !items[i].Definition.Eager {
					s.error(start, p.Pos, "duplicate Kash definition name")
				}
			}
			items = append(items, ScriptItem{Kind: Definition, Span: d.Span, Definition: d})
			continue
		}
		if word == "set" || word == "yield" {
			e := p.valueStatement(word, start, wordEnd)
			expr.SetCommandChannels(s.Alloc, e, p.Channels)
			items = append(items, ScriptItem{Kind: Expression, Span: e.Span, Expression: e})
			continue
		}
		if word == "def" && headerEnd == 0 {
			if !leading {
				s.error(start, wordEnd, "function header must occupy its own line")
				break
			}
			d := p.functionBlock(level, start)
			for i := range items {
				if items[i].Kind == Definition && items[i].Definition.Name == d.Name {
					s.error(start, p.Pos, "duplicate Kash definition name")
				}
			}
			items = append(items, ScriptItem{Kind: Definition, Span: d.Span, Definition: d})
			continue
		}
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
			items = append(items, ScriptItem{Kind: Expression, Span: declaration.Span, Expression: declaration})
			p.Channels = append(p.Channels, name)
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
		if word == "if" || word == "match" || word == "for" || word == "while" || word == "with" {
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
			if word == "with" {
				e = p.withBlock(level, start, wordEnd)
			} else if word == "match" {
				e = p.matchBlock(level, start, wordEnd)
			} else if word == "for" {
				e = p.forBlock(level, start, wordEnd)
			} else {
				e = p.conditionalBlock(level, start)
			}
			expr.SetCommandChannels(s.Alloc, e, p.Channels)
			items = append(items, ScriptItem{Kind: Command, Span: e.Span, Expression: e})
			continue
		}
		if headerEnd != 0 {
			if word != "def" {
				s.error(start, headerEnd, "bare binding requires def, let, or set")
				break
			}
			if level == 0 && leading {
				if d := p.valueDefinition(start, headerEnd); d != nil {
					for _, item := range items {
						if item.Kind == Definition && item.Definition.Name == d.Name {
							s.error(start, p.Pos, "duplicate Kash definition name")
						}
					}
					items = append(items, ScriptItem{Kind: Definition, Span: d.Span, Definition: d})
					continue
				}
			}
			valueStart := bindingValueStart(text, start, headerEnd)
			if valueStart == len(text) || text[valueStart] == '\n' || text[valueStart] == ';' || text[valueStart] == '#' {
				s.error(headerEnd, valueStart, "expected Kash definition value")
				break
			}
			part := definition.ParseKashPrefix(s.Alloc, s.Source, start)
			s.takeDiagnostics(part.Diagnostics)
			if part.Definition == nil {
				break
			}
			if part.Definition.Default || part.Definition.Contribution {
				s.error(start, part.Definition.Span.End, "Kash def requires a non-reserved name and =")
			}
			p.Pos = part.Definition.Span.End
			expr.SetCommandChannels(s.Alloc, part.Definition.Expression, p.Channels)
			for i := range items {
				if items[i].Kind == Definition && items[i].Definition.Name == part.Definition.Name {
					s.error(start, p.Pos, "duplicate Kash definition name")
				}
			}
			items = append(items, ScriptItem{Kind: Definition, Span: part.Definition.Span, Definition: part.Definition})
			end := p.Pos
			for end < len(text) && space(text[end]) {
				end++
			}
			if end < len(text) && text[end] != '\n' && text[end] != ';' && text[end] != '#' {
				s.error(end, end+1, "unexpected Kash definition value; expected statement separator")
			}
		} else if word == "break" || word == "continue" {
			kind := expr.KashBreak
			if word == "continue" {
				kind = expr.KashContinue
			}
			e := p.loopTransferStatement(start, wordEnd, kind)
			items = append(items, ScriptItem{Kind: Command, Span: e.Span, Expression: e})
		} else if word == "return" {
			e := p.valueReturn(start, wordEnd)
			expr.SetCommandChannels(s.Alloc, e, p.Channels)
			items = append(items, ScriptItem{Kind: Command, Span: e.Span, Expression: e})
		} else if word == "exit" {
			e := p.exitStatement(start, wordEnd)
			items = append(items, ScriptItem{Kind: Command, Span: e.Span, Expression: e})
		} else if word == "defer" {
			p.Pos = wordEnd
			for p.Pos < len(text) && space(text[p.Pos]) {
				p.Pos++
			}
			if p.Pos == len(text) || text[p.Pos] == '\n' || text[p.Pos] == ';' || text[p.Pos] == '#' {
				s.error(start, p.Pos, "defer requires a command or parenthesized expression")
				break
			}
			if standaloneKeyword(text, p.Pos, "yield") {
				s.error(p.Pos, p.Pos+5, "cannot defer yield")
				break
			}
			if standaloneKeyword(text, p.Pos, "return") {
				s.error(p.Pos, p.Pos+6, "cannot defer return")
				break
			}
			if standaloneKeyword(text, p.Pos, "exit") {
				s.error(p.Pos, p.Pos+4, "cannot defer exit")
				break
			}
			if standaloneKeyword(text, p.Pos, "break") {
				s.error(p.Pos, p.Pos+5, "cannot defer a loop transfer")
				break
			}
			if standaloneKeyword(text, p.Pos, "continue") {
				s.error(p.Pos, p.Pos+8, "cannot defer a loop transfer")
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
			e.Items = append(e.Items, part.Expr)
			expr.SetCommandChannels(s.Alloc, e, p.Channels)
			items = append(items, ScriptItem{Kind: Command, Span: e.Span, Expression: e})
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
			items = append(items, ScriptItem{Kind: Expression, Span: source.Span{Start: start, End: part.End}, Expression: part.Expr})
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
			items = append(items, ScriptItem{Kind: Command, Span: part.Expr.Span, Expression: part.Expr})
			p.Pos = part.End
			expr.SetCommandChannels(s.Alloc, part.Expr, p.Channels)
		}
		if p.Pos <= start {
			break
		}
	}
	mem.FreeSlice(nil, p.Channels)
	p.Channels = previousChannels
	return items
}

// indentation compares a line with the active block prefixes. A new
// block takes its prefix from its first statement.
func (p *kashParser) indentation(start, end, level int) int {
	prefix := p.Script.Source.Text[start:end]
	if strings.Contains(prefix, " ") && strings.Contains(prefix, "\t") {
		p.Script.error(start, end, "mixed Kash indentation")
		return -1
	}
	prefix = strings.TrimPrefix(prefix, p.Script.Source.StructuralIndent)
	if prefix == "" {
		return 0
	}
	for i, active := range p.Prefixes {
		if prefix == active {
			return i + 1
		}
	}
	if level == len(p.Prefixes)+1 && (len(p.Prefixes) == 0 || strings.HasPrefix(prefix, p.Prefixes[len(p.Prefixes)-1])) {
		p.Prefixes = append(p.Prefixes, prefix)
		return level
	}
	p.Script.error(start, end, "inconsistent Kash indentation")
	return -1
}

func (p *kashParser) commentDepth(prefix string, level int) int {
	prefix = strings.TrimPrefix(prefix, p.Script.Source.StructuralIndent)
	if prefix == "" {
		return 0
	}
	for i, active := range p.Prefixes {
		if prefix == active {
			return i + 1
		}
	}
	return level
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
		e.Items = append(e.Items, branch)
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
				condition := expr.ParseValuePrefix(s.Alloc, s.Source, valueStart)
				end = physicalLineEnd(text, condition.End)
				s.takeDiagnostics(condition.Diagnostics)
				branch.Items = append(branch.Items, condition.Expr)
				for condition.End < end && space(text[condition.End]) {
					condition.End++
				}
				if condition.End < end && text[condition.End] != '#' {
					s.error(condition.End, end, "condition header must occupy its own line")
				}
			}
		}
		p.Pos = nextLine(text, end)
		if e.Kind == expr.KashWhile {
			p.Loops++
		}
		p.branchBody(branch, level+1)
		if e.Kind == expr.KashWhile {
			p.Loops--
		}
		branch.Span.End, e.Span.End = p.Pos, p.Pos
		if e.Kind == expr.KashWhile {
			break
		}
		if p.Pos == len(text) || len(s.Diagnostics) != 0 || p.indentation(strings.LastIndexByte(text[:p.Pos], '\n')+1, p.Pos, level) != level {
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
	e.Items = append(e.Items, branch)
	pos := wordEnd
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	nameStart := pos
	for pos < len(text) && !space(text[pos]) && text[pos] != '\n' {
		pos++
	}
	name := text[nameStart:pos]
	if !expr.ValidBindingName(name) {
		s.error(nameStart, pos, "expected non-reserved loop binding name")
		return e
	}
	e.Text = name
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	records := pos+4 < len(text) && text[pos:pos+4] == "from" && space(text[pos+4])
	if !records && (pos+2 >= len(text) || text[pos:pos+2] != "in" || !space(text[pos+2])) {
		s.error(pos, pos, "expected in LIST or from SOURCE FRAME")
		return e
	}
	if records {
		e.Kind = expr.KashRecords
		pos += 4
	} else {
		pos += 2
	}
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	if pos == len(text) || text[pos] == '\n' || text[pos] == '#' {
		s.error(pos, pos, "expected loop iterable expression")
		return e
	}
	var part expr.Prefix
	if records {
		part = expr.ParseKashRecordSourcePrefix(s.Alloc, s.Source, pos)
	} else {
		part = expr.ParseKashValueExpressionPrefix(s.Alloc, s.Source, pos)
	}
	s.takeDiagnostics(part.Diagnostics)
	branch.Items = append(branch.Items, part.Expr)
	if iterable := expr.Ungroup(part.Expr); records && iterable != nil && iterable.Infix == "??" {
		s.error(pos, part.End, "record source does not allow ??")
	}
	end := part.End
	for end < len(text) && space(text[end]) {
		end++
	}
	if records {
		frameStart := end
		for end < len(text) && !space(text[end]) && text[end] != '\n' && text[end] != '#' {
			end++
		}
		frame := text[frameStart:end]
		if frame == ":lines" {
			e.RecordFrame = ":lines"
		} else if frame == ":nul" {
			e.RecordFrame = ":nul"
		} else {
			s.error(frameStart, end, "record loop frame must be literal :lines or :nul")
		}
		for end < len(text) && space(text[end]) {
			end++
		}
	}
	if end < len(text) && text[end] != '\n' && text[end] != '#' {
		s.error(end, end+1, "loop header must occupy its own line")
	}
	for end < len(text) && text[end] != '\n' {
		end++
	}
	p.Pos = nextLine(text, end)
	p.Loops++
	p.branchBody(branch, level+1)
	p.Loops--
	for i := range branch.Body {
		if (branch.Body[i].Kind == expr.KashDefinition || branch.Body[i].Kind == expr.KashLet) && branch.Body[i].Text == name {
			s.error(branch.Body[i].Span.Start, branch.Body[i].Span.End, "body definition conflicts with loop binding")
		}
	}
	if records {
		branch.Text = owned(s.Alloc, "for "+name+" from")
	} else {
		branch.Text = owned(s.Alloc, "for "+name+" in")
	}
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
			if d.Eager {
				statement = d.Expression
				d.Expression = nil
				definition.Free(s.Alloc, d)
				branch.Body = append(branch.Body, statement)
				hasStatement = true
				continue
			}
			statement = mem.Alloc[expr.Expr](s.Alloc)
			statement.Kind, statement.Text, statement.Bool, statement.Span = expr.KashDefinition, d.Name, d.Function, d.Span
			statement.ExplicitDef = d.ExplicitDef
			statement.Items = append(statement.Items, d.Expression)
			d.Expression = nil
			for j := range d.Parameters {
				parameter := d.Parameters[j]
				statement.Parameters = append(statement.Parameters, expr.Parameter{Name: parameter.Name, Span: parameter.Span, Rest: parameter.Rest})
			}
			definition.Free(s.Alloc, d)
		} else if item.Kind == Comment {
			statement = mem.Alloc[expr.Expr](s.Alloc)
			statement.Kind, statement.Text, statement.Span = expr.KashComment, item.Text, item.Span
			statement.CommentIndent = item.commentDepth
		} else {
			statement = item.Expression
		}
		if statement == nil {
			continue
		}
		if statement.Kind != expr.KashComment {
			hasStatement = true
		}
		branch.Body = append(branch.Body, statement)
	}
	mem.FreeSlice(nil, body)
	if !hasStatement {
		s.error(branch.Span.Start, p.Pos, emptyKashBody)
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
	valueStart := trimStart(text, wordEnd, end)
	if valueStart == end || text[valueStart] == '#' {
		s.error(wordEnd, end, "match requires a subject expression")
	} else {
		subject := expr.ParseValuePrefix(s.Alloc, s.Source, valueStart)
		s.takeDiagnostics(subject.Diagnostics)
		e.Body = append(e.Body, subject.Expr)
		end = physicalLineEnd(text, subject.End)
		following := trimStart(text, subject.End, end)
		if following < end && text[following] != '#' {
			s.error(following, end, "match header requires one expression")
		}
	}
	p.Pos = nextLine(text, end)
	prefixDepth := len(p.Prefixes)
	defer func() { p.Prefixes = p.Prefixes[:prefixDepth] }()
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
			lineStart := strings.LastIndexByte(text[:armStart], '\n') + 1
			comment.CommentIndent = p.commentDepth(text[lineStart:armStart], level+1)
			e.Body = append(e.Body, comment)
			continue
		}
		indent := p.indentation(strings.LastIndexByte(text[:p.Pos], '\n')+1, p.Pos, level+1)
		if indent <= level {
			break
		}
		if indent != level+1 {
			s.error(p.Pos, p.Pos+1, "match arms must be one indentation level deeper")
			break
		}
		end = physicalLineEnd(text, armStart)
		if seenElse {
			s.error(armStart, end, "else must be the last match arm")
			break
		}
		branch := mem.Alloc[expr.Expr](s.Alloc)
		branch.Kind, branch.Span.Start = expr.KashBranch, armStart
		e.Items = append(e.Items, branch)
		arrow := armStart
		if standaloneKeyword(text, armStart, "else") {
			seenElse = true
			branch.Text = "else"
			arrow += len("else")
		} else {
			bounded := *s.Source
			bounded.Text = text[:end]
			pattern := expr.ParseMatchPatternPrefix(s.Alloc, &bounded, armStart)
			s.takeDiagnostics(pattern.Diagnostics)
			branch.Items = append(branch.Items, pattern.Expr)
			arrow = pattern.End
		}
		arrow = trimStart(text, arrow, end)
		if !strings.HasPrefix(text[arrow:end], "=>") {
			s.error(arrow, end, "match arm requires => after the pattern")
			break
		}
		p.Pos = nextLine(text, end)
		resultStart := trimStart(text, arrow+2, end)
		if resultStart == end || text[resultStart] == '#' {
			p.branchBody(branch, level+2)
		} else {
			value := expr.ParseRange(s.Alloc, s.Source, resultStart, end, "")
			s.takeDiagnostics(value.Diagnostics)
			branch.Bool = true // Inline arm result rather than a statement body.
			branch.Body = append(branch.Body, value.Expr)
			branch.Span.End = end
		}
	}
	if len(e.Items) == 0 {
		s.error(start, p.Pos, "empty Kash match block")
	}
	e.Span.End = p.Pos
	return e
}

// standaloneKeyword reports whether text at pos is word followed by a statement
// boundary: whitespace, line end, semicolon, or comment.
func standaloneKeyword(text string, pos int, word string) bool {
	if pos < 0 || pos+len(word) > len(text) || text[pos:pos+len(word)] != word {
		return false
	}
	end := pos + len(word)
	return end == len(text) || space(text[end]) || text[end] == '\n' || text[end] == ';' || text[end] == '#'
}
