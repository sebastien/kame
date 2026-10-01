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

// ParseKashValuePrefix adds Kash's standalone value wrappers, not word-list RHSs.
func ParseKashValuePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	// Kash owns top-level semicolons. Bound an atom's source view so number and
	// path scanners keep Kame's grammar without swallowing the next statement.
	bounded := mem.Alloc[source.Source](a)
	*bounded = *s
	if start < len(s.Text) && s.Text[start] != '(' && s.Text[start] != '[' && s.Text[start] != '"' && s.Text[start] != '$' && s.Text[start] != '@' {
		depth := 0
		for i := start; i < len(s.Text); i++ {
			if s.Text[i] == '\\' { i++; continue }
			if s.Text[i] == '{' { depth++ }
			if s.Text[i] == '}' && depth > 0 { depth-- }
			if depth == 0 && s.Text[i] == ';' { bounded.Text = s.Text[:i]; break }
			if depth == 0 && (s.Text[i] == ' ' || s.Text[i] == '\t' || s.Text[i] == '\n' || s.Text[i] == '\r') { break }
		}
	}
	p := parser{a: a, s: bounded, pos: start, kash: true}
	var e *Expr
	if start < len(s.Text) && ((s.Text[start] == '$' && (start+1 == len(s.Text) || s.Text[start+1] != '(')) || (start+1 < len(s.Text) && s.Text[start:start+2] == "@(")) {
		word := p.commandWord()
		if word.Bool {
			e = word.Parts[0].Expr
			word.Parts[0].Expr = nil
		} else { p.error(start, p.pos, "definition value must be one Kame expression or standalone substitution") }
		freeExpr(a, word)
	} else { e = p.expression() }
	p.s = s
	mem.Free(a, bounded)
	end := p.pos
	for end < len(s.Text) && (s.Text[end] == ' ' || s.Text[end] == '\t' || s.Text[end] == '\r') { end++ }
	if len(p.diags) == 0 && end > p.pos && end+2 < len(s.Text) && s.Text[end:end+2] == "??" && commandWhitespace(s.Text[end+2]) {
		rightStart := end+2
		for rightStart < len(s.Text) && (s.Text[rightStart] == ' ' || s.Text[rightStart] == '\t' || s.Text[rightStart] == '\r') { rightStart++ }
		if rightStart == len(s.Text) || s.Text[rightStart] == '\n' || s.Text[rightStart] == ';' || s.Text[rightStart] == '#' { p.error(end, rightStart, "expected value after ??") } else {
			right := ParseKashValuePrefix(a, s, rightStart)
			for i := range right.Diagnostics { p.diags = slices.Append(a, p.diags, right.Diagnostics[i]) }
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
	previousKash := p.kash
	p.kash = true
	kind := CommandGraph
	if capture { kind = CommandCapture }
	e := p.node(kind, start)
	stage := p.node(CommandStage, p.pos)
	e.Items = slices.Append(p.a, e.Items, stage)
	input, output := false, false
	for {
		p.commandSpace(capture)
		if p.pos+1 < len(p.s.Text) && p.s.Text[p.pos] == '\\' && p.s.Text[p.pos+1] == '\n' {
			p.pos += 2
			continue
		}
		if p.pos+2 < len(p.s.Text) && p.s.Text[p.pos:p.pos+3] == "\\\r\n" {
			p.pos += 3
			continue
		}
		if p.pos == len(p.s.Text) {
			if capture { p.error(start, p.pos, "unclosed Kash command substitution") }
			break
		}
		if !capture && (p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == ';' || p.s.Text[p.pos] == '#') { break }
		if capture && p.s.Text[p.pos] == ';' { p.error(p.pos, p.pos+1, "command substitution contains exactly one process expression"); break }
		if p.s.Text[p.pos] == '?' {
			from := p.pos
			p.pos++
			if !hasExecutable(stage) { p.error(from, p.pos, "acceptance requires a complete process graph"); break }
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '?' { p.error(from, p.pos+1, "?? is not a raw-command operator"); break }
			stage.Span.End = from
			separated := from > start && commandWhitespace(p.s.Text[from-1]) && p.pos < len(p.s.Text) && commandWhitespace(p.s.Text[p.pos])
			p.commandSpace(capture)
			if !capture && p.pos < len(p.s.Text) && p.s.Text[p.pos] == '&' { e.AcceptExit = true; continue }
			if p.pos < len(p.s.Text) && !((capture && p.s.Text[p.pos] == ')') || (!capture && (p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == ';' || p.s.Text[p.pos] == '#'))) {
				if !separated { p.error(from, p.pos, "command recovery requires whitespace around ?"); break }
				e.Body = slices.Append(p.a, e.Body, p.commandGraph(p.pos, capture))
				break
			}
			e.AcceptExit = true
			if capture && p.pos < len(p.s.Text) && p.s.Text[p.pos] == ')' { p.pos++; break }
			if capture { p.error(start, p.pos, "unclosed Kash command substitution") }
			break
		}
		if p.s.Text[p.pos] == '&' {
			from := p.pos
			p.pos++
			if capture || !hasExecutable(stage) { p.error(from, p.pos, "async syntax requires a complete command statement"); break }
			p.commandSpace(false)
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] != '\n' && p.s.Text[p.pos] != ';' && p.s.Text[p.pos] != '#' { p.error(from, p.pos, "& must terminate the process expression"); break }
			e.Async = true
			break
		}
		if p.s.Text[p.pos] == ')' {
			if !capture { p.error(p.pos, p.pos+1, "unexpected Kash closing parenthesis"); break }
			stage.Span.End = p.pos
			p.pos++
			break
		}
		if p.s.Text[p.pos] == '|' {
			if !hasExecutable(stage) {
				p.error(p.pos, p.pos+1, "expected Kash executable before pipe")
				break
			}
			if output { p.error(p.pos, p.pos+1, "stdout redirection conflicts with pipeline output"); break }
			stage.Span.End = p.pos
			p.pos++
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '|' {
				p.error(p.pos-1, p.pos+1, "|| is not a Kash process operator")
				break
			}
			stage = p.node(CommandStage, p.pos)
			e.Items = slices.Append(p.a, e.Items, stage)
			continue
		}
		if p.s.Text[p.pos] == '<' || p.s.Text[p.pos] == '>' {
			from := p.pos
			op := p.s.Text[p.pos:p.pos+1]
			p.pos++
			if op == ">" && p.pos < len(p.s.Text) && p.s.Text[p.pos] == '>' { p.pos++; op = ">>" }
			if op == "<" && (input || len(e.Items) != 1) { p.error(from, p.pos, "stdin redirection conflicts with pipeline input or earlier redirection"); break }
			if op != "<" && output { p.error(from, p.pos, "duplicate stdout redirection"); break }
			p.commandSpace(capture)
			if p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' || p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == '#' || commandOperator(p.s.Text[p.pos]) { p.error(from, p.pos, "expected redirection path"); break }
			redirection := p.node(CommandRedirection, from)
			redirection.Text = op
			redirection.Items = slices.Append(p.a, redirection.Items, p.commandWord())
			redirection.Span.End = p.pos
			stage.Items = slices.Append(p.a, stage.Items, redirection)
			if op == "<" { input = true } else { output = true }
			if len(p.diags) != 0 { break }
			continue
		}
		if !hasExecutable(stage) && p.s.Text[p.pos] == ':' {
			from := p.pos
			p.pos++
			keyStart := p.pos
			for p.pos < len(p.s.Text) && setupKeyByte(p.s.Text[p.pos], p.pos == keyStart) { p.pos++ }
			key := p.s.Text[keyStart:p.pos]
			if key == "" || key == "async" || p.pos == len(p.s.Text) || (p.s.Text[p.pos] != ' ' && p.s.Text[p.pos] != '\t' && p.s.Text[p.pos] != '\n' && p.s.Text[p.pos] != '\r') { p.error(from, p.pos, "invalid Kash stage setup key"); break }
			duplicate := false
			for i := range stage.Items { if stage.Items[i].Kind == CommandSetup && stage.Items[i].Text == key { duplicate = true } }
			if duplicate { p.error(from, p.pos, "duplicate Kash stage setup key"); break }
			p.commandSpace(capture)
			if p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' || p.s.Text[p.pos] == '\n' || p.s.Text[p.pos] == '#' || commandOperator(p.s.Text[p.pos]) { p.error(from, p.pos, "expected Kash stage setup value"); break }
			setup := p.node(CommandSetup, from)
			setup.Text = key
			setup.Items = slices.Append(p.a, setup.Items, p.commandWord())
			setup.Span.End = p.pos
			stage.Items = slices.Append(p.a, stage.Items, setup)
			if len(p.diags) != 0 { break }
			continue
		}
		if p.s.Text[p.pos] == '#' {
			for p.pos < len(p.s.Text) && p.s.Text[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		before := p.pos
		word := p.commandWord()
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
	if stage.Span.End == stage.Span.Start { stage.Span.End = p.pos }
	e.Span.End = p.pos
	if len(e.Body) != 0 && e.Body[0].Async { e.Async = true; e.Body[0].Async = false }
	p.kash = previousKash
	return e
}

func (p *parser) commandSpace(capture bool) {
	if capture { p.skipSpace(); return }
	for p.pos < len(p.s.Text) && (p.s.Text[p.pos] == ' ' || p.s.Text[p.pos] == '\t' || p.s.Text[p.pos] == '\r') { p.pos++ }
}

func hasExecutable(stage *Expr) bool {
	for i := range stage.Items { if stage.Items[i].Kind == CommandWord { return true } }
	return false
}

func setupKeyByte(b byte, first bool) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (!first && b >= '0' && b <= '9')
}

func commandWhitespace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

func commandOperator(b byte) bool {
	return b == '|' || b == '<' || b == '>' || b == '&' || b == '?' || b == ';' || b == '(' || b == '\''
}

func (p *parser) commandWord() *Expr {
	start := p.pos
	e := p.node(CommandWord, start)
	text := strings.NewBuilder(p.a)
	quoted, hadQuote := false, false
	partStart := start
	for p.pos < len(p.s.Text) {
		b := p.s.Text[p.pos]
		if !quoted && (b == '?' || b == ')' || b == ';' || b == '|' || b == '<' || b == '>' || b == ' ' || b == '\t' || b == '\r' || b == '\n') {
			break
		}
		if b == '"' {
			quoted = !quoted
			hadQuote = true
			p.pos++
			continue
		}
		if b == '\\' {
			from := p.pos
			p.pos++
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
		if b == '$' || (b == '@' && p.pos+1 < len(p.s.Text) && p.s.Text[p.pos+1] == '(') {
			p.addTextPart(e, &text, partStart, p.pos)
			from := p.pos
			form := "$"
			var inner *Expr
			if b == '@' {
				form = "@"
				p.pos++
				inner = p.expressionBoundary()
			} else if p.pos+1 < len(p.s.Text) && p.s.Text[p.pos+1] == '(' {
				form = "$("
				inner = p.commandCapture()
			} else {
				p.pos++
				braced := p.pos < len(p.s.Text) && p.s.Text[p.pos] == '{'
				if braced {
					form = "${"
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
		if !quoted && (commandOperator(b) || b == '@') {
			p.error(p.pos, p.pos+1, "unsupported Kash process operator or reserved syntax")
			break
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
	e.Span.End = p.pos
	return e
}

// The boundary's parentheses are not necessarily application parentheses:
// @([1 2]) is one list value, while @([x] x) delegates to Kame's lambda parser.
func (p *parser) expressionBoundary() *Expr {
	open := p.pos
	p.pos++
	p.skipSpace()
	first := p.expression()
	if first == nil {
		return nil
	}
	p.skipSpace()
	if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ')' {
		p.pos++
		return first
	}
	// Multiple expressions and pipes belong to the existing parenthesized-form
	// grammar. Reparse that form on the same Source without translating text.
	freeExpr(p.a, first)
	p.pos = open
	return p.paren()
}

func writeCommandWord(b *strings.Builder, e *Expr) {
	if !e.Bool {
		b.WriteByte('"')
	}
	for i := range e.Parts {
		part := e.Parts[i]
		if part.Expr != nil {
			if part.Form == "$(" {
				writeExpr(b, part.Expr)
			} else if part.Form == "@" {
				b.WriteString("@(")
				writeExpr(b, part.Expr)
				b.WriteByte(')')
			} else {
				b.WriteString(part.Form)
				writeExpr(b, part.Expr)
				if part.Form == "${" {
					b.WriteByte('}')
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
	if !e.Bool {
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
