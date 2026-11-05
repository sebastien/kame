package expr

import (
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// Kash's process grammar and Kame expressions are mutually recursive. Keep the
// shared parsing primitives here to avoid an expr <-> kash package cycle.
// CommandCapture owns command words, not shell text or reparsed expression text.
func (p *parser) commandCapture() *Expr {
	start := p.pos
	p.pos += 2
	return p.commandGraph(start, true)
}

// ParseCommandPrefix parses one raw Kash graph on the original source. Nested
// substitutions keep the same parser and source spans; no shell text is lowered.
func ParseCommandPrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start}
	e := p.commandGraph(start, false)
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

// ParseCommandSetupValuePrefix shares stage setup's scalar/typed word grammar.
func ParseCommandSetupValuePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start}
	e := p.commandWord(false)
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

// ParseKashValuePrefix adds Kash's reference wrappers, not word-list RHSs.
func ParseKashValuePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	// Kash owns top-level semicolons. Bound an atom's source view so number and
	// path scanners keep Kame's grammar without swallowing the next statement.
	bounded := mem.Alloc[source.Source](a)
	*bounded = *s
	if start < len(s.Text) && s.Text[start] != '(' && s.Text[start] != '[' && s.Text[start] != '"' && s.Text[start] != '$' && s.Text[start] != '@' {
		depth := 0
		for i := start; i < len(s.Text); i++ {
			if s.Text[i] == '\\' {
				i++
				continue
			}
			if s.Text[i] == '{' {
				depth++
			}
			if s.Text[i] == '}' && depth > 0 {
				depth--
			}
			if depth == 0 && s.Text[i] == ';' {
				bounded.Text = s.Text[:i]
				break
			}
			if depth == 0 && (s.Text[i] == ' ' || s.Text[i] == '\t' || s.Text[i] == '\n' || s.Text[i] == '\r') {
				break
			}
		}
	}
	p := parser{a: a, s: bounded, pos: start, kash: true, valuePathStart: start + 1}
	var e *Expr
	if start < len(s.Text) && s.Text[start] == '$' && (start+1 == len(s.Text) || s.Text[start+1] != '(') {
		word := p.commandWord(false)
		if word.Bool {
			e = word.Parts[0].Expr
			word.Parts[0].Expr = nil
		} else {
			p.error(start, p.pos, "definition value must be one Kame expression or standalone substitution")
		}
		freeExpr(a, word)
	} else {
		e = p.expression()
	}
	p.s = s
	mem.Free(a, bounded)
	end := p.pos
	for end < len(s.Text) && (s.Text[end] == ' ' || s.Text[end] == '\t' || s.Text[end] == '\r') {
		end++
	}
	if len(p.diags) == 0 && end > p.pos && end+2 < len(s.Text) && s.Text[end:end+2] == "??" && commandWhitespace(s.Text[end+2]) {
		rightStart := end + 2
		for rightStart < len(s.Text) && (s.Text[rightStart] == ' ' || s.Text[rightStart] == '\t' || s.Text[rightStart] == '\r') {
			rightStart++
		}
		if rightStart == len(s.Text) || s.Text[rightStart] == '\n' || s.Text[rightStart] == ';' || s.Text[rightStart] == '#' {
			p.error(end, rightStart, "expected value after ??")
		} else {
			right := ParseKashValuePrefix(a, s, rightStart)
			for i := range right.Diagnostics {
				p.diags = slices.Append(a, p.diags, right.Diagnostics[i])
			}
			slices.Free(a, right.Diagnostics)
			recovery := mem.Alloc[Expr](a)
			recovery.Kind, recovery.Span = ValueRecovery, source.Span{Start: start, End: right.End}
			recovery.Items = slices.Append(a, recovery.Items, e)
			recovery.Items = slices.Append(a, recovery.Items, right.Expr)
			e, p.pos = recovery, right.End
		}
	}
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

func (p *parser) commandGraph(start int, capture bool) *Expr {
	first := p.commandPipeline(start, capture, false, false)
	if len(p.diags) != 0 {
		return first
	}
	var operands []*Expr
	var operators []CommandOperator
	for p.pos+1 < len(p.s.Text) {
		operator := p.s.Text[p.pos : p.pos+2]
		if operator != "&&" && operator != "||" {
			break
		}
		from := p.pos
		if commandWhitespaceBeforeNewline(p.s.Text, from) {
			p.error(from, from+2, "a command-list operator cannot start a new line")
			break
		}
		p.pos += 2
		afterNewline := false
		for p.pos < len(p.s.Text) && commandWhitespace(p.s.Text[p.pos]) {
			if p.s.Text[p.pos] == '\n' {
				afterNewline = true
			}
			p.pos++
		}
		if p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' || p.s.Text[p.pos] == ';' || p.s.Text[p.pos] == '#' {
			p.error(from, p.pos, "expected command after command-list operator")
			break
		}
		if len(operands) == 0 {
			operands = slices.Append(p.a, operands, first)
		}
		operators = slices.Append(p.a, operators, CommandOperator{Text: operator, Span: source.Span{Start: from, End: from + 2}, AfterNewline: afterNewline})
		next := p.commandPipeline(p.pos, capture, false, false)
		operands = slices.Append(p.a, operands, next)
		if len(p.diags) != 0 {
			break
		}
	}
	result := first
	if len(operands) > 1 {
		for i := range operands {
			if operands[i].Async {
				p.error(operands[i].Span.Start, operands[i].Span.End, "asynchronous command lists are not supported")
				break
			}
		}
		result = mem.Alloc[Expr](p.a)
		result.Kind, result.Span, result.Kash = CommandList, source.Span{Start: start, End: p.pos}, true
		result.CommandListCapture = capture
		result.Items, result.CommandOps = operands, operators
	}
	if capture {
		if p.pos == len(p.s.Text) || p.s.Text[p.pos] != ')' {
			p.error(start, p.pos, "unclosed Kash command substitution")
		} else {
			p.pos++
			result.Span.End = p.pos
		}
	}
	return result
}

func commandWhitespaceBeforeNewline(text string, pos int) bool {
	for pos > 0 && (text[pos-1] == ' ' || text[pos-1] == '\t' || text[pos-1] == '\r') {
		pos--
	}
	return pos > 0 && text[pos-1] == '\n'
}

func (p *parser) commandPipeline(start int, capture bool, grouped bool, branch bool) *Expr {
	previousKash := p.kash
	previousGroup := p.commandGroup
	p.commandGroup = grouped
	p.kash = true
	kind := CommandGraph
	if capture {
		kind = CommandCapture
	}
	e := p.node(kind, start)
	stage := p.node(CommandStage, p.pos)
	if branch || grouped {
		p.commandSpace(true)
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '|' {
			stage.Kind = CommandInput
		}
	}
	e.Items = slices.Append(p.a, e.Items, stage)
	input, output := false, false
	for {
		p.commandSpace(capture || grouped || branch)
		if !capture && !grouped && p.pos < len(p.s.Text) && p.s.Text[p.pos] == '\n' {
			next := p.pos + 1
			for next < len(p.s.Text) && commandWhitespace(p.s.Text[next]) {
				next++
			}
			if next < len(p.s.Text) && p.s.Text[next] == '|' && (next+1 == len(p.s.Text) || p.s.Text[next+1] != '|') {
				p.pos = next
			}
		}
		if p.pos+1 < len(p.s.Text) && p.s.Text[p.pos] == '\\' && p.s.Text[p.pos+1] == '\n' {
			p.pos += 2
			continue
		}
		if p.pos+2 < len(p.s.Text) && p.s.Text[p.pos:p.pos+3] == "\\\r\n" {
			p.pos += 3
			continue
		}
		if p.pos == len(p.s.Text) {
			if capture {
				p.error(start, p.pos, "unclosed Kash command substitution")
			}
			break
		}
		if ((grouped || branch) && p.s.Text[p.pos] == ')') || (grouped && p.s.Text[p.pos] == ',') {
			break
		}
		if p.pos+1 < len(p.s.Text) && ((p.s.Text[p.pos:p.pos+2] == "&&") || (p.s.Text[p.pos:p.pos+2] == "||")) {
			if grouped || branch {
				p.error(p.pos, p.pos+2, "command lists require a complete synchronous graph")
			}
			break
		}
		if stage.Kind == CommandMux && streamOperatorImplicitStage(stage.Text) && p.s.Text[p.pos] != '|' && p.s.Text[p.pos] != '?' && p.s.Text[p.pos] != '&' && p.s.Text[p.pos] != ')' && p.s.Text[p.pos] != '\n' && p.s.Text[p.pos] != ';' && p.s.Text[p.pos] != '#' {
			stage = p.node(CommandStage, p.pos)
			input = false
			e.Items = slices.Append(p.a, e.Items, stage)
			continue
		}
		if stage.Kind == CommandStage && len(stage.Items) == 0 && IsCommandGroup(p.s.Text, p.pos) {
			freeExpr(p.a, stage)
			stage = p.processGroup()
			e.Items[len(e.Items)-1] = stage
			continue
		}
		if !capture && (p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == ';' || p.s.Text[p.pos] == '#') {
			break
		}
		if capture && p.s.Text[p.pos] == ';' {
			p.error(p.pos, p.pos+1, "command substitution contains exactly one process expression")
			break
		}
		if p.s.Text[p.pos] == '?' {
			from := p.pos
			p.pos++
			if !hasExecutable(stage) {
				p.error(from, p.pos, "acceptance requires a complete process graph")
				break
			}
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '?' {
				p.error(from, p.pos+1, "?? is not a raw-command operator")
				break
			}
			stage.Span.End = from
			separated := from > start && commandWhitespace(p.s.Text[from-1]) && p.pos < len(p.s.Text) && commandWhitespace(p.s.Text[p.pos])
			p.commandSpace(capture)
			if p.pos+1 < len(p.s.Text) && (p.s.Text[p.pos:p.pos+2] == "&&" || p.s.Text[p.pos:p.pos+2] == "||") {
				e.AcceptExit = true
				continue
			}
			if !capture && p.pos < len(p.s.Text) && p.s.Text[p.pos] == '&' && (p.pos+1 == len(p.s.Text) || p.s.Text[p.pos+1] != '&') {
				e.AcceptExit = true
				continue
			}
			if p.pos < len(p.s.Text) && !(((capture || grouped) && p.s.Text[p.pos] == ')') || (grouped && p.s.Text[p.pos] == ',') || (!capture && (p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == ';' || p.s.Text[p.pos] == '#'))) {
				if !separated {
					p.error(from, p.pos, "command recovery requires whitespace around ?")
					break
				}
				e.Body = slices.Append(p.a, e.Body, p.commandPipeline(p.pos, capture, false, false))
				break
			}
			e.AcceptExit = true
			if grouped {
				break
			}
			break
		}
		if p.s.Text[p.pos] == '&' {
			if p.pos+1 < len(p.s.Text) && p.s.Text[p.pos+1] == '&' {
				if grouped || branch {
					p.error(p.pos, p.pos+2, "command lists require a complete synchronous graph")
				}
				break
			}
			if len(stage.Items) == 0 && !hasExecutable(stage) && p.pos+1 < len(p.s.Text) && channelNameByte(p.s.Text[p.pos+1], true) {
				freeExpr(p.a, stage)
				stage = p.channelEndpoint(CommandReader, 1)
				e.Items[len(e.Items)-1] = stage
				continue
			}
			from := p.pos
			p.pos++
			if capture || !hasExecutable(stage) {
				p.error(from, p.pos, "async syntax requires a complete command statement")
				break
			}
			p.commandSpace(false)
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] != '\n' && p.s.Text[p.pos] != ';' && p.s.Text[p.pos] != '#' {
				p.error(from, p.pos, "& must terminate the process expression")
				break
			}
			e.Async = true
			break
		}
		if p.s.Text[p.pos] == ')' {
			if !capture {
				p.error(p.pos, p.pos+1, "unexpected Kash closing parenthesis")
				break
			}
			stage.Span.End = p.pos
			break
		}
		if p.s.Text[p.pos] == '|' {
			if !hasExecutable(stage) {
				p.error(p.pos, p.pos+1, "expected Kash executable before pipe")
				break
			}
			if output {
				p.error(p.pos, p.pos+1, "stdout redirection conflicts with pipeline output")
				break
			}
			stage.Span.End = p.pos
			p.pos++
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '|' {
				p.error(p.pos-1, p.pos+1, "|| is not a Kash process operator")
				break
			}
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '(' {
				mux := p.node(CommandMux, p.pos-1)
				p.pos++
				p.commandSpace(true)
				from := p.pos
				for p.pos < len(p.s.Text) && setupKeyByte(p.s.Text[p.pos], p.pos == from) {
					p.pos++
				}
				mux.Text = p.s.Text[from:p.pos]
				p.commandSpace(true)
				operator := streamOperatorFor(mux.Text)
				switch {
				case operator != nil && operator.Argument == argumentLineAdapter:
					argument := p.expression()
					if argument != nil {
						mux.Items = slices.Append(p.a, mux.Items, argument)
					}
					if argument == nil || argument.Kind != Symbol || argument.Text != "lines" {
						p.error(mux.Span.Start, p.pos, "line adapters require :lines")
					}
					p.commandSpace(true)
				case operator != nil && operator.Argument == argumentPredicate:
					predicate := p.expression()
					if predicate != nil {
						mux.Items = slices.Append(p.a, mux.Items, predicate)
					}
					p.commandSpace(true)
					if p.pos == len(p.s.Text) || p.s.Text[p.pos] != '|' {
						p.error(mux.Span.Start, p.pos, "filter requires a predicate and selected branch")
					} else {
						if p.pos+1 == len(p.s.Text) || p.s.Text[p.pos+1] != '(' {
							p.pos++
						}
						p.commandSpace(true)
						mux.Body = slices.Append(p.a, mux.Body, p.commandPipeline(p.pos, false, false, true))
					}
				case operator != nil && operator.Policy:
					p.streamPolicy(mux)
				case operator != nil && operator.Argument == argumentPath:
					if strings.HasPrefix(p.s.Text[p.pos:], ":append") && descriptorBoundary(p.s.Text, p.pos+7) {
						p.pos += 7
						mux.Bool = true
						p.commandSpace(true)
					}
					if p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' {
						p.error(mux.Span.Start, p.pos, "tee requires a path")
					} else {
						mux.Items = slices.Append(p.a, mux.Items, p.commandWord(false))
						p.commandSpace(true)
					}
				}
				if operator == nil || p.pos == len(p.s.Text) || p.s.Text[p.pos] != ')' || len(p.diags) != 0 {
					p.error(mux.Span.Start, p.pos, "invalid Kash stream operator")
					freeExpr(p.a, mux)
					break
				}
				p.pos++
				mux.Span.End = p.pos
				e.Items = slices.Append(p.a, e.Items, mux)
				stage = mux
				input = false
				continue
			}
			stage = p.node(CommandStage, p.pos)
			input = false
			e.Items = slices.Append(p.a, e.Items, stage)
			p.commandSpace(capture || grouped || branch)
			continue
		}
		if end, kind := attachedDescriptor(p.s.Text, p.pos); kind != descriptorNone {
			from := p.pos
			if kind == descriptorUnsupported {
				p.error(from, end, "unsupported descriptor redirection")
				break
			}
			op := "2>"
			if kind == descriptorErrorAppend {
				op = "2>>"
			}
			if kind == descriptorErrorAlias {
				op = "2>&1"
			}
			target := stage
			aliasAfterWriter := false
			if stage.Kind == CommandWriter {
				if len(e.Items) < 2 || e.Items[len(e.Items)-2].Kind != CommandStage {
					p.error(from, end, "stderr redirection requires a command")
					break
				}
				target = e.Items[len(e.Items)-2]
				aliasAfterWriter = true
			} else if stage.Kind != CommandStage {
				p.error(from, end, "stderr redirection requires a command")
				break
			}
			if stderrRedirection(target) {
				p.error(from, end, "duplicate stderr redirection")
				break
			}
			p.pos = end
			// Parse the destination before creating the node: a rejected
			// redirection must not leave an unattached expression behind.
			var path *Expr
			if op != "2>&1" {
				p.commandSpace(capture)
				if p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' || p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == '#' || (commandOperator(p.s.Text[p.pos]) && p.s.Text[p.pos] != '(') {
					p.error(from, p.pos, "expected redirection path")
					break
				}
				path = p.commandWord(false)
			}
			redirection := p.node(CommandRedirection, from)
			redirection.Text = op
			if op == "2>&1" {
				redirection.Bool = aliasAfterWriter
			} else {
				redirection.Items = slices.Append(p.a, redirection.Items, path)
			}
			redirection.Span.End = p.pos
			target.Items = slices.Append(p.a, target.Items, redirection)
			if len(p.diags) != 0 {
				break
			}
			continue
		}
		if p.pos+1 < len(p.s.Text) && (p.s.Text[p.pos:p.pos+2] == ">&" || p.s.Text[p.pos:p.pos+2] == ">@") {
			from := p.pos
			if p.s.Text[p.pos+1] == '&' && p.pos+2 < len(p.s.Text) && channelNameByte(p.s.Text[p.pos+2], true) {
				if output || e.Exit != "" || stage.Kind == CommandWriter {
					p.error(from, from+2, "duplicate channel output disposition")
					break
				}
				if !hasExecutable(stage) && len(stage.Items) != 0 {
					p.error(from, from+2, "channel endpoints cannot carry stage setup or redirections")
					break
				}
				endpoint := p.channelEndpoint(CommandWriter, 2)
				if !hasExecutable(stage) {
					freeExpr(p.a, stage)
					e.Items[len(e.Items)-1] = endpoint
				} else {
					stage.Span.End = from
					e.Items = slices.Append(p.a, e.Items, endpoint)
				}
				stage, output = endpoint, true
				continue
			}
			if !branch || !hasExecutable(stage) || output || e.Exit != "" {
				p.error(from, from+2, "branch rejoin has no valid continuation scope")
				p.diags[len(p.diags)-1].Code = "KASH_REJOIN_OUT_OF_SCOPE"
				break
			}
			e.Exit = "next"
			if p.s.Text[from+1] == '@' {
				e.Exit = "otherwise"
			}
			p.pos += 2
			p.commandSpace(true)
			if p.pos == len(p.s.Text) || p.s.Text[p.pos] != ')' {
				p.error(from, p.pos, "rejoin must be the final branch disposition")
				break
			}
			break
		}
		if stage.Kind == CommandGroup || stage.Kind == CommandMux || stage.Kind == CommandReader || stage.Kind == CommandWriter {
			p.error(p.pos, p.pos+1, "expected a process connection or statement end after graph operand")
			break
		}
		if p.pos+1 < len(p.s.Text) && p.s.Text[p.pos] == '<' && (p.s.Text[p.pos+1] == '&' || p.s.Text[p.pos+1] == '(') {
			if p.pos > start && !commandWhitespace(p.s.Text[p.pos-1]) {
				p.error(p.pos, p.pos+2, "live stream operands must be standalone arguments")
				break
			}
			if !hasExecutable(stage) {
				p.error(p.pos, p.pos+2, "live stream operands cannot be command heads")
				break
			}
			operand := p.node(CommandOperand, p.pos)
			producer := p.s.Text[p.pos+1] == '('
			p.pos += 2
			if producer {
				p.commandSpace(true)
				operand.Body = slices.Append(p.a, operand.Body, p.commandPipeline(p.pos, false, false, true))
				if p.pos == len(p.s.Text) || p.s.Text[p.pos] != ')' {
					p.error(operand.Span.Start, p.pos, "unclosed live producer graph")
				} else {
					p.pos++
				}
			} else {
				from := p.pos
				for p.pos < len(p.s.Text) && channelNameByte(p.s.Text[p.pos], p.pos == from) {
					p.pos++
				}
				operand.Text = p.s.Text[from:p.pos]
				if operand.Text != "" && !ChannelName(operand.Text) {
					p.error(from, p.pos, "invalid or reserved channel name")
				}
			}
			if p.pos < len(p.s.Text) && !commandWhitespace(p.s.Text[p.pos]) && p.s.Text[p.pos] != ')' && p.s.Text[p.pos] != ',' && p.s.Text[p.pos] != ';' && p.s.Text[p.pos] != '|' && p.s.Text[p.pos] != '>' && p.s.Text[p.pos] != '&' && p.s.Text[p.pos] != '?' && p.s.Text[p.pos] != '#' {
				p.error(operand.Span.Start, p.pos+1, "live stream operands must be standalone arguments")
			}
			operand.Span.End = p.pos
			stage.Items = slices.Append(p.a, stage.Items, operand)
			if len(p.diags) != 0 {
				break
			}
			continue
		}
		if p.s.Text[p.pos] == '<' || p.s.Text[p.pos] == '>' {
			from := p.pos
			op := p.s.Text[p.pos : p.pos+1]
			p.pos++
			if op == ">" && p.pos < len(p.s.Text) && p.s.Text[p.pos] == '>' {
				p.pos++
				op = ">>"
			}
			if op == "<" && input {
				p.error(from, p.pos, "duplicate stdin redirection")
				break
			}
			if op != "<" && output {
				p.error(from, p.pos, "duplicate stdout redirection")
				break
			}
			p.commandSpace(capture)
			if p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' || p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == '#' || (commandOperator(p.s.Text[p.pos]) && p.s.Text[p.pos] != '(') {
				p.error(from, p.pos, "expected redirection path")
				break
			}
			redirection := p.node(CommandRedirection, from)
			redirection.Text = op
			redirection.Items = slices.Append(p.a, redirection.Items, p.commandWord(false))
			redirection.Span.End = p.pos
			stage.Items = slices.Append(p.a, stage.Items, redirection)
			if op == "<" {
				input = true
			} else {
				output = true
			}
			if len(p.diags) != 0 {
				break
			}
			continue
		}
		if !hasExecutable(stage) && p.s.Text[p.pos] == ':' {
			from := p.pos
			p.pos++
			keyStart := p.pos
			for p.pos < len(p.s.Text) && setupKeyByte(p.s.Text[p.pos], p.pos == keyStart) {
				p.pos++
			}
			key := p.s.Text[keyStart:p.pos]
			kind, valid := ClassifySetupKey(key)
			if !valid || kind == SetupAsync || p.pos == len(p.s.Text) || (p.s.Text[p.pos] != ' ' && p.s.Text[p.pos] != '\t' && p.s.Text[p.pos] != '\n' && p.s.Text[p.pos] != '\r') {
				p.error(from, p.pos, "invalid Kash stage setup key")
				break
			}
			duplicate := false
			for i := range stage.Items {
				if stage.Items[i].Kind == CommandSetup && stage.Items[i].Text == key {
					duplicate = true
				}
			}
			if duplicate {
				p.error(from, p.pos, "duplicate Kash stage setup key")
				break
			}
			p.commandSpace(capture)
			if p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' || p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == '#' || (commandOperator(p.s.Text[p.pos]) && p.s.Text[p.pos] != '(') {
				p.error(from, p.pos, "expected Kash stage setup value")
				break
			}
			setup := p.node(CommandSetup, from)
			setup.Text = key
			if key == "accept-exit" && p.s.Text[p.pos] == '[' {
				word := p.node(CommandWord, p.pos)
				word.Bool = true
				inner := p.expression()
				word.Parts = slices.Append(p.a, word.Parts, StringPart{Expr: inner, Form: "(", Span: inner.Span})
				word.Span.End = p.pos
				setup.Items = slices.Append(p.a, setup.Items, word)
			} else {
				setup.Items = slices.Append(p.a, setup.Items, p.commandWord(false))
			}
			setup.Span.End = p.pos
			stage.Items = slices.Append(p.a, stage.Items, setup)
			if len(p.diags) != 0 {
				break
			}
			continue
		}
		if p.s.Text[p.pos] == '#' {
			for p.pos < len(p.s.Text) && p.s.Text[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		before := p.pos
		word := p.commandWord(true)
		if !hasExecutable(stage) && (p.s.Text[before:p.pos] == "exit" || p.s.Text[before:p.pos] == "with" || p.s.Text[before:p.pos] == "break" || p.s.Text[before:p.pos] == "continue") {
			p.error(before, p.pos, "control keywords are statements, not process stages")
		}
		if word != nil {
			stage.Items = slices.Append(p.a, stage.Items, word)
		}
		if len(p.diags) != 0 || p.pos == before {
			break
		}
	}
	if !hasExecutable(stage) {
		p.error(start, p.pos, "expected Kash executable")
	}
	if stage.Kind == CommandMux && (stage.Text == "broadcast" || stage.Text == "balance") {
		p.error(stage.Span.Start, p.pos, "fan-out requires a destination group")
	}
	for i := 1; i < len(e.Items); i++ {
		candidate := e.Items[i]
		if candidate.Kind != CommandStage {
			continue
		}
		claimed := false
		for j := range candidate.Items {
			word := candidate.Items[j]
			if word.Kind == CommandOperand && word.Text == "" && len(word.Body) == 0 {
				claimed = true
			}
		}
		for j := range candidate.Items {
			word := candidate.Items[j]
			if word.Kind == CommandRedirection && word.Text == "<" && !claimed {
				p.error(word.Span.Start, word.Span.End, "stdin redirection conflicts with pipeline input")
			}
		}
	}
	if stage.Span.End == stage.Span.Start {
		stage.Span.End = p.pos
	}
	e.Span.End = p.pos
	if len(e.Body) != 0 && e.Body[0].Async {
		e.Async = true
		e.Body[0].Async = false
	}
	p.kash = previousKash
	p.commandGroup = previousGroup
	return e
}

func (p *parser) commandSpace(capture bool) {
	if capture {
		p.skipSpace()
		return
	}
	for p.pos < len(p.s.Text) && (p.s.Text[p.pos] == ' ' || p.s.Text[p.pos] == '\t' || p.s.Text[p.pos] == '\r') {
		p.pos++
	}
}

func hasExecutable(stage *Expr) bool {
	if stage.Kind == CommandMux || stage.Kind == CommandInput {
		return true
	}
	if stage.Kind == CommandReader || stage.Kind == CommandWriter {
		return true
	}
	if stage.Kind == CommandGroup {
		return len(stage.Items) >= 2
	}
	for i := range stage.Items {
		if stage.Items[i].Kind == CommandWord {
			return true
		}
	}
	return false
}

func setupKeyByte(b byte, first bool) bool {
	return b == '_' || (!first && b == '-') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (!first && b >= '0' && b <= '9')
}

func commandWhitespace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

func commandOperator(b byte) bool {
	return b == '|' || b == '<' || b == '>' || b == '&' || b == '?' || b == ';' || b == '(' || b == '\''
}

func stderrRedirection(stage *Expr) bool {
	for i := range stage.Items {
		text := stage.Items[i].Text
		if stage.Items[i].Kind == CommandRedirection && (text == "2>" || text == "2>>" || text == "2>&1") {
			return true
		}
	}
	return false
}

// descriptorKind classifies a numeric descriptor glued to '>'.
type descriptorKind int

const (
	descriptorNone        descriptorKind = iota // not a descriptor: an ordinary argument
	descriptorUnsupported                       // 3>, 1>&2, 2>&-: rejected, never guessed
	descriptorErrorFile                         // 2>
	descriptorErrorAppend                       // 2>>
	descriptorErrorAlias                        // 2>&1
)

// attachedDescriptor recognizes a numeric descriptor glued to '>'. It returns
// the end offset of the whole form and its kind.
func attachedDescriptor(text string, pos int) (int, descriptorKind) {
	if pos >= len(text) || text[pos] < '0' || text[pos] > '9' {
		return pos, descriptorNone
	}
	i := pos
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == pos || i >= len(text) || text[i] != '>' {
		return pos, descriptorNone
	}
	digits := text[pos:i]
	end := i + 1
	if i+1 < len(text) && (text[i+1] == '>' || text[i+1] == '&') {
		end = i + 2
	}
	if digits != "2" {
		return end, descriptorUnsupported
	}
	if i+1 < len(text) && text[i+1] == '>' {
		return i + 2, descriptorErrorAppend
	}
	if i+1 < len(text) && text[i+1] == '&' {
		if i+2 < len(text) && text[i+2] == '1' && descriptorBoundary(text, i+3) {
			return i + 3, descriptorErrorAlias
		}
		if i+2 < len(text) {
			end = i + 3
		}
		return end, descriptorUnsupported
	}
	return i + 1, descriptorErrorFile
}

func descriptorBoundary(text string, pos int) bool {
	if pos >= len(text) {
		return true
	}
	b := text[pos]
	return commandWhitespace(b) || b == '|' || b == '&' || b == ';' || b == ')' || b == ',' || b == '#' || b == '?' || b == '<' || b == '>'
}

func (p *parser) commandWord(allowSpread bool) *Expr {
	start := p.pos
	e := p.node(CommandWord, start)
	if strings.HasPrefix(p.s.Text[start:], "/") || strings.HasPrefix(p.s.Text[start:], "./") || strings.HasPrefix(p.s.Text[start:], "../") || strings.HasPrefix(p.s.Text[start:], "@(") || strings.HasPrefix(p.s.Text[start:], "@{") {
		inner := p.pathAtom(true)
		if len(inner.Parts) != 0 {
			e.Parts = slices.Append(p.a, e.Parts, StringPart{Expr: inner, Form: "path", Span: inner.Span})
		} else {
			freeExpr(p.a, inner)
			p.pos = start
		}
	}
	text := strings.NewBuilder(p.a)
	quoted, hadQuote, spread := false, false, false
	wordDelimiters := 0
	partStart := p.pos
	for p.pos < len(p.s.Text) {
		b := p.s.Text[p.pos]
		listOperator := p.pos+1 < len(p.s.Text) && (p.s.Text[p.pos:p.pos+2] == "&&" || p.s.Text[p.pos:p.pos+2] == "||")
		if !quoted && ((p.commandGroup && b == ',' && wordDelimiters == 0) || listOperator || b == '?' || b == ')' || b == ';' || b == '|' || b == '<' || b == '>' || b == ' ' || b == '\t' || b == '\r' || b == '\n') {
			break
		}
		if b == '"' {
			quoted = !quoted
			hadQuote = true
			p.pos++
			continue
		}
		if !quoted && b == '(' {
			p.addTextPart(e, &text, partStart, p.pos)
			from := p.pos
			previousKash := p.kash
			p.kash = true
			inner := p.expression()
			p.kash = previousKash
			if inner != nil {
				e.Parts = slices.Append(p.a, e.Parts, StringPart{Expr: inner, Form: "(", Span: source.Span{Start: from, End: p.pos}})
			}
			partStart = p.pos
			if len(p.diags) != 0 {
				break
			}
			continue
		}
		if b == '\\' {
			from := p.pos
			p.pos++
			if !quoted && p.pos+1 < len(p.s.Text) && (p.s.Text[p.pos:p.pos+2] == "@(" || p.s.Text[p.pos:p.pos+2] == "@{") {
				p.error(from, p.pos+2, "direct Kash has no template layer; use $name / ${name} for a reference or (function args...) for a call")
				break
			}
			if p.pos == len(p.s.Text) {
				p.error(from, p.pos, "unfinished Kash escape")
				break
			}
			escaped := p.s.Text[p.pos]
			if !quoted && escaped == '\r' && p.pos+1 < len(p.s.Text) && p.s.Text[p.pos+1] == '\n' {
				p.pos += 2
				continue
			}
			if !quoted && escaped == '\n' {
				p.pos++
				continue
			}
			if quoted {
				if escaped == 'n' {
					escaped = '\n'
				} else if escaped == 'r' {
					escaped = '\r'
				} else if escaped == 't' {
					escaped = '\t'
				} else if escaped != '"' && escaped != '\\' && escaped != '$' && escaped != '@' {
					p.error(from, p.pos+1, "invalid Kash quoted escape")
				}
			}
			text.WriteByte(escaped)
			p.pos++
			continue
		}
		if !quoted && b == '@' && p.pos+1 < len(p.s.Text) && (p.s.Text[p.pos+1] == '(' || p.s.Text[p.pos+1] == '{') {
			p.error(p.pos, p.pos+2, "direct Kash has no template layer; use $name / ${name} for a reference or (function args...) for a call")
			break
		}
		if !quoted && b == '@' && p.pos == start {
			if p.pos+1 < len(p.s.Text) && isNameStart(p.s.Text[p.pos+1]) {
				p.error(p.pos, p.pos+2, "template directives are invalid in Kash; use Kash if/match or an explicit render expression")
				break
			}
			inner := p.selector()
			e.Parts = slices.Append(p.a, e.Parts, StringPart{Expr: inner, Form: "selector", Span: source.Span{Start: start, End: p.pos}})
			partStart = p.pos
			if len(p.diags) != 0 {
				break
			}
			continue
		}
		if b == '$' {
			p.addTextPart(e, &text, partStart, p.pos)
			from := p.pos
			form := "$"
			var inner *Expr
			if p.pos+1 < len(p.s.Text) && p.s.Text[p.pos+1] == '(' {
				form = "$("
				inner = p.commandCapture()
			} else {
				p.pos++
				if strings.HasPrefix(p.s.Text[p.pos:], "...") {
					form, spread = "$...", true
					p.pos += 3
				}
				braced := p.pos < len(p.s.Text) && p.s.Text[p.pos] == '{'
				if braced {
					if spread {
						form = "$...{"
					} else {
						form = "${"
					}
					p.pos++
				}
				if p.pos == len(p.s.Text) || !isNameStart(p.s.Text[p.pos]) {
					p.error(from, p.pos, "expected Kame reference after $")
				} else {
					inner = p.reference()
				}
				if braced {
					if p.pos == len(p.s.Text) || p.s.Text[p.pos] != '}' {
						p.error(from, p.pos, "unclosed braced Kame reference")
					} else {
						p.pos++
					}
				}
			}
			if inner != nil {
				e.Parts = slices.Append(p.a, e.Parts, StringPart{Expr: inner, Form: form, Span: source.Span{Start: from, End: p.pos}})
			}
			partStart = p.pos
			if len(p.diags) != 0 {
				break
			}
			continue
		}
		if !quoted && b == '@' {
			p.error(p.pos, p.pos+1, "literal at-sign in a Kash word must be quoted, for example \"--mtime=@0\"")
			break
		}
		if !quoted && commandOperator(b) {
			p.error(p.pos, p.pos+1, "unsupported Kash process operator or reserved syntax")
			break
		}
		if !quoted {
			if b == '{' || b == '[' {
				wordDelimiters++
			}
			if (b == '}' || b == ']') && wordDelimiters > 0 {
				wordDelimiters--
			}
		}
		text.WriteByte(b)
		p.pos++
	}
	p.addTextPart(e, &text, partStart, p.pos)
	text.Free()
	if quoted {
		p.error(start, p.pos, "unclosed Kash quoted word")
	}
	// A standalone unquoted substitution retains its typed value (list splice).
	e.Bool = !hadQuote && len(e.Parts) == 1 && e.Parts[0].Expr != nil
	if spread && (!allowSpread || !e.Bool) {
		p.error(start, p.pos, "list spread requires a standalone unquoted command argv word")
	}
	e.Span.End = p.pos
	return e
}

// Bool records typed substitution semantics, not whether a literal needs quotes.
func commandWordNeedsQuotes(e *Expr) bool {
	if e.Bool {
		return false
	}
	if len(e.Parts) == 0 {
		return true
	}
	if len(e.Parts) == 1 && e.Parts[0].Expr != nil {
		return true
	}
	for i := range e.Parts {
		part := e.Parts[i]
		if part.Expr != nil {
			continue
		}
		// Keep grammar-sensitive literals quoted even outside executable position.
		if len(e.Parts) == 1 && (part.Text == "=" || part.Text == "if" || part.Text == "elif" || part.Text == "else" || part.Text == "match" || part.Text == "case" || part.Text == "defer" || part.Text == "defn" || part.Text == "return" || part.Text == "let" || part.Text == "exit" || part.Text == "with" || part.Text == "break" || part.Text == "continue") {
			return true
		}
		if i == 0 && len(part.Text) != 0 && (part.Text[0] == ':' || part.Text[0] == '#') {
			return true
		}
		for j := 0; j < len(part.Text); j++ {
			c := part.Text[j]
			if commandWhitespace(c) || commandOperator(c) || c == ',' || c == ')' || c == '"' || c == '\\' || c == '$' || c == '@' {
				return true
			}
		}
	}
	return false
}

func writeCommandWord(b *strings.Builder, e *Expr) {
	// Expressions must stay outside command quotes. Quote only the adjacent
	// literal/reference fragments, preserving scalar words versus typed splices.
	mixedExpression := false
	for i := range e.Parts {
		if e.Parts[i].Form == "(" {
			mixedExpression = true
		}
	}
	if mixedExpression {
		if !e.Bool && len(e.Parts) == 1 {
			b.WriteString("\"\"")
		}
		for i := range e.Parts {
			part := e.Parts[i]
			if part.Form == "(" || part.Form == "path" || part.Form == "selector" {
				writeExpr(b, part.Expr)
				continue
			}
			fragment := Expr{Kind: CommandWord, Parts: e.Parts[i : i+1]}
			writeCommandWord(b, &fragment)
		}
		return
	}
	quoted := commandWordNeedsQuotes(e)
	if quoted {
		b.WriteByte('"')
	}
	for i := range e.Parts {
		part := e.Parts[i]
		if part.Expr != nil {
			boundary := false
			if part.Form == "$" && i+1 < len(e.Parts) && len(e.Parts[i+1].Text) != 0 {
				c := e.Parts[i+1].Text[0]
				boundary = isNameContinue(c) || c == '/' || c == '.' || c == '?' || c == '!'
			}
			if boundary && !quoted {
				b.WriteByte('"')
			}
			if part.Form == "$(" {
				writeExpr(b, part.Expr)
			} else if part.Form == "(" || part.Form == "selector" || part.Form == "path" {
				writeExpr(b, part.Expr)
			} else {
				b.WriteString(part.Form)
				writeExpr(b, part.Expr)
				if part.Form == "${" || part.Form == "$...{" {
					b.WriteByte('}')
				}
			}
			// Literal suffixes must not become reference namespaces, names, fields or flags.
			if boundary {
				if quoted {
					b.WriteString("\"\"")
				} else {
					b.WriteByte('"')
				}
			}
			continue
		}
		for j := 0; j < len(part.Text); j++ {
			c := part.Text[j]
			if c == '"' || c == '\\' || c == '$' || c == '@' {
				b.WriteByte('\\')
				b.WriteByte(c)
			} else if c == '\n' {
				b.WriteString("\\n")
			} else if c == '\r' {
				b.WriteString("\\r")
			} else if c == '\t' {
				b.WriteString("\\t")
			} else {
				b.WriteByte(c)
			}
		}
	}
	if quoted {
		b.WriteByte('"')
	}
}

// ParseReferencePrefix shares the complete Kame reference grammar with Kash.
func ParseReferencePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start}
	var e *Expr
	if start >= len(s.Text) || !isNameStart(s.Text[start]) {
		p.error(start, start, "expected Kame reference")
	} else {
		e = p.reference()
	}
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}
