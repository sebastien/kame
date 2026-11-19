// Package definition parses and formats Kame definitions.
package definition

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"

	"strings"
)

type Parameter struct {
	Name string
	Span source.Span
	Rest bool
}

type Definition struct {
	Span         source.Span
	Name         string
	NameSpan     source.Span
	Function     bool
	ExplicitDef  bool
	Eager        bool
	Default      bool
	Contribution bool
	// Aggregate is evaluator-owned; Expression holds ordered operand expressions.
	Aggregate  bool
	Parameters []Parameter
	Expression *expr.Expr
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
	mem.FreeSlice(nil, p.Diagnostics)
	*p = Part{}
}

func (r *Result) Free() {
	if r == nil {
		return
	}
	a := r.Alloc
	Free(a, r.Definition)
	mem.FreeSlice(nil, r.Diagnostics)
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

// ParseFunctionHeader parses a block-bodied def header. The script parser
// owns the following action block.
func ParseFunctionHeader(a mem.Allocator, s *source.Source, start, end int) Part {
	p := parser{a: a, s: s, start: start, end: end}
	d := mem.Alloc[Definition](a)
	d.ExplicitDef = true
	d.Span = source.Span{Start: start, End: end}
	if !defPrefix(s.Text[:end], start) {
		p.error(start, end, "expected def and a function name")
		return Part{Definition: d, Diagnostics: p.diags}
	}
	if comment := strings.IndexByte(s.Text[start:end], '#'); comment > 0 && space(s.Text[start+comment-1]) {
		end = start + comment
	}
	p.namedLHS(d, start+len("def"), end)
	if d.Name != "" && !d.Function && len(p.diags) == 0 {
		p.error(d.NameSpan.End, end, "expected parameters or () in a function header")
	}
	p.validateNames(d)
	return Part{Definition: d, Diagnostics: p.diags}
}

func Free(a mem.Allocator, d *Definition) {
	if d == nil {
		return
	}
	expr.Free(a, d.Expression)
	mem.FreeSlice(nil, d.Parameters)
	mem.Free(a, d)
}

type parser struct {
	a     mem.Allocator
	s     *source.Source
	start int
	end   int
	diags []source.Diagnostic
	kash  bool
}

// KashHeaderEnd recognizes standalone '=' and contribution headers so unsupported
// Kash contributions receive an authored diagnostic, not a missing-tool error.
func KashHeaderEnd(_ mem.Allocator, s *source.Source, start int) int {
	return headerEnd(s, start, false)
}

// ValueHeaderEnd recognizes compact value headers as ParseRange does; only Kash
// requires a standalone operator. The complete name/function head stays valid.
func ValueHeaderEnd(_ mem.Allocator, s *source.Source, start int) int {
	return headerEnd(s, start, true)
}

func headerEnd(s *source.Source, start int, defaults bool) int {
	text := s.Text
	if start >= len(text) {
		return 0
	}
	if defPrefix(text, start) {
		line := text[start:source.LogicalLineEnd(text, start)]
		if comment := source.TopLevelByte(line, '#'); comment >= 0 {
			line = line[:comment]
		}
		equals := source.TopLevelByte(line, '=')
		if equals < 0 || strings.HasPrefix(line[equals:], "==") {
			return 0
		}
		return start + equals + 1
	}
	name := scanName(text, start)
	if !name.OK {
		return 0
	}
	end := name.End
	pos := end
	for pos < len(text) && space(text[pos]) {
		pos++
	}
	compoundOp := false
	if pos+1 < len(text) && (text[pos] == '+' || (defaults && text[pos] == '?')) && text[pos+1] == '=' {
		compoundOp = true
		pos++
	}
	if (!defaults && pos == end && !compoundOp) || pos == len(text) || text[pos] != '=' {
		return 0
	}
	if !defaults && pos+1 < len(text) && !space(text[pos+1]) && text[pos+1] != '\n' && text[pos+1] != ';' {
		return 0
	}
	return pos + 1
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
	p.diags = append(p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

func (p *parser) definition() *Definition {
	equals := source.TopLevelByte(p.s.Text[p.start:p.end], '=')
	if equals < 0 {
		p.error(p.start, p.end, "expected definition equals")
		return nil
	}
	equals += p.start
	d := p.bindingHeader(equals)
	rhsStart, _ := trim(p.s.Text, equals+1, p.end)
	rhsEnd := p.end
	for rhsStart < rhsEnd {
		next := source.ContinuationEnd(p.s.Text, rhsStart, rhsEnd)
		if next == rhsStart {
			break
		}
		rhsStart, _ = trim(p.s.Text, next, rhsEnd)
	}
	if p.kash {
		var prefix expr.Prefix
		if d.ExplicitDef {
			prefix = expr.ParseKashValueExpressionPrefix(p.a, p.s, rhsStart)
		} else {
			prefix = expr.ParseKashValuePrefix(p.a, p.s, rhsStart)
		}
		d.Expression, d.Span.End = prefix.Expr, prefix.End
		for i := range prefix.Diagnostics {
			p.diags = append(p.diags, prefix.Diagnostics[i])
		}
		mem.FreeSlice(nil, prefix.Diagnostics)
		return d
	}
	if rhsStart == rhsEnd {
		p.error(equals+1, rhsEnd, "expected definition value")
		return d
	}
	// Bound the parser to this definition: malformed values cannot consume the
	// next declaration or another source fragment. The script boundary probe
	// determines rhsEnd; this parse retains the AST and validates the whole RHS.
	bounded := *p.s
	bounded.Text = p.s.Text[:rhsEnd]
	prefix := expr.ParseValuePrefix(p.a, &bounded, rhsStart)
	d.Expression = prefix.Expr
	for i := range prefix.Diagnostics {
		p.diags = append(p.diags, prefix.Diagnostics[i])
	}
	mem.FreeSlice(nil, prefix.Diagnostics)
	end := prefix.End
	for end < rhsEnd {
		next := source.ContinuationEnd(p.s.Text, end, rhsEnd)
		if next != end {
			end = next
			continue
		}
		if p.s.Text[end] == '#' {
			for end < rhsEnd && p.s.Text[end] != '\n' {
				end++
			}
			break
		}
		if !space(p.s.Text[end]) {
			break
		}
		end++
	}
	if len(p.diags) == 0 && end != rhsEnd {
		p.error(end, rhsEnd, "expected one definition expression")
	}
	return d
}

// ParseBindingHeader parses the declaration through its equals token.
func ParseBindingHeader(a mem.Allocator, s *source.Source, start, afterEquals int) Part {
	p := parser{a: a, s: s, start: start, end: afterEquals}
	return Part{Definition: p.bindingHeader(afterEquals - 1), Diagnostics: p.diags}
}

func (p *parser) bindingHeader(equals int) *Definition {
	d := mem.Alloc[Definition](p.a)
	d.Span = source.Span{Start: p.start, End: p.end}
	lhsStart, lhsEnd := trim(p.s.Text, p.start, equals)
	if equals > lhsStart && p.s.Text[equals-1] == '?' {
		d.Default = true
		lhsStart, lhsEnd = trim(p.s.Text, lhsStart, lhsEnd-1)
	}
	if equals > lhsStart && p.s.Text[equals-1] == '+' {
		d.Contribution = true
		lhsStart, lhsEnd = trim(p.s.Text, lhsStart, lhsEnd-1)
		if p.kash {
			p.error(lhsStart, equals+1, "+= contributions require a Kame program")
		}
	}
	if lhsStart == lhsEnd {
		p.error(p.start, equals, "expected definition name")
		return d
	}
	if defPrefix(p.s.Text[:lhsEnd], lhsStart) {
		d.ExplicitDef = true
		if d.Default || d.Contribution {
			p.error(lhsStart, equals+1, "def requires =")
		}
		p.namedLHS(d, lhsStart+3, lhsEnd)

	} else {
		name := scanName(p.s.Text[:lhsEnd], lhsStart)
		if !name.OK || name.End != lhsEnd {
			p.error(lhsStart, lhsEnd, "invalid definition name")
		} else {
			d.Name, d.NameSpan = name.Text, source.Span{Start: lhsStart, End: lhsEnd}
		}
	}
	p.validateNames(d)
	return d
}

func (p *parser) validateNames(d *Definition) {
	if d.Name != "" && !expr.ValidBindingName(d.Name) {
		p.error(d.NameSpan.Start, d.NameSpan.End, "expected a binding name")
	}
	parameters := make(map[string]bool, len(d.Parameters))
	for _, parameter := range d.Parameters {
		if !expr.ValidBindingName(parameter.Name) {
			p.error(parameter.Span.Start, parameter.Span.End, "expected a parameter name")
		}
		if parameters[parameter.Name] {
			p.error(parameter.Span.Start, parameter.Span.End, "duplicate parameter name")
		}
		parameters[parameter.Name] = true
	}
}

func (p *parser) namedLHS(d *Definition, start, end int) {
	text := p.s.Text[:end]
	if strings.ContainsRune(text[start:], '\n') {
		p.error(start, end, "def header must fit on one line")
		return
	}
	nameStart, end := trim(text, start, end)
	name := scanName(text[:end], nameStart)
	if !name.OK {
		p.error(nameStart, end, "expected function name")
		return
	}
	d.Name, d.NameSpan = name.Text, source.Span{Start: nameStart, End: name.End}
	pos := name.End
	if pos < end && space(text[pos]) && strings.TrimSpace(text[pos:end]) == "()" {
		d.Function = true
		return
	}
	for pos < end {
		if !space(p.s.Text[pos]) {
			p.error(pos, pos+1, "function parameters need whitespace")
			return
		}
		for pos < end && space(p.s.Text[pos]) {
			pos++
		}
		if pos == end {
			break
		}
		paramStart := pos
		parameter := scanName(text[:end], pos)
		if !parameter.OK {
			p.error(pos, end, "invalid function parameter")
			return
		}
		pos = parameter.End
		rest := false
		if pos+3 <= end && p.s.Text[pos:pos+3] == "..." {
			pos += 3
			rest = true
		}
		d.Function = true
		d.Parameters = append(d.Parameters, Parameter{Name: parameter.Text, Span: source.Span{Start: paramStart, End: pos}, Rest: rest})
		if rest && pos < end {
			p.error(pos, end, "rest parameter must be last")
			return
		}
	}
}

func defPrefix(text string, start int) bool {
	return len(text)-start > 3 && strings.HasPrefix(text[start:], "def") && (text[start+3] == ' ' || text[start+3] == '\t')
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
	return formatDefinition(a, d, false)
}

// FormatCompact keeps definitions embedded in Kash's statement grammar inline.
func FormatCompact(a mem.Allocator, d *Definition) string {
	return formatDefinition(a, d, true)
}

func formatDefinition(a mem.Allocator, d *Definition, compact bool) string {
	if d.Eager {
		return expr.Compact(a, d.Expression)
	}
	if d.Expression != nil && d.Expression.Kind == expr.KashFunction {
		return expr.FormatKashControl(a, d.Expression, "\t")
	}
	b := strings.Builder{}
	if d.ExplicitDef || d.Function {
		b.WriteString("def ")
		b.WriteString(d.Name)
		if d.Function && len(d.Parameters) == 0 {
			b.WriteString(" ()")
		}
		for _, p := range d.Parameters {
			b.WriteByte(' ')
			b.WriteString(p.Name)
			if p.Rest {
				b.WriteString("...")
			}
		}

	} else {
		b.WriteString(d.Name)
	}
	if d.Default {
		b.WriteString(" ?=")
	} else if d.Contribution {
		b.WriteString(" +=")
	} else {
		b.WriteString(" =")
	}
	v := strings.Builder{}
	value := expr.Compact(a, d.Expression)
	v.WriteString(value)
	mem.FreeString(a, value)
	column := expr.Columns(b.String(), 0) + 1
	if d.Expression != nil && d.Expression.Kind == expr.Match {
		b.WriteByte(' ')
		formatted := expr.FormatAtIndent(a, d.Expression, column, 0)
		b.WriteString(formatted)
		mem.FreeString(a, formatted)
	} else if compact || expr.Fits(v.String(), column) {
		b.WriteByte(' ')
		b.WriteString(v.String())
	} else {
		indent := "  "
		if d.ExplicitDef {
			indent = "\t"
		}
		b.WriteByte('\n')
		b.WriteString(indent)
		formatted := expr.FormatAt(a, d.Expression, expr.Columns(indent, 0))
		b.WriteString(formatted)
		mem.FreeString(a, formatted)
	}
	v.Reset()
	value = owned(a, b.String())
	b.Reset()
	return value
}
