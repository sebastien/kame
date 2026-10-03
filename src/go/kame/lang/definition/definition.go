// Package definition parses and formats Kame definitions.
package definition

import (
	"kame/lang/expr"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type ValueKind int

const (
	ValueWords ValueKind = iota
	ValueExpression
	ValueTemplate
)

type Word struct {
	Text     string
	Span     source.Span
	Template *template.String
}
type Parameter struct {
	Name string
	Span source.Span
	Rest bool
}

type Definition struct {
	Span       source.Span
	Name       string
	NameSpan   source.Span
	Function   bool
	Default    bool
	Parameters []Parameter
	ValueKind  ValueKind
	Expression *expr.Expr
	Template   *template.String
	Words      []Word
}

type Result struct {
	Alloc       mem.Allocator
	Source      *source.Source
	Definition  *Definition
	Diagnostics []source.Diagnostic
}

// Part is parsed from a range in a Source owned by the caller.
type Part struct {
	Definition  *Definition
	Diagnostics []source.Diagnostic
}

func (p *Part) Free(a mem.Allocator) {
	if p == nil {
		return
	}
	Free(a, p.Definition)
	slices.Free(a, p.Diagnostics)
	*p = Part{}
}

func (r *Result) Free() {
	if r == nil {
		return
	}
	a := r.Alloc
	Free(a, r.Definition)
	slices.Free(a, r.Diagnostics)
	r.Source.Free(a)
	*r = Result{}
	mem.Free(a, r)
}

func Parse(a mem.Allocator, name string, text string) *Result {
	s := source.New(a, name, text)
	part := ParseRange(a, s, 0, len(text))
	r := mem.Alloc[Result](a)
	r.Alloc, r.Source, r.Definition, r.Diagnostics = a, s, part.Definition, part.Diagnostics
	return r
}

// ParseRange retains spans in the caller-provided Source.
func ParseRange(a mem.Allocator, s *source.Source, start int, end int) Part {
	p := parser{a: a, s: s, start: start, end: end}
	return Part{Definition: p.definition(), Diagnostics: p.diags}
}

func Free(a mem.Allocator, d *Definition) {
	if d == nil {
		return
	}
	expr.Free(a, d.Expression)
	d.Template.Free()
	slices.Free(a, d.Parameters)
	for i := range d.Words {
		d.Words[i].Template.Free()
	}
	slices.Free(a, d.Words)
	mem.Free(a, d)
}

type parser struct {
	a     mem.Allocator
	s     *source.Source
	start int
	end   int
	diags []source.Diagnostic
	kash bool
}

// KashHeaderEnd recognizes only a valid header followed by standalone '='.
// Command words containing '=' never become definitions by accident.
func KashHeaderEnd(a mem.Allocator, s *source.Source, start int) int {
	return headerEnd(a, s, start, false)
}

// ValueHeaderEnd additionally recognizes Kame's contiguous default operator.
func ValueHeaderEnd(a mem.Allocator, s *source.Source, start int) int {
	return headerEnd(a, s, start, true)
}

func headerEnd(a mem.Allocator, s *source.Source, start int, defaults bool) int {
	text := s.Text
	if start >= len(text) { return 0 }
	end := start
	if text[start] == '(' {
		for end < len(text) && text[end] != ')' && text[end] != '\n' { end++ }
		if end == len(text) || text[end] != ')' { return 0 }
		end++
		p := parser{a: a, s: s}
		d := Definition{}
		p.functionLHS(&d, start, end)
		valid := len(p.diags) == 0
		slices.Free(a, d.Parameters); slices.Free(a, p.diags)
		if !valid { return 0 }
	} else {
		name := scanName(text, start)
		if !name.OK { return 0 }
		end = name.End
	}
	pos := end
	for pos < len(text) && space(text[pos]) { pos++ }
	defaultOp := false
	if defaults && pos+1 < len(text) && text[pos] == '?' && text[pos+1] == '=' { defaultOp = true; pos++ }
	if defaults && pos == end && pos < len(text) && text[pos] == '=' && end > start && text[end-1] == '?' { defaultOp = true }
	if (pos == end && !defaultOp) || pos == len(text) || text[pos] != '=' { return 0 }
	if pos+1 < len(text) && !space(text[pos+1]) && text[pos+1] != '\n' && text[pos+1] != ';' { return 0 }
	return pos+1
}

// ParseKashPrefix uses Kame's header and expression grammar with Kash value
// wrappers. Its span ends after the single RHS, not at the end of the source.
func ParseKashPrefix(a mem.Allocator, s *source.Source, start int) Part {
	p := parser{a: a, s: s, start: start, end: len(s.Text), kash: true}
	return Part{Definition: p.definition(), Diagnostics: p.diags}
}

func (p *parser) error(start int, end int, message string) {
	if end > len(p.s.Text) {
		end = len(p.s.Text)
	}
	p.diags = slices.Append(p.a, p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

func (p *parser) definition() *Definition {
	equals := topLevel(p.s.Text[p.start:p.end], '=')
	if equals < 0 {
		p.error(p.start, p.end, "expected definition equals")
		return nil
	}
	equals += p.start
	d := mem.Alloc[Definition](p.a)
	d.Span = source.Span{Start: p.start, End: p.end}
	lhsStart, lhsEnd := trim(p.s.Text, p.start, equals)
	if equals > lhsStart && p.s.Text[equals-1] == '?' {
		d.Default = true
		lhsStart, lhsEnd = trim(p.s.Text, lhsStart, lhsEnd-1)
	}
	if lhsStart == lhsEnd {
		p.error(p.start, equals, "expected definition name")
		return d
	}
	if p.s.Text[lhsStart] == '(' {
		if d.Default { p.error(lhsStart, lhsEnd, "default definitions must name a value") }
		d.Function = true
		p.functionLHS(d, lhsStart, lhsEnd)
	} else {
		name := scanName(p.s.Text, lhsStart)
		if !name.OK || name.End != lhsEnd {
			p.error(lhsStart, lhsEnd, "invalid definition name")
		} else {
			d.Name, d.NameSpan = name.Text, source.Span{Start: lhsStart, End: lhsEnd}
		}
	}
	rhsStart, rhsEnd := trim(p.s.Text, equals+1, p.end)
	for rhsStart < rhsEnd {
		next := source.ContinuationEnd(p.s.Text, rhsStart, rhsEnd)
		if next == rhsStart { break }
		rhsStart, rhsEnd = trim(p.s.Text, next, rhsEnd)
	}
	if p.kash {
		prefix := expr.ParseKashValuePrefix(p.a, p.s, rhsStart)
		d.Expression, d.ValueKind, d.Span.End = prefix.Expr, ValueExpression, prefix.End
		for i := range prefix.Diagnostics { p.diags = slices.Append(p.a, p.diags, prefix.Diagnostics[i]) }
		slices.Free(p.a, prefix.Diagnostics)
		return d
	}
	if rhsStart == rhsEnd {
		p.error(equals+1, rhsEnd, "expected definition value")
		return d
	}
	if p.s.Text[rhsStart] == '(' || p.s.Text[rhsStart] == '[' || (rhsStart+1 < rhsEnd && p.s.Text[rhsStart:rhsStart+2] == "$(") {
		prefix := expr.ParsePrefix(p.a, p.s, rhsStart)
		d.Expression, d.ValueKind = prefix.Expr, ValueExpression
		for i := range prefix.Diagnostics {
			p.diags = slices.Append(p.a, p.diags, prefix.Diagnostics[i])
		}
		slices.Free(p.a, prefix.Diagnostics)
		if prefix.End != rhsEnd {
			p.error(prefix.End, rhsEnd, "unexpected definition value")
		}
		return d
	}
	if p.s.Text[rhsStart] == '"' {
		// Verbatim multi-line literal (3+ quotes) is raw: parse as expression
		// so @(...) inside stays literal until render. Single-quote strings
		// keep template semantics.
		n := 0
		for rhsStart+n < rhsEnd && p.s.Text[rhsStart+n] == '"' {
			n++
		}
		if n >= 3 {
			prefix := expr.ParsePrefix(p.a, p.s, rhsStart)
			d.Expression, d.ValueKind = prefix.Expr, ValueExpression
			for i := range prefix.Diagnostics {
				p.diags = slices.Append(p.a, p.diags, prefix.Diagnostics[i])
			}
			slices.Free(p.a, prefix.Diagnostics)
			if prefix.Expr == nil || prefix.End != rhsEnd {
				p.error(rhsStart, rhsEnd, "malformed verbatim literal")
			}
			return d
		}
		if rhsEnd-rhsStart < 2 || p.s.Text[rhsEnd-1] != '"' {
			p.error(rhsStart, rhsEnd, "unclosed template string")
			return d
		}
		d.Template, d.ValueKind = template.ParseStringRange(p.a, p.s, rhsStart+1, rhsEnd-1), ValueTemplate
		for i := range d.Template.Diagnostics {
			p.diags = slices.Append(p.a, p.diags, d.Template.Diagnostics[i])
		}
		slices.Free(p.a, d.Template.Diagnostics)
		d.Template.Diagnostics = nil
		return d
	}
	d.ValueKind = ValueWords
	for pos := rhsStart; pos < rhsEnd; {
		start := pos
		for pos < rhsEnd && !space(p.s.Text[pos]) && source.ContinuationEnd(p.s.Text, pos, rhsEnd) == pos {
			pos++
		}
		word := template.ParseStringRange(p.a, p.s, start, pos)
		for i := range word.Diagnostics {
			p.diags = slices.Append(p.a, p.diags, word.Diagnostics[i])
		}
		slices.Free(p.a, word.Diagnostics)
		word.Diagnostics = nil
		d.Words = slices.Append(p.a, d.Words, Word{Text: p.s.Text[start:pos], Span: source.Span{Start: start, End: pos}, Template: word})
		for pos < rhsEnd {
			next := source.ContinuationEnd(p.s.Text, pos, rhsEnd)
			if next != pos { pos = next; continue }
			if !space(p.s.Text[pos]) { break }
			pos++
		}
	}
	return d
}

func (p *parser) functionLHS(d *Definition, start int, end int) {
	if end-start < 3 || p.s.Text[end-1] != ')' {
		p.error(start, end, "unclosed function definition")
		return
	}
	pos := start + 1
	for pos < end-1 && space(p.s.Text[pos]) {
		pos++
	}
	nameStart := pos
	name := scanName(p.s.Text, pos)
	if !name.OK {
		p.error(nameStart, end, "expected function name")
		return
	}
	d.Name, d.NameSpan, pos = name.Text, source.Span{Start: nameStart, End: name.End}, name.End
	for pos < end-1 {
		if !space(p.s.Text[pos]) {
			p.error(pos, pos+1, "function parameters need whitespace")
			return
		}
		for pos < end-1 && space(p.s.Text[pos]) {
			pos++
		}
		if pos == end-1 {
			break
		}
		paramStart := pos
		parameter := scanName(p.s.Text, pos)
		if !parameter.OK {
			p.error(pos, end, "invalid function parameter")
			return
		}
		pos = parameter.End
		rest := false
		if pos+3 <= end-1 && p.s.Text[pos:pos+3] == "..." {
			pos += 3
			rest = true
		}
		d.Parameters = slices.Append(p.a, d.Parameters, Parameter{Name: parameter.Text, Span: source.Span{Start: paramStart, End: pos}, Rest: rest})
		if rest && pos < end-1 {
			p.error(pos, end-1, "rest parameter must be last")
			return
		}
	}
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

func space(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }
func trim(text string, start int, end int) (int, int) {
	for start < end && space(text[start]) {
		start++
	}
	for start < end && space(text[end-1]) {
		end--
	}
	return start, end
}

type nameResult struct {
	Text string
	End  int
	OK   bool
}

// ValidName applies the definition grammar to one complete name.
func ValidName(text string) bool {
	name := scanName(text, 0)
	return name.OK && name.End == len(text)
}

func scanName(text string, start int) nameResult {
	if start == len(text) || !nameStart(text[start]) {
		return nameResult{End: start}
	}
	pos := start + 1
	for pos < len(text) && nameContinue(text[pos]) {
		pos++
	}
	if pos < len(text) && (text[pos] == '?' || text[pos] == '!') {
		pos++
	}
	return nameResult{Text: text[start:pos], End: pos, OK: true}
}
func nameStart(b byte) bool    { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func nameContinue(b byte) bool { return nameStart(b) || (b >= '0' && b <= '9') || b == '-' }

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

// Format returns allocator-owned canonical definition text.
func Format(a mem.Allocator, d *Definition) string {
	b := strings.NewBuilder(a)
	if d.Function {
		b.WriteByte('(')
		b.WriteString(d.Name)
		for i := range d.Parameters {
			b.WriteByte(' ')
			b.WriteString(d.Parameters[i].Name)
			if d.Parameters[i].Rest {
				b.WriteString("...")
			}
		}
		b.WriteByte(')')
	} else {
		b.WriteString(d.Name)
	}
	if d.Default { b.WriteString(" ?= ") } else { b.WriteString(" = ") }
	if d.ValueKind == ValueExpression {
		value := expr.Format(a, d.Expression)
		b.WriteString(value)
		mem.FreeString(a, value)
	} else if d.ValueKind == ValueTemplate {
		b.WriteByte('"')
		value := template.FormatString(a, d.Template)
		b.WriteString(value)
		mem.FreeString(a, value)
		b.WriteByte('"')
	} else {
		for i := range d.Words {
			if i != 0 {
				b.WriteByte(' ')
			}
			value := template.FormatString(a, d.Words[i].Template)
			b.WriteString(value)
			mem.FreeString(a, value)
		}
	}
	value := owned(a, b.String())
	b.Free()
	return value
}
