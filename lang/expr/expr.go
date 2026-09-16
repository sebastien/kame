// Package expr parses and formats LittleMake expressions.
package expr

import (
	"littlemake/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

type Kind int

const (
	Invalid Kind = iota
	Boolean
	Nil
	Integer
	Float
	String
	Symbol
	Name
	Path
	Selector
	Reference
	List
	Record
	Application
	Lambda
)

type ReferenceKind int

const (
	ReferenceName ReferenceKind = iota
	ReferenceIndex
	ReferenceSlice
	ReferenceSelection
)

type ReferencePart struct {
	Kind ReferenceKind
	Text string
	Span source.Span
}

type Field struct {
	Key  string
	Span source.Span
	Value *Expr
}

type Parameter struct {
	Name string
	Span source.Span
	Rest bool
}

type StringPart struct {
	Text string
	Span source.Span
	Expr *Expr
	Brace bool
}

// Expr is immutable after parsing. All child storage is owned by Result.Alloc.
type Expr struct {
	Kind       Kind
	Span       source.Span
	Text       string
	Bool       bool
	Int        int64
	Float      float64
	Parts      []StringPart
	Reference  []ReferencePart
	Items      []*Expr
	Fields     []Field
	Parameters []Parameter
	Body       []*Expr
}

type Result struct {
	Alloc       mem.Allocator
	Source      *source.Source
	Expr        *Expr
	Diagnostics []source.Diagnostic
}

// Prefix is an expression parsed from an existing Source. The caller owns the
// diagnostics slice and the expression tree; the Source remains caller-owned.
type Prefix struct {
	Expr        *Expr
	End         int
	Diagnostics []source.Diagnostic
}

func (r *Result) Free() {
	if r == nil { return }
	a := r.Alloc
	freeExpr(a, r.Expr)
	slices.Free(a, r.Diagnostics)
	r.Source.Free(a)
	*r = Result{}
	mem.Free(a, r)
}

func freeExpr(a mem.Allocator, e *Expr) {
	if e == nil { return }
	for i := range e.Parts {
		mem.FreeString(a, e.Parts[i].Text)
		freeExpr(a, e.Parts[i].Expr)
	}
	for i := range e.Items { freeExpr(a, e.Items[i]) }
	for i := range e.Fields { freeExpr(a, e.Fields[i].Value) }
	for i := range e.Body { freeExpr(a, e.Body[i]) }
	slices.Free(a, e.Parts)
	slices.Free(a, e.Reference)
	slices.Free(a, e.Items)
	slices.Free(a, e.Fields)
	slices.Free(a, e.Parameters)
	slices.Free(a, e.Body)
	mem.Free(a, e)
}

// Free releases an expression retained by another language AST.
func Free(a mem.Allocator, e *Expr) { freeExpr(a, e) }

type parser struct {
	a     mem.Allocator
	s     *source.Source
	pos   int
	diags []source.Diagnostic
}

func Parse(a mem.Allocator, name string, text string) *Result {
	p := parser{a: a, s: source.New(a, name, text)}
	p.skipSpace()
	e := p.expression()
	p.skipSpace()
	if e != nil && p.pos != len(p.s.Text) { p.error(p.pos, p.pos+1, "unexpected trailing input") }
	r := mem.Alloc[Result](a)
	r.Alloc, r.Source, r.Expr, r.Diagnostics = a, p.s, e, p.diags
	return r
}

func ParsePrefix(a mem.Allocator, s *source.Source, start int) Prefix {
	p := parser{a: a, s: s, pos: start}
	e := p.expression()
	return Prefix{Expr: e, End: p.pos, Diagnostics: p.diags}
}

func (p *parser) error(start int, end int, message string) {
	if end > len(p.s.Text) { end = len(p.s.Text) }
	p.diags = slices.Append(p.a, p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

func (p *parser) node(kind Kind, start int) *Expr {
	e := mem.Alloc[Expr](p.a)
	e.Kind, e.Span = kind, source.Span{Start: start, End: p.pos}
	return e
}

func (p *parser) skipSpace() int {
	start := p.pos
	for p.pos < len(p.s.Text) {
		b := p.s.Text[p.pos]
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' { break }
		p.pos++
	}
	return p.pos - start
}

func (p *parser) expression() *Expr {
	if p.pos == len(p.s.Text) { p.error(p.pos, p.pos, "expected expression"); return nil }
	start := p.pos
	switch p.s.Text[p.pos] {
	case '(':
		return p.paren()
	case '[':
		return p.list()
	case '"':
		return p.quoted()
	case ':':
		return p.symbol()
	case '@':
		return p.selector()
	case '?':
		p.pos++
		e := p.node(Name, start)
		e.Text = "?"
		return e
	}
	if isNameStart(p.s.Text[p.pos]) { return p.reference() }
	if p.s.Text[p.pos] == '/' || (p.s.Text[p.pos] == '.' && p.pos+1 < len(p.s.Text) && (p.s.Text[p.pos+1] == '/' || (p.pos+2 < len(p.s.Text) && p.s.Text[p.pos+1] == '.' && p.s.Text[p.pos+2] == '/'))) { return p.path() }
	if p.s.Text[p.pos] == '-' || isDigit(p.s.Text[p.pos]) { return p.number() }
	p.error(start, start+1, "expected expression")
	return nil
}

func (p *parser) path() *Expr {
	start := p.pos
	for p.pos < len(p.s.Text) && !isDelimiter(p.s.Text[p.pos]) { p.pos++ }
	e := p.node(Path, start)
	e.Text = p.s.Text[start:p.pos]
	return e
}

func isNameStart(b byte) bool { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func isNameContinue(b byte) bool { return isNameStart(b) || isDigit(b) || b == '-' }
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

type nameResult struct {
	Text string
	Span source.Span
	OK   bool
}

func (p *parser) name() nameResult {
	start := p.pos
	if p.pos == len(p.s.Text) || !isNameStart(p.s.Text[p.pos]) { return nameResult{} }
	p.pos++
	for p.pos < len(p.s.Text) && isNameContinue(p.s.Text[p.pos]) { p.pos++ }
	if p.pos < len(p.s.Text) && (p.s.Text[p.pos] == '?' || p.s.Text[p.pos] == '!') { p.pos++ }
	return nameResult{Text: p.s.Text[start:p.pos], Span: source.Span{Start: start, End: p.pos}, OK: true}
}

func (p *parser) symbol() *Expr {
	start := p.pos
	p.pos++
	name := p.name()
	if !name.OK { p.error(start, p.pos, "expected symbol name"); return nil }
	if name.Text == "true" || name.Text == "false" {
		e := p.node(Boolean, start); e.Bool = name.Text == "true"; return e
	}
	if name.Text == "nil" { return p.node(Nil, start) }
	e := p.node(Symbol, start); e.Text = name.Text; return e
}

func (p *parser) selector() *Expr {
	start := p.pos
	p.pos++
	if p.pos == len(p.s.Text) { p.error(start, p.pos, "invalid selector"); return nil }
	if p.s.Text[p.pos] == '<' || p.s.Text[p.pos] == '>' { p.pos++ }
	if p.pos < len(p.s.Text) && (p.s.Text[p.pos] == '*' || p.s.Text[p.pos] == '#' || p.s.Text[p.pos] == '_') { p.pos++
	} else {
		for p.pos < len(p.s.Text) && (isDigit(p.s.Text[p.pos]) || p.s.Text[p.pos] == '.' || p.s.Text[p.pos] == '-') { p.pos++ }
	}
	text := p.s.Text[start:p.pos]
	if !ValidSelector(text) { p.error(start, p.pos, "invalid selector"); return nil }
	e := p.node(Selector, start); e.Text = text; return e
}

// ValidSelector reports whether text is a complete selector.
func ValidSelector(text string) bool {
	if len(text) < 2 || text[0] != '@' { return false }
	i := 1
	if text[i] == '<' || text[i] == '>' { i++ }
	if i == len(text) { return true }
	if text[i] == '*' || text[i] == '#' || text[i] == '_' { return i+1 == len(text) }
	if text[i] == '-' { i++ }
	start := i
	for i < len(text) && isDigit(text[i]) { i++ }
	if i == start && !(i+1 < len(text) && text[i] == '.' && text[i+1] == '.') { return false }
	if i == len(text) { return true }
	if i+1 >= len(text) || text[i] != '.' || text[i+1] != '.' { return false }
	i += 2
	if i < len(text) && text[i] == '-' { i++ }
	for i < len(text) && isDigit(text[i]) { i++ }
	return i == len(text)
}

func (p *parser) number() *Expr {
	start := p.pos
	for p.pos < len(p.s.Text) && !isDelimiter(p.s.Text[p.pos]) { p.pos++ }
	text := p.s.Text[start:p.pos]
	if !validNumber(text) { p.error(start, p.pos, "invalid number"); return nil }
	if hasFloat(text) {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil { p.error(start, p.pos, "numeric overflow"); return nil }
		e := p.node(Float, start); e.Float = value; return e
	}
	negative := len(text) != 0 && text[0] == '-'
	unsigned := text
	if negative { unsigned = unsigned[1:] }
	base := 10
	if len(unsigned) > 2 && unsigned[0] == '0' && (unsigned[1] == 'b' || unsigned[1] == 'o' || unsigned[1] == 'x') {
		if unsigned[1] == 'b' { base = 2 } else if unsigned[1] == 'o' { base = 8 } else { base = 16 }
		unsigned = unsigned[2:]
	}
	limit := uint64(9223372036854775807)
	if negative { limit++ }
	parsed := parseUnsigned(unsigned, base, limit)
	if !parsed.OK { p.error(start, p.pos, "numeric overflow"); return nil }
	e := p.node(Integer, start)
	if negative {
		if parsed.Value == uint64(9223372036854775807)+1 { e.Int = -9223372036854775807 - 1
		} else { e.Int = -int64(parsed.Value) }
	} else { e.Int = int64(parsed.Value) }
	return e
}

type unsignedResult struct { Value uint64; OK bool }

func parseUnsigned(text string, base int, limit uint64) unsignedResult {
	value := uint64(0)
	for i := 0; i < len(text); i++ {
		if text[i] == '_' { continue }
		digit := uint64(text[i] - '0')
		if text[i] >= 'a' && text[i] <= 'f' { digit = uint64(text[i]-'a') + 10 }
		if text[i] >= 'A' && text[i] <= 'F' { digit = uint64(text[i]-'A') + 10 }
		if value > (limit-digit)/uint64(base) { return unsignedResult{} }
		value = value*uint64(base) + digit
	}
	return unsignedResult{Value: value, OK: true}
}

func isDelimiter(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '(' || b == ')' || b == '[' || b == ']' || b == '"' || b == '|'
}

func validDigits(text string, base int) bool {
	if len(text) == 0 || text[0] == '_' || text[len(text)-1] == '_' { return false }
	lastUnderscore := false
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b == '_' { if lastUnderscore { return false }; lastUnderscore = true; continue }
		lastUnderscore = false
		value := int(b - '0')
		if b >= 'a' && b <= 'f' { value = int(b-'a') + 10 }
		if b >= 'A' && b <= 'F' { value = int(b-'A') + 10 }
		if value < 0 || value >= base { return false }
	}
	return true
}

func hasFloat(text string) bool {
	start := 0
	if len(text) != 0 && text[0] == '-' { start++ }
	if start+2 <= len(text) && text[start] == '0' && (text[start+1] == 'b' || text[start+1] == 'o' || text[start+1] == 'x') { return false }
	for i := 0; i < len(text); i++ { if text[i] == '.' || text[i] == 'e' || text[i] == 'E' { return true } }
	return false
}

func validNumber(text string) bool {
	if len(text) == 0 { return false }
	if text[0] == '-' { text = text[1:]; if len(text) == 0 { return false } }
	if len(text) > 2 && text[0] == '0' && (text[1] == 'b' || text[1] == 'o' || text[1] == 'x') {
		base := 2; if text[1] == 'o' { base = 8 }; if text[1] == 'x' { base = 16 }
		return validDigits(text[2:], base)
	}
	if !hasFloat(text) {
		if !validDigits(text, 10) { return false }
		return len(text) == 1 || text[0] != '0'
	}
	dot, exponent := -1, -1
	for i := 0; i < len(text); i++ {
		if text[i] == '.' { if dot != -1 { return false }; dot = i }
		if text[i] == 'e' || text[i] == 'E' { if exponent != -1 { return false }; exponent = i }
	}
	end := len(text)
	if exponent != -1 {
		end = exponent
		i := exponent + 1
		if i < len(text) && (text[i] == '+' || text[i] == '-') { i++ }
		if !validDigits(text[i:], 10) { return false }
	}
	if dot == -1 { return validDigits(text[:end], 10) }
	return dot < end && validDigits(text[:dot], 10) && validDigits(text[dot+1:end], 10)
}

func (p *parser) reference() *Expr {
	start := p.pos
	name := p.name()
	if p.pos == len(p.s.Text) || p.s.Text[p.pos] != '.' {
		e := p.node(Name, start); e.Text = name.Text; return e
	}
	e := p.node(Reference, start)
	e.Reference = slices.Append(p.a, e.Reference, ReferencePart{Kind: ReferenceName, Text: name.Text, Span: name.Span})
	for p.pos < len(p.s.Text) && p.s.Text[p.pos] == '.' {
		partStart := p.pos
		p.pos++
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '{' {
			p.pos++
			selectionStart := p.pos
			for p.pos < len(p.s.Text) && p.s.Text[p.pos] != '}' { p.pos++ }
			if p.pos == len(p.s.Text) { p.error(partStart, p.pos, "unclosed reference selection"); return e }
			if !validSelection(p.s.Text[selectionStart:p.pos]) { p.error(selectionStart, p.pos, "invalid reference selection") }
			p.pos++
			e.Reference = slices.Append(p.a, e.Reference, ReferencePart{Kind: ReferenceSelection, Text: p.s.Text[selectionStart:p.pos-1], Span: source.Span{Start: partStart, End: p.pos}})
			continue
		}
		componentStart := p.pos
		if p.pos+1 < len(p.s.Text) && p.s.Text[p.pos] == '.' && p.s.Text[p.pos+1] == '.' {
			p.pos += 2
			if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '-' { p.pos++ }
			for p.pos < len(p.s.Text) && isDigit(p.s.Text[p.pos]) { p.pos++ }
		} else if p.pos < len(p.s.Text) && (isDigit(p.s.Text[p.pos]) || p.s.Text[p.pos] == '-') {
			if p.s.Text[p.pos] == '-' { p.pos++ }
			for p.pos < len(p.s.Text) && isDigit(p.s.Text[p.pos]) { p.pos++ }
			if p.pos+1 < len(p.s.Text) && p.s.Text[p.pos] == '.' && p.s.Text[p.pos+1] == '.' {
				p.pos += 2
				if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '.' { p.pos++ }
				if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '-' { p.pos++ }
				for p.pos < len(p.s.Text) && isDigit(p.s.Text[p.pos]) { p.pos++ }
			}
		} else {
			component := p.name()
			if !component.OK { for p.pos < len(p.s.Text) && !isDelimiter(p.s.Text[p.pos]) && p.s.Text[p.pos] != '.' && p.s.Text[p.pos] != '}' { p.pos++ } }
		}
		text := p.s.Text[componentStart:p.pos]
		kind := ReferenceName
		if hasSlice(text) { kind = ReferenceSlice; if !validSlice(text) { p.error(partStart, p.pos, "invalid reference slice") }
		} else if !validIndex(text) {
			if !validName(text) { p.error(partStart, p.pos, "invalid reference component") }
		} else { kind = ReferenceIndex }
		e.Reference = slices.Append(p.a, e.Reference, ReferencePart{Kind: kind, Text: text, Span: source.Span{Start: partStart, End: p.pos}})
	}
	e.Span.End = p.pos
	return e
}

func validName(text string) bool {
	if len(text) == 0 || !isNameStart(text[0]) { return false }
	i := 1
	for i < len(text) && isNameContinue(text[i]) { i++ }
	if i < len(text) && (text[i] == '?' || text[i] == '!') { i++ }
	return i == len(text)
}

func validIndex(text string) bool {
	if len(text) == 0 { return false }
	if text[0] == '-' { text = text[1:] }
	return validDigits(text, 10)
}

func hasSlice(text string) bool {
	for i := 0; i+1 < len(text); i++ { if text[i] == '.' && text[i+1] == '.' { return true } }
	return false
}

func validSlice(text string) bool {
	cut := -1
	for i := 0; i+1 < len(text); i++ { if text[i] == '.' && text[i+1] == '.' { if cut != -1 { return false }; cut = i } }
	if cut == -1 { return false }
	return (cut == 0 || validIndex(text[:cut])) && (cut+2 == len(text) || validIndex(text[cut+2:]))
}

func validSelection(text string) bool {
	if len(text) == 0 { return false }
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == ',' {
			if !validName(text[start:i]) { return false }
			start = i + 1
		}
	}
	return true
}

func (p *parser) list() *Expr {
	start := p.pos; p.pos++; p.skipSpace()
	if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ']' { p.pos++; return p.node(List, start) }
	list := p.node(List, start)
	for p.pos < len(p.s.Text) {
		mark := p.pos
		name := p.name()
		if name.OK && p.pos < len(p.s.Text) && p.s.Text[p.pos] == ':' {
			p.pos++
			record := p.node(Record, start)
			value := p.itemAfterSpace()
			if value == nil { freeExpr(p.a, list); freeExpr(p.a, record); return nil }
			record.Fields = slices.Append(p.a, record.Fields, Field{Key: name.Text, Span: name.Span, Value: value})
			for p.pos < len(p.s.Text) && p.s.Text[p.pos] != ']' {
				if p.skipSpace() == 0 { p.error(p.pos, p.pos+1, "record entries need whitespace"); break }
				if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ']' { break }
				key := p.name()
				if !key.OK || p.pos == len(p.s.Text) || p.s.Text[p.pos] != ':' { p.error(p.pos, p.pos+1, "expected record key"); break }
				p.pos++
				value = p.itemAfterSpace()
				if value == nil { break }
				record.Fields = slices.Append(p.a, record.Fields, Field{Key: key.Text, Span: key.Span, Value: value})
			}
			if p.pos == len(p.s.Text) || p.s.Text[p.pos] != ']' { p.error(start, p.pos, "unclosed record"); freeExpr(p.a, list); return record }
			p.pos++; record.Span.End = p.pos; freeExpr(p.a, list); return record
		}
		p.pos = mark
		item := p.expression()
		if item == nil { break }
		list.Items = slices.Append(p.a, list.Items, item)
		space := p.skipSpace()
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ']' { p.pos++; list.Span.End = p.pos; return list }
		if space == 0 { p.error(p.pos, p.pos+1, "list items need whitespace"); break }
	}
	p.error(start, p.pos, "unclosed list")
	return list
}

func (p *parser) itemAfterSpace() *Expr {
	if p.skipSpace() == 0 { p.error(p.pos, p.pos+1, "expected value after key"); return nil }
	return p.expression()
}

func (p *parser) paren() *Expr {
	start := p.pos; p.pos++; p.skipSpace()
	if p.pos == len(p.s.Text) { p.error(start, p.pos, "unclosed application"); return nil }
	if p.s.Text[p.pos] == ')' { p.pos++; p.error(start, p.pos, "empty application"); return nil }
	if p.s.Text[p.pos] == '[' { return p.lambda(start) }
	first := p.expression()
	if first == nil { return nil }
	app := p.node(Application, start); app.Items = slices.Append(p.a, app.Items, first)
	for p.pos < len(p.s.Text) {
		space := p.skipSpace()
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ')' { p.pos++; app.Span.End = p.pos; return app }
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] == '|' {
			p.pos++
			if p.skipSpace() == 0 { p.error(p.pos, p.pos, "pipe needs right expression"); return app }
			if len(app.Items) != 1 { p.error(app.Span.Start, app.Span.End, "pipe left side must be one expression"); return app }
			left := app.Items[0]
			items := app.Items
			app.Items = nil
			slices.Free(p.a, items)
			mem.Free(p.a, app)
			return p.pipe(start, left)
		}
		if space == 0 { p.error(p.pos, p.pos+1, "application items need whitespace"); return app }
		item := p.expression(); if item == nil { return app }
		app.Items = slices.Append(p.a, app.Items, item)
	}
	p.error(start, p.pos, "unclosed application")
	return app
}

func (p *parser) pipe(start int, left *Expr) *Expr {
	var right []*Expr
	for p.pos < len(p.s.Text) && p.s.Text[p.pos] != ')' && p.s.Text[p.pos] != '|' {
		item := p.expression(); if item == nil { break }
		right = slices.Append(p.a, right, item)
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] != ')' && p.skipSpace() == 0 { p.error(p.pos, p.pos+1, "pipe items need whitespace"); break }
	}
	if len(right) == 0 { p.error(p.pos, p.pos, "pipe needs right expression"); slices.Free(p.a, right); return left }
	if p.pos == len(p.s.Text) { p.error(start, p.pos, "unclosed pipe"); slices.Free(p.a, right); return left }
	result := p.node(Application, start)
	used := false
	for i := range right {
		if right[i].Kind == Name && right[i].Text == "_" {
			if used { result.Items = slices.Append(p.a, result.Items, cloneExpr(p.a, left))
			} else { result.Items = slices.Append(p.a, result.Items, left) }
			freeExpr(p.a, right[i])
			used = true
		} else { result.Items = slices.Append(p.a, result.Items, right[i]) }
	}
	if !used { result.Items = slices.Append(p.a, result.Items, left) }
	slices.Free(p.a, right)
	if p.s.Text[p.pos] == ')' { p.pos++; result.Span.End = p.pos; return result }
	p.pos++
	if p.skipSpace() == 0 { p.error(p.pos, p.pos, "pipe needs right expression"); return result }
	return p.pipe(start, result)
}

func cloneExpr(a mem.Allocator, original *Expr) *Expr {
	if original == nil { return nil }
	copy := mem.Alloc[Expr](a)
	*copy = *original
	copy.Parts, copy.Reference, copy.Items, copy.Fields, copy.Parameters, copy.Body = nil, nil, nil, nil, nil, nil
	for i := range original.Parts {
		part := original.Parts[i]
		if part.Expr != nil { part.Expr = cloneExpr(a, part.Expr) } else { part.Text = sourceText(a, part.Text) }
		copy.Parts = slices.Append(a, copy.Parts, part)
	}
	copy.Reference = slices.Clone(a, original.Reference)
	for i := range original.Items { copy.Items = slices.Append(a, copy.Items, cloneExpr(a, original.Items[i])) }
	for i := range original.Fields { field := original.Fields[i]; field.Value = cloneExpr(a, field.Value); copy.Fields = slices.Append(a, copy.Fields, field) }
	copy.Parameters = slices.Clone(a, original.Parameters)
	for i := range original.Body { copy.Body = slices.Append(a, copy.Body, cloneExpr(a, original.Body[i])) }
	return copy
}

func (p *parser) lambda(start int) *Expr {
	p.pos++
	lambda := p.node(Lambda, start)
	p.skipSpace()
	for p.pos < len(p.s.Text) && p.s.Text[p.pos] != ']' {
		name := p.name()
		if !name.OK { p.error(p.pos, p.pos+1, "expected parameter"); return lambda }
		rest := false
		if p.pos+3 <= len(p.s.Text) && p.s.Text[p.pos:p.pos+3] == "..." { p.pos += 3; rest = true }
		lambda.Parameters = slices.Append(p.a, lambda.Parameters, Parameter{Name: name.Text, Span: source.Span{Start: name.Span.Start, End: p.pos}, Rest: rest})
		if rest && p.pos < len(p.s.Text) && p.s.Text[p.pos] != ']' { p.error(p.pos, p.pos+1, "rest parameter must be last") }
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] != ']' && p.skipSpace() == 0 { p.error(p.pos, p.pos+1, "parameters need whitespace"); return lambda }
	}
	if p.pos == len(p.s.Text) { p.error(start, p.pos, "unclosed parameter list"); return lambda }
	p.pos++
	for p.pos < len(p.s.Text) && p.s.Text[p.pos] != ')' {
		if p.skipSpace() == 0 { p.error(p.pos, p.pos+1, "lambda body needs whitespace"); return lambda }
		if p.pos < len(p.s.Text) && p.s.Text[p.pos] == ')' { break }
		body := p.expression(); if body == nil { return lambda }
		lambda.Body = slices.Append(p.a, lambda.Body, body)
	}
	if p.pos == len(p.s.Text) { p.error(start, p.pos, "unclosed lambda"); return lambda }
	p.pos++; lambda.Span.End = p.pos; return lambda
}

func (p *parser) quoted() *Expr {
	start := p.pos; p.pos++
	e := p.node(String, start)
	var text strings.Builder
	text = strings.NewBuilder(p.a)
	partStart := p.pos
	for p.pos < len(p.s.Text) {
		b := p.s.Text[p.pos]
		if b == '"' {
			p.addTextPart(e, &text, partStart, p.pos)
			p.pos++; e.Span.End = p.pos; text.Free(); return e
		}
		if b == '\\' {
			if p.pos+1 == len(p.s.Text) { p.error(p.pos, p.pos+1, "unfinished escape"); break }
			p.pos++
			escaped := p.s.Text[p.pos]
			if escaped == 'n' { text.WriteByte('\n')
			} else if escaped == 'r' { text.WriteByte('\r')
			} else if escaped == 't' { text.WriteByte('\t')
			} else if escaped == '"' || escaped == '\\' || escaped == '{' { text.WriteByte(escaped)
			} else { p.error(p.pos-1, p.pos+1, "invalid string escape"); text.WriteByte(escaped) }
			p.pos++; continue
		}
		if (b == '@' || b == '{') && p.pos+1 < len(p.s.Text) && p.s.Text[p.pos+1] == '(' {
			p.addTextPart(e, &text, partStart, p.pos)
			open, brace := p.pos, b == '{'; p.pos += 2
			innerStart := p.pos
			inner := p.expression()
			var items []*Expr
			if inner != nil { items = slices.Append(p.a, items, inner) }
			for inner != nil {
				space := p.skipSpace()
				if p.pos == len(p.s.Text) || p.s.Text[p.pos] == ')' { break }
				if space == 0 { p.error(p.pos, p.pos+1, "interpolation expressions need whitespace"); inner = nil; break }
				next := p.expression()
				if next == nil { inner = nil; break }
				items = slices.Append(p.a, items, next)
			}
			if inner == nil {
				for i := range items { freeExpr(p.a, items[i]) }
				slices.Free(p.a, items)
			} else if len(items) > 1 { application := p.node(Application, innerStart); application.Items = items; inner = application
			} else { slices.Free(p.a, items) }
			if inner == nil || p.pos == len(p.s.Text) || p.s.Text[p.pos] != ')' || (brace && (p.pos+1 == len(p.s.Text) || p.s.Text[p.pos+1] != '}')) { p.error(open, p.pos, "unclosed string interpolation"); break }
			p.pos++
			if brace { p.pos++ }
		e.Parts = slices.Append(p.a, e.Parts, StringPart{Expr: inner, Brace: brace, Span: source.Span{Start: open, End: p.pos}}); partStart = p.pos; continue
		}
		text.WriteByte(b); p.pos++
	}
	text.Free(); p.error(start, p.pos, "unclosed string"); return e
}

func (p *parser) addTextPart(e *Expr, text *strings.Builder, start int, end int) {
	if text.Len() == 0 { return }
	e.Parts = slices.Append(p.a, e.Parts, StringPart{Text: sourceText(p.a, text.String()), Span: source.Span{Start: start, End: end}})
	text.Reset()
}

func sourceText(a mem.Allocator, text string) string {
	if len(text) == 0 { return "" }
	b := mem.AllocSlice[byte](a, len(text), len(text)); copy(b, []byte(text)); return string(b)
}

// Format returns allocator-owned canonical expression text.
func Format(a mem.Allocator, e *Expr) string {
	b := strings.NewBuilder(a)
	write(&b, e)
	text := sourceText(a, b.String())
	b.Free()
	return text
}

func write(b *strings.Builder, e *Expr) {
	if e == nil { return }
	if e.Kind == Boolean { if e.Bool { b.WriteString(":true") } else { b.WriteString(":false") }; return }
	if e.Kind == Nil { b.WriteString(":nil"); return }
	if e.Kind == Integer { var buf [strconv.MaxIntBase10Len]byte; b.WriteString(strconv.FormatInt(buf[:], e.Int, 10)); return }
	if e.Kind == Float { var buf [strconv.MaxFloat64Len]byte; b.WriteString(strconv.FormatFloat(buf[:], e.Float, 'g', -1, 64)); return }
	if e.Kind == Symbol { b.WriteByte(':'); b.WriteString(e.Text); return }
	if e.Kind == Name { b.WriteString(e.Text); return }
	if e.Kind == Path { b.WriteString(e.Text); return }
	if e.Kind == Selector { b.WriteString(e.Text); return }
	if e.Kind == Reference { writeReference(b, e); return }
	if e.Kind == String { writeString(b, e); return }
	if e.Kind == List { writeMany(b, '[', ']', e.Items); return }
	if e.Kind == Application { writeMany(b, '(', ')', e.Items); return }
	if e.Kind == Record { b.WriteByte('['); for i := range e.Fields { if i != 0 { b.WriteByte(' ') }; b.WriteString(e.Fields[i].Key); b.WriteString(": "); write(b, e.Fields[i].Value) }; b.WriteByte(']'); return }
	if e.Kind == Lambda { b.WriteString("(["); for i := range e.Parameters { if i != 0 { b.WriteByte(' ') }; b.WriteString(e.Parameters[i].Name); if e.Parameters[i].Rest { b.WriteString("...") } }; b.WriteByte(']'); for i := range e.Body { b.WriteByte(' '); write(b, e.Body[i]) }; b.WriteByte(')') }
}

func writeMany(b *strings.Builder, open byte, close byte, items []*Expr) {
	b.WriteByte(open); for i := range items { if i != 0 { b.WriteByte(' ') }; write(b, items[i]) }; b.WriteByte(close)
}

func writeReference(b *strings.Builder, e *Expr) {
	for i := range e.Reference {
		part := e.Reference[i]
		if i != 0 { b.WriteByte('.') }
		if part.Kind == ReferenceSelection { b.WriteByte('{'); b.WriteString(part.Text); b.WriteByte('}')
		} else { b.WriteString(part.Text) }
	}
}

func writeString(b *strings.Builder, e *Expr) {
	b.WriteByte('"')
	for i := range e.Parts {
		part := e.Parts[i]
		if part.Expr != nil { if part.Brace { b.WriteString("{(") } else { b.WriteString("@(") }; writeEmbedded(b, part.Expr); b.WriteByte(')'); if part.Brace { b.WriteByte('}') }; continue }
		for j := 0; j < len(part.Text); j++ {
			c := part.Text[j]
			if c == '"' { b.WriteString("\\\"")
			} else if c == '\\' { b.WriteString("\\\\")
			} else if c == '\n' { b.WriteString("\\n")
			} else if c == '\r' { b.WriteString("\\r")
			} else if c == '\t' { b.WriteString("\\t")
			} else { b.WriteByte(c) }
		}
	}
	b.WriteByte('"')
}

func writeEmbedded(b *strings.Builder, e *Expr) {
	if e.Kind != Application { write(b, e); return }
	for i := range e.Items { if i != 0 { b.WriteByte(' ') }; write(b, e.Items[i]) }
}
