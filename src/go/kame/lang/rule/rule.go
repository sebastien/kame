// Package rule parses Kame rule blocks.
package rule

import (
	"kame/lang/source"
 "kame/lang/expr"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type TargetKind int

const (
	TargetName TargetKind = iota
	TargetPath
	TargetTemplate
)

type InputKind int

const (
	InputName InputKind = iota
	InputPath
	InputTemplate
	InputString
	InputExpression
 InputWildcard
)

type Target struct {
	Kind       TargetKind
	Text       string
	Span       source.Span
	Path       bool
	Template   bool
	TargetForm *template.Target
}
type Input struct {
 OrderOnly bool
	Kind       InputKind
	Text       string
	Span       source.Span
	Template   *template.String
	TargetForm *template.Target
}
type RecipeLine struct {
	Text     string
	Span     source.Span
	Template *template.String
}

type Kind int

const (
	FileRule Kind = iota
	TaskRule
	CachedTaskRule
	ServiceRule
)

// EnvironmentAssignment retains authored spelling and a decoded literal value.
type EnvironmentAssignment struct {
 Text string
 Value string
 Span source.Span
}

type Rule struct {
 Environment []EnvironmentAssignment
	// Always bypasses freshness while preserving file-output semantics.
	Always  bool
	Span    source.Span
	Header  source.Span
	Kind    Kind
	Outputs []Target
	Inputs  []Input
	Body    []RecipeLine
	BodyDoc *template.Document
}

type Result struct {
	Alloc       mem.Allocator
	Source      *source.Source
	Rule        *Rule
	Diagnostics []source.Diagnostic
}

// Part is parsed from a range in a Source owned by the caller.
type Part struct {
	Rule        *Rule
	Diagnostics []source.Diagnostic
}

func (p *Part) Free(a mem.Allocator) {
	if p == nil {
		return
	}
	FreeRule(a, p.Rule)
	slices.Free(a, p.Diagnostics)
	*p = Part{}
}

func (r *Result) Free() {
	if r == nil {
		return
	}
	a := r.Alloc
	FreeRule(a, r.Rule)
	slices.Free(a, r.Diagnostics)
	r.Source.Free(a)
	*r = Result{}
	mem.Free(a, r)
}

type parser struct {
	a     mem.Allocator
	s     *source.Source
	start int
	end   int
	diags []source.Diagnostic
}

func ParseRule(a mem.Allocator, name string, text string) *Result {
	p := parser{a: a, s: source.New(a, name, text), end: len(text)}
	r := p.rule()
	return p.result(r)
}

func ParseRuleRange(a mem.Allocator, s *source.Source, start int, end int) Part {
	p := parser{a: a, s: s, start: start, end: end}
	return Part{Rule: p.rule(), Diagnostics: p.diags}
}

func FreeRule(a mem.Allocator, r *Rule) {
	if r == nil {
		return
	}
	for i := range r.Outputs {
		r.Outputs[i].TargetForm.Free()
	}
	slices.Free(a, r.Outputs)
	for i := range r.Inputs {
		r.Inputs[i].Template.Free()
		r.Inputs[i].TargetForm.Free()
	}
	slices.Free(a, r.Inputs)
 for i := range r.Environment { mem.FreeString(a, r.Environment[i].Value) }
 slices.Free(a, r.Environment)
	for i := range r.Body {
		r.Body[i].Template.Free()
	}
	slices.Free(a, r.Body)
	if r.BodyDoc != nil {
		r.BodyDoc.Free()
		r.BodyDoc = nil
	}
	mem.Free(a, r)
}

func (p *parser) result(r *Rule) *Result {
	result := mem.Alloc[Result](p.a)
	result.Alloc, result.Source, result.Rule, result.Diagnostics = p.a, p.s, r, p.diags
	return result
}

func (p *parser) error(start int, end int, message string) {
	if end > len(p.s.Text) {
		end = len(p.s.Text)
	}
	p.diags = slices.Append(p.a, p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

func (p *parser) takeDiagnostics(diags []source.Diagnostic) {
	for i := range diags {
		p.diags = slices.Append(p.a, p.diags, diags[i])
	}
}

func (p *parser) takeTargetDiagnostics(diags []source.Diagnostic, offset int) {
	for i := range diags {
		diagnostic := diags[i]
		diagnostic.Span.Start += offset
		diagnostic.Span.End += offset
		p.diags = slices.Append(p.a, p.diags, diagnostic)
	}
}

func (p *parser) rule() *Rule {
	lineEnd := source.LogicalLineEnd(p.s.Text[:p.end], p.start)
	headerStart, headerEnd := trim(p.s.Text, p.start, lineEnd)
	r := mem.Alloc[Rule](p.a)
	r.Header, r.Span = source.Span{Start: headerStart, End: headerEnd}, source.Span{Start: p.start, End: lineEnd}
	colon := topLevel(p.s.Text[headerStart:headerEnd], ':')
	if colon < 0 {
		p.error(headerStart, headerEnd, "expected rule colon")
		return r
	}
	colon += headerStart
	leftStart, leftEnd := trim(p.s.Text, headerStart, colon)
	rightStart, rightEnd := trim(p.s.Text, colon+1, headerEnd)
	p.ruleTargets(r, leftStart, leftEnd)
 metadata := topLevel(p.s.Text[rightStart:rightEnd], ';')
 if metadata >= 0 {
  metadata += rightStart
  p.ruleEnvironment(r, metadata+1, rightEnd)
  rightEnd = metadata
 }
	p.ruleInputs(r, rightStart, rightEnd)
	p.classify(r)
	p.recipe(r, lineEnd)
	return r
}

func (p *parser) ruleTargets(r *Rule, start int, end int) {
	items := ranges(p.a, p.s.Text, start, end)
	defer slices.Free(p.a, items)
	words := items
	if len(words) == 0 {
		p.error(start, end, "expected rule output")
		return
	}
	if len(words) >= 2 && p.s.Text[words[0].Start:words[0].End] == "always" {
		r.Always = true
		words = words[1:]
	}
	if len(words) >= 2 && (p.s.Text[words[0].Start:words[0].End] == "task" || p.s.Text[words[0].Start:words[0].End] == "service") {
		if p.s.Text[words[0].Start:words[0].End] == "task" {
			r.Kind = CachedTaskRule
		} else {
			r.Kind = ServiceRule
		}
		if len(words) != 2 {
			p.error(start, end, "prefixed rule needs one target")
			return
		}
		words = words[1:]
	}
	for i := range words {
		text := p.s.Text[words[i].Start:words[i].End]
		value := targetValue(text)
		path, templated := explicitPath(value), hasTemplate(value)
		if !path && !templated && !validName(value) {
			p.error(words[i].Start, words[i].End, "invalid rule target; use ./ for a file path")
		}
		kind := TargetName
		if path {
			kind = TargetPath
		} else if templated {
			kind = TargetTemplate
		}
		output := Target{Kind: kind, Text: text, Span: words[i], Path: path, Template: templated}
		if templated {
			output.TargetForm = template.ParseTarget(p.a, p.s.Name, value)
			p.takeTargetDiagnostics(output.TargetForm.Diagnostics, words[i].Start)
		}
		r.Outputs = slices.Append(p.a, r.Outputs, output)
	}
}

func (p *parser) ruleInputs(r *Rule, start int, end int) {
	items := ranges(p.a, p.s.Text, start, end)
 ordered := false
 for i, span := range items {
  text := p.s.Text[span.Start:span.End]
  if text == "|" {
   if ordered || i == len(items)-1 { p.error(span.Start, span.End, "expected one order-only prerequisite section") }
   ordered = true
   continue
  }
  input := Input{OrderOnly: ordered, Kind: InputName, Text: text, Span: span}
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			input.Kind = InputString
			input.Template = template.ParseStringRange(p.a, p.s, span.Start+1, span.End-1)
		} else if len(text) >= 3 && text[0] == '@' && text[1] == '(' && text[len(text)-1] == ')' {
			input.Kind = InputExpression
			input.Template = template.ParseStringRange(p.a, p.s, span.Start, span.End)
		} else if strings.Contains(text, "@(") {
			input.Kind = InputString
			input.Template = template.ParseStringRange(p.a, p.s, span.Start, span.End)
		} else if hasTemplate(text) {
			input.Kind = InputTemplate
			input.TargetForm = template.ParseTarget(p.a, p.s.Name, text)
			p.takeTargetDiagnostics(input.TargetForm.Diagnostics, span.Start)
		} else if explicitPath(text) {
			input.Kind = InputPath
   if strings.ContainsAny(text, "*?[") {
    input.Kind = InputWildcard
    input.Template = wildcardInput(p.a, text, span)
   }
		} else if !validName(text) {
			p.error(span.Start, span.End, "invalid rule input")
		}
		if input.Template != nil {
			p.takeDiagnostics(input.Template.Diagnostics)
			slices.Free(p.a, input.Template.Diagnostics)
			input.Template.Diagnostics = nil
		}
		r.Inputs = slices.Append(p.a, r.Inputs, input)
	}
	slices.Free(p.a, items)
}

func (p *parser) classify(r *Rule) {
	if len(r.Outputs) == 0 {
		return
	}
	paths := r.Outputs[0].Path
	for i := range r.Outputs {
		if r.Outputs[i].Path != paths {
			p.error(r.Outputs[i].Span.Start, r.Outputs[i].Span.End, "cannot mix path and name outputs")
		}
	}
	if r.Always && !paths {
		p.error(r.Header.Start, r.Header.End, "always rules require file outputs")
	}
	if r.Kind == CachedTaskRule || r.Kind == ServiceRule {
		if r.Outputs[0].Path {
			p.error(r.Outputs[0].Span.Start, r.Outputs[0].Span.End, "prefixed rule target must be a name")
		}
		return
	}
	if paths {
		r.Kind = FileRule
	} else if len(r.Outputs) == 1 {
		r.Kind = TaskRule
	} else {
		p.error(r.Header.Start, r.Header.End, "named rules have one output")
	}
}

func (p *parser) recipe(r *Rule, lineEnd int) {
	pos := lineEnd
	if pos < p.end && p.s.Text[pos] == '\r' {
		pos++
	}
	if pos < p.end && p.s.Text[pos] == '\n' {
		pos++
	}
	indent := ""
	for pos < p.end {
		lineStart, end := pos, pos
		for end < p.end && p.s.Text[end] != '\n' {
			end++
		}
		contentEnd := end
		if contentEnd > lineStart && p.s.Text[contentEnd-1] == '\r' {
			contentEnd--
		}
		blankEnd := lineStart
		for blankEnd < contentEnd && (p.s.Text[blankEnd] == ' ' || p.s.Text[blankEnd] == '\t') {
			blankEnd++
		}
		if blankEnd == contentEnd {
			next := end + 1
			look := next
			for look < p.end && p.s.Text[look] == '\n' {
				look++
			}
			if len(r.Body) != 0 && look < p.end && (p.s.Text[look] == ' ' || p.s.Text[look] == '\t') {
				r.Body = slices.Append(p.a, r.Body, RecipeLine{Span: source.Span{Start: lineStart, End: contentEnd}})
				r.Span.End = end
			}
			pos = next
			continue
		}
		if p.s.Text[lineStart] != ' ' && p.s.Text[lineStart] != '\t' {
			break
		}
		prefixEnd := lineStart
		for prefixEnd < contentEnd && (p.s.Text[prefixEnd] == ' ' || p.s.Text[prefixEnd] == '\t') {
			prefixEnd++
		}
		prefix := p.s.Text[lineStart:prefixEnd]
		if indent == "" {
			indent = prefix
		} else if len(prefix) < len(indent) || prefix[:len(indent)] != indent {
			p.error(lineStart, prefixEnd, "inconsistent recipe indentation")
			break
		}
		bodyStart := lineStart + len(indent)
		line := template.ParseStringRange(p.a, p.s, bodyStart, contentEnd)
		for i := range line.Diagnostics {
			p.diags = slices.Append(p.a, p.diags, line.Diagnostics[i])
		}
		slices.Free(p.a, line.Diagnostics)
		line.Diagnostics = nil
		r.Body = slices.Append(p.a, r.Body, RecipeLine{Text: p.s.Text[bodyStart:contentEnd], Span: source.Span{Start: bodyStart, End: contentEnd}, Template: line})
		r.Span.End = contentEnd
		pos = end + 1
	}
	p.recipeDocument(r, indent)
}

func (p *parser) recipeDocument(r *Rule, indent string) {
	if len(r.Body) == 0 {
		return
	}
	// Detect plain-style directive lines in stripped body text. If none,
	// keep per-line templates for exact backward-compatible rendering.
	found := false
	for i := range r.Body {
		// Blank lines (nil Template) cannot be directives.
		if r.Body[i].Template == nil {
			continue
		}
		text := r.Body[i].Text
		// Trim trailing spaces for whole-line check.
		end := len(text)
		for end > 0 && (text[end-1] == ' ' || text[end-1] == '\t') {
			end--
		}
		if end == 0 || text[0] != '@' {
			continue
		}
		if end >= 2 && text[0] == '@' && text[1] == '@' {
			found = true
			break
		}
		// Keyword letters.
		kend := 1
		for kend < end && isDocKeywordLetter(text[kend]) {
			kend++
		}
		if kend == 1 {
			continue
		}
		if !isDocKeyword(text[1:kend]) {
			continue
		}
		word := text[1:kend]
		if word == "else" || word == "end" || word == "raw" {
			found = true
			break
		}
		// Args keywords need "(" adjacent; "@if (" is ordinary text.
		if kend < end && text[kend] == '(' {
			found = true
			break
		}
	}
	if !found {
		return
	}
	// Parse whole body range as plain document, preserving original spans.
	// First body line is non-blank; reconstruct its lineStart.
	firstStart := r.Body[0].Span.Start - len(indent)
	if firstStart < 0 {
		firstStart = r.Body[0].Span.Start
	}
	lastEnd := r.Body[len(r.Body)-1].Span.End
	doc := template.ParseDocumentRange(p.a, p.s, firstStart, lastEnd, "plain")
	for i := range doc.Diagnostics {
		p.diags = slices.Append(p.a, p.diags, doc.Diagnostics[i])
	}
	r.BodyDoc = doc
}

func isDocKeywordLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func isDocKeyword(word string) bool {
	switch word {
	case "if", "elif", "else", "for", "with", "let", "include", "raw", "end":
		return true
	}
	return false
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
func explicitPath(text string) bool {
	return len(text) != 0 && (text[0] == '/' || (len(text) >= 2 && text[0] == '.' && text[1] == '/') || (len(text) >= 3 && text[0] == '.' && text[1] == '.' && text[2] == '/'))
}
func hasTemplate(text string) bool {
	for i := range text {
		if text[i] == '{' {
			return true
		}
	}
	return false
}

func targetValue(text string) string {
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		return text[1 : len(text)-1]
	}
	return text
}

func validName(text string) bool {
	if len(text) == 0 || !nameStart(text[0]) {
		return false
	}
	i := 1
	for i < len(text) && nameContinue(text[i]) {
		i++
	}
	if i < len(text) && (text[i] == '?' || text[i] == '!') {
		i++
	}
	return i == len(text)
}

func nameStart(b byte) bool    { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func nameContinue(b byte) bool { return nameStart(b) || (b >= '0' && b <= '9') || b == '-' }

func ranges(a mem.Allocator, text string, start int, end int) []source.Span {
	var out []source.Span
	for start < end {
		for start < end {
			next := source.ContinuationEnd(text, start, end)
			if next != start { start = next; continue }
			if !space(text[start]) { break }
			start++
		}
		if start == end {
			break
		}
		item, depth, quote := start, 0, false
		for start < end {
			b := text[start]
			if !quote && depth == 0 && source.ContinuationEnd(text, start, end) != start { break }
			if quote {
				if b == '\\' && start+1 < end {
					start += 2
					continue
				}
				if b == '"' {
					quote = false
				}
				start++
				continue
			}
			if b == '"' {
				quote = true
			} else if b == '(' || b == '[' || b == '{' {
				depth++
			} else if b == ')' || b == ']' || b == '}' {
				depth--
			} else if depth == 0 && space(b) {
				break
			}
			start++
		}
		out = slices.Append(a, out, source.Span{Start: item, End: start})
	}
	return out
}

func owned(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

// FormatRule returns allocator-owned canonical rule text without a terminal newline.
func FormatRule(a mem.Allocator, r *Rule) string {
	return FormatRuleWithIndent(a, r, "\t")
}

// FormatRuleWithIndent returns allocator-owned canonical rule text without a terminal newline.
func FormatRuleWithIndent(a mem.Allocator, r *Rule, indent string) string {
	b := strings.NewBuilder(a)
	if r.Always {
		b.WriteString("always ")
	}
	if r.Kind == CachedTaskRule {
		b.WriteString("task ")
	}
	if r.Kind == ServiceRule {
		b.WriteString("service ")
	}
	for i := range r.Outputs {
		if i != 0 {
			b.WriteByte(' ')
		}
		b.WriteString(r.Outputs[i].Text)
	}
	b.WriteString(" :")
 for i := range r.Inputs {
  if r.Inputs[i].OrderOnly && (i == 0 || !r.Inputs[i-1].OrderOnly) { b.WriteString(" |") }
		b.WriteByte(' ')
		b.WriteString(r.Inputs[i].Text)
	}
 for i := range r.Environment {
  if i == 0 { b.WriteString(" ; env") }
  b.WriteByte(' ')
  b.WriteString(r.Environment[i].Text)
 }
	for i := range r.Body {
		b.WriteByte('\n')
		if r.Body[i].Text != "" {
			b.WriteString(indent)
			b.WriteString(r.Body[i].Text)
		}
	}
	value := owned(a, b.String())
	b.Free()
	return value
}

// The authored token and spans remain intact; the evaluator shares wildcard's
// existing capability checks and dependency tracking.
func wildcardInput(a mem.Allocator, text string, span source.Span) *template.String {
 call := mem.Alloc[expr.Expr](a)
 call.Kind, call.Span = expr.Application, span
 name := mem.Alloc[expr.Expr](a)
 name.Kind, name.Text, name.Span = expr.Name, "wildcard", span
 pattern := mem.Alloc[expr.Expr](a)
 pattern.Kind, pattern.Text, pattern.Span = expr.Symbol, text, span
 call.Items = slices.Append(a, call.Items, name, pattern)
 value := mem.Alloc[template.String](a)
 value.Alloc, value.Span = a, span
 value.Parts = slices.Append(a, value.Parts, template.Part{Kind: template.Expression, Span: span, Expr: call})
 return value
}
