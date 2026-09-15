// Package rule parses LittleMake rule blocks.
package rule

import (
	"littlemake/lang/source"
	"littlemake/lang/template"
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

type Rule struct {
	Span    source.Span
	Header  source.Span
	Kind    Kind
	Outputs []Target
	Inputs  []Input
	Body    []RecipeLine
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
	for i := range r.Outputs { r.Outputs[i].TargetForm.Free() }
	slices.Free(a, r.Outputs)
	for i := range r.Inputs { r.Inputs[i].Template.Free(); r.Inputs[i].TargetForm.Free() }
	slices.Free(a, r.Inputs)
	for i := range r.Body { r.Body[i].Template.Free() }
	slices.Free(a, r.Body)
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
	p.diags = slices.Append(p.a, p.diags, source.Diagnostic{Code: "LM-PARSE", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message})
}

func (p *parser) takeDiagnostics(diags []source.Diagnostic) {
	for i := range diags { p.diags = slices.Append(p.a, p.diags, diags[i]) }
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
	lineEnd := p.start
	for lineEnd < p.end && p.s.Text[lineEnd] != '\n' {
		lineEnd++
	}
	headerStart, headerEnd := trim(p.s.Text, p.start, lineEnd)
	r := mem.Alloc[Rule](p.a)
	r.Header, r.Span = source.Span{Start: headerStart, End: headerEnd}, source.Span{Start: 0, End: lineEnd}
	colon := topLevel(p.s.Text[headerStart:headerEnd], ':')
	if colon < 0 {
		p.error(headerStart, headerEnd, "expected rule colon")
		return r
	}
	colon += headerStart
	leftStart, leftEnd := trim(p.s.Text, headerStart, colon)
	rightStart, rightEnd := trim(p.s.Text, colon+1, headerEnd)
	p.ruleTargets(r, leftStart, leftEnd)
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
		if !path && !templated && !validName(value) { p.error(words[i].Start, words[i].End, "invalid rule target; use ./ for a file path") }
		kind := TargetName
		if path { kind = TargetPath } else if templated { kind = TargetTemplate }
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
	for _, span := range items {
		text := p.s.Text[span.Start:span.End]
		input := Input{Kind: InputName, Text: text, Span: span}
		if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
			input.Kind = InputString
			input.Template = template.ParseStringRange(p.a, p.s, span.Start+1, span.End-1)
		} else if len(text) >= 3 && text[0] == '@' && text[1] == '(' && text[len(text)-1] == ')' {
			input.Kind = InputExpression
			input.Template = template.ParseStringRange(p.a, p.s, span.Start, span.End)
		} else if hasTemplate(text) {
			input.Kind = InputTemplate
			input.TargetForm = template.ParseTarget(p.a, p.s.Name, text)
			p.takeTargetDiagnostics(input.TargetForm.Diagnostics, span.Start)
		} else if explicitPath(text) {
			input.Kind = InputPath
		} else if !validName(text) { p.error(span.Start, span.End, "invalid rule input") }
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
	if pos < p.end && p.s.Text[pos] == '\r' { pos++ }
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
		if contentEnd > lineStart && p.s.Text[contentEnd-1] == '\r' { contentEnd-- }
		if lineStart == contentEnd {
			pos = end + 1
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
		for i := range line.Diagnostics { p.diags = slices.Append(p.a, p.diags, line.Diagnostics[i]) }
		slices.Free(p.a, line.Diagnostics)
		line.Diagnostics = nil
		r.Body = slices.Append(p.a, r.Body, RecipeLine{Text: p.s.Text[bodyStart:contentEnd], Span: source.Span{Start: bodyStart, End: contentEnd}, Template: line})
		r.Span.End = contentEnd
		pos = end + 1
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
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' { return text[1 : len(text)-1] }
	return text
}

func validName(text string) bool {
	if len(text) == 0 || !nameStart(text[0]) { return false }
	i := 1
	for i < len(text) && nameContinue(text[i]) { i++ }
	if i < len(text) && (text[i] == '?' || text[i] == '!') { i++ }
	return i == len(text)
}

func nameStart(b byte) bool { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func nameContinue(b byte) bool { return nameStart(b) || (b >= '0' && b <= '9') || b == '-' }

func ranges(a mem.Allocator, text string, start int, end int) []source.Span {
	var out []source.Span
	for start < end {
		for start < end && space(text[start]) {
			start++
		}
		if start == end {
			break
		}
		item, depth, quote := start, 0, false
		for start < end {
			b := text[start]
			if quote {
				if b == '\\' && start+1 < end { start += 2; continue }
				if b == '"' { quote = false }
				start++
				continue
			}
			if b == '"' { quote = true
			} else if b == '(' || b == '[' || b == '{' { depth++
			} else if b == ')' || b == ']' || b == '}' { depth--
			} else if depth == 0 && space(b) { break }
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
	b := strings.NewBuilder(a)
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
		b.WriteByte(' ')
		b.WriteString(r.Inputs[i].Text)
	}
	for i := range r.Body {
		b.WriteString("\n\t")
		b.WriteString(r.Body[i].Text)
	}
	value := owned(a, b.String())
	b.Free()
	return value
}
