// Package template parses Kame string and target templates.
package template

import (
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type TargetPartKind int

const (
	TargetLiteral TargetPartKind = iota
	Capture
)

type TargetPart struct {
	Kind    TargetPartKind
	Text    string
	Name    string
	Pattern string
	Regex   *expr.Pattern
	Span    source.Span
}

type Target struct {
	Alloc       mem.Allocator
	Source      *source.Source
	Parts       []TargetPart
	Diagnostics []source.Diagnostic
}

func (t *Target) Free() {
	if t == nil {
		return
	}
	for i := range t.Parts {
		if t.Parts[i].Kind == TargetLiteral {
			mem.FreeString(t.Alloc, t.Parts[i].Text)
		}
		if t.Parts[i].Regex != nil { t.Parts[i].Regex.Free(t.Alloc) }
	}
	slices.Free(t.Alloc, t.Parts)
	slices.Free(t.Alloc, t.Diagnostics)
	t.Source.Free(t.Alloc)
	a := t.Alloc
	*t = Target{}
	mem.Free(a, t)
}

func ParseTarget(a mem.Allocator, name string, text string) *Target {
	// Captures borrow the caller's text, not the Source clone: the caller
	// text must outlive the Target. Free releases the Source clone plus
	// owned literal texts only.
	s := source.New(a, name, text)
	t := mem.Alloc[Target](a)
	t.Alloc, t.Source = a, s
	start, pos := 0, 0
	for pos < len(text) {
		if text[pos] == '\\' && pos+1 < len(text) {
			pos += 2
			continue
		}
		if text[pos] != '{' {
			pos++
			continue
		}
		if start < pos {
			t.Parts = slices.Append(a, t.Parts, TargetPart{Kind: TargetLiteral, Text: targetLiteral(a, text[start:pos]), Span: source.Span{Start: start, End: pos}})
		}
		groupStart := pos
		pos++
		if pos < len(text) && text[pos] == '~' {
			regexStart := pos + 1
			pos++
			for pos < len(text) {
				if text[pos] == '\\' {
					if pos+1 >= len(text) { pos = len(text); break }
					pos += 2
					continue
				}
				if text[pos] == '}' { break }
				pos++
			}
			if pos == len(text) {
				t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(groupStart, pos, "unclosed regular-expression capture group"))
				start = groupStart
				break
			}
			appendRegexTargetPart(a, t, text, groupStart, regexStart, pos, "")
			pos++
			start = pos
			continue
		}
		if pos < len(text) && text[pos] == '*' {
			pattern := "*"
			pos++
			if pos < len(text) && text[pos] == '*' {
				pattern = "**"
				pos++
			}
			if pos == len(text) || text[pos] != '}' {
				t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(groupStart, pos, "invalid anonymous capture group"))
				start = groupStart
				break
			}
			pos++
			t.Parts = slices.Append(a, t.Parts, TargetPart{Kind: Capture, Pattern: pattern, Span: source.Span{Start: groupStart, End: pos}})
			start = pos
			continue
		}
		nameStart := pos
		for pos < len(text) && isCaptureContinue(text[pos]) {
			pos++
		}
		capture := text[nameStart:pos]
		if !validCapture(capture) {
			t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(groupStart, pos, "invalid capture name"))
		}
		pattern := "*"
		if pos < len(text) && text[pos] == ':' {
			pos++
			if pos < len(text) && text[pos] == '~' {
				regexStart := pos + 1
				pos++
				for pos < len(text) {
					if text[pos] == '\\' {
						if pos+1 >= len(text) { pos = len(text); break }
						pos += 2
						continue
					}
					if text[pos] == '}' { break }
					pos++
				}
				if pos == len(text) {
					t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(groupStart, pos, "unclosed regular-expression capture group"))
					start = groupStart
					break
				}
				appendRegexTargetPart(a, t, text, groupStart, regexStart, pos, capture)
				pos++
				start = pos
				continue
			}
			patternStart := pos
			for pos < len(text) && text[pos] != '}' {
				pos++
			}
			pattern = text[patternStart:pos]
			if !validPattern(pattern) {
				t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(patternStart, pos, "invalid capture pattern"))
			}
		}
		if pos == len(text) || text[pos] != '}' {
			t.Diagnostics = slices.Append(a, t.Diagnostics, parseDiagnostic(groupStart, pos, "unclosed capture group"))
			start = groupStart
			break
		}
		pos++
		t.Parts = slices.Append(a, t.Parts, TargetPart{Kind: Capture, Name: capture, Pattern: pattern, Span: source.Span{Start: groupStart, End: pos}})
		start = pos
	}
	if start < len(text) {
		t.Parts = slices.Append(a, t.Parts, TargetPart{Kind: TargetLiteral, Text: targetLiteral(a, text[start:]), Span: source.Span{Start: start, End: len(text)}})
	}
	return t
}

func appendRegexTargetPart(a mem.Allocator, target *Target, text string, groupStart int, regexStart int, groupEnd int, name string) {
	parsed := expr.ParsePatternText(a, text[groupStart:groupEnd+1], groupStart)
	if len(parsed.Diagnostics) != 0 || parsed.Pattern.Matchers != 1 || parsed.Pattern.References != 0 {
		for i := range parsed.Diagnostics { target.Diagnostics = slices.Append(a, target.Diagnostics, parsed.Diagnostics[i]) }
		if len(parsed.Diagnostics) == 0 { target.Diagnostics = slices.Append(a, target.Diagnostics, parseDiagnostic(groupStart, groupEnd+1, "invalid regular-expression capture group")) }
		parsed.Pattern.Free(a)
		slices.Free(a, parsed.Diagnostics)
		return
	}
	slices.Free(a, parsed.Diagnostics)
	target.Parts = slices.Append(a, target.Parts, TargetPart{Kind: Capture, Name: name, Pattern: text[regexStart:groupEnd], Regex: parsed.Pattern, Span: source.Span{Start: groupStart, End: groupEnd+1}})
}

func targetLiteral(a mem.Allocator, text string) string {
	var out strings.Builder
	out = strings.NewBuilder(a)
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) {
			i++
			out.WriteByte(text[i])
		} else {
			out.WriteByte(text[i])
		}
	}
	value := owned(a, out.String())
	out.Free()
	return value
}

func parseDiagnostic(start int, end int, message string) source.Diagnostic {
	return source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: start, End: end}, Message: message}
}

func validCapture(name string) bool {
	if len(name) == 0 || !isCaptureStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isCaptureContinue(name[i]) {
			return false
		}
	}
	return true
}

func isCaptureStart(b byte) bool    { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func isCaptureContinue(b byte) bool { return isCaptureStart(b) || isDigit(b) || b == '-' }

func validPattern(pattern string) bool {
	if len(pattern) == 0 {
		return false
	}
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			if i == len(pattern) {
				return false
			}
			continue
		}
		if pattern[i] == '{' || pattern[i] == '}' || pattern[i] == ':' {
			return false
		}
		if pattern[i] == '[' {
			end := i + 1
			if end < len(pattern) && pattern[end] == '!' {
				end++
			}
			if end == len(pattern) || pattern[end] == ']' {
				return false
			}
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end == len(pattern) {
				return false
			}
			if !validClass(pattern[i+1 : end]) {
				return false
			}
			i = end
		}
	}
	return true
}

func validClass(class string) bool {
	start := 0
	if class[0] == '!' {
		start = 1
	}
	for i := start; i < len(class); {
		if i+2 < len(class) && class[i+1] == '-' {
			if class[i] > class[i+2] {
				return false
			}
			i += 3
		} else {
			i++
		}
	}
	return true
}

type CaptureValue struct {
	Name string
	Text string
}

type Match struct {
	Captures []CaptureValue
	Limited  bool
}

func (m *Match) Free(a mem.Allocator) {
	if m != nil {
		slices.Free(a, m.Captures)
		mem.Free(a, m)
	}
}

// MatchTarget applies anchored, leftmost-shortest target matching.
func (t *Target) MatchTarget(a mem.Allocator, target string) *Match {
	var captures []CaptureValue
	budget := expr.RegexStepLimit
	matched, limited := t.match(a, target, 0, 0, &captures, &budget)
	if limited {
		slices.Free(a, captures)
		m := mem.Alloc[Match](a)
		m.Limited = true
		return m
	}
	if !matched {
		slices.Free(a, captures)
		return nil
	}
	m := mem.Alloc[Match](a)
	m.Captures = slices.Clone(a, captures)
	slices.Free(a, captures)
	return m
}

func (t *Target) match(a mem.Allocator, target string, part int, offset int, captures *[]CaptureValue, budget *int) (bool, bool) {
	if part == len(t.Parts) {
		return offset == len(target), false
	}
	p := t.Parts[part]
	if p.Kind == TargetLiteral {
		if len(target)-offset < len(p.Text) || target[offset:offset+len(p.Text)] != p.Text {
			return false, false
		}
		return t.match(a, target, part+1, offset+len(p.Text), captures, budget)
	}
	previous := ""
	found := false
	if p.Name != "" {
		for i := range *captures {
			if (*captures)[i].Name == p.Name {
				previous, found = (*captures)[i].Text, true
				break
			}
		}
	}
	for end := offset + 1; end <= len(target); end++ {
		value := target[offset:end]
		if found && value != previous {
			continue
		}
		if p.Regex != nil {
			result := p.Regex.MatchTextBudget(a, value, budget)
			slices.Free(a, result.Captures)
			if result.Limited { return false, true }
			if !result.Matched { continue }
		} else if !matchPattern(p.Pattern, value) {
			continue
		}
		*captures = slices.Append(a, *captures, CaptureValue{Name: p.Name, Text: value})
		matched, limited := t.match(a, target, part+1, end, captures, budget)
		if matched { return true, false }
		if limited { return false, true }
		*captures = (*captures)[:len(*captures)-1]
	}
	return false, false
}

func matchPattern(pattern string, value string) bool { return matchPatternAt(pattern, value, 0, 0) }

func matchPatternAt(pattern string, value string, pi int, vi int) bool {
	if pi == len(pattern) {
		return vi == len(value)
	}
	if pattern[pi] == '*' {
		cross := pi+1 < len(pattern) && pattern[pi+1] == '*'
		next := pi + 1
		if cross {
			next++
		}
		for end := vi + 1; end <= len(value); end++ {
			if !cross && value[end-1] == '/' {
				break
			}
			if matchPatternAt(pattern, value, next, end) {
				return true
			}
		}
		return false
	}
	if pattern[pi] == '?' {
		return vi < len(value) && value[vi] != '/' && matchPatternAt(pattern, value, pi+1, vi+1)
	}
	if pattern[pi] == '\\' {
		return pi+1 < len(pattern) && vi < len(value) && pattern[pi+1] == value[vi] && matchPatternAt(pattern, value, pi+2, vi+1)
	}
	if pattern[pi] == '[' {
		end := pi + 1
		for end < len(pattern) && pattern[end] != ']' {
			end++
		}
		return end < len(pattern) && vi < len(value) && classMatches(pattern[pi+1:end], value[vi]) && matchPatternAt(pattern, value, end+1, vi+1)
	}
	return vi < len(value) && pattern[pi] == value[vi] && matchPatternAt(pattern, value, pi+1, vi+1)
}

func classMatches(class string, value byte) bool {
	invert, i, matched := false, 0, false
	if len(class) != 0 && class[0] == '!' {
		invert, i = true, 1
	}
	for i < len(class) {
		if i+2 < len(class) && class[i+1] == '-' {
			if value >= class[i] && value <= class[i+2] {
				matched = true
			}
			i += 3
		} else {
			if value == class[i] {
				matched = true
			}
			i++
		}
	}
	if invert {
		return !matched
	}
	return matched
}

func FormatTarget(a mem.Allocator, t *Target) string {
	b := strings.NewBuilder(a)
	for i := range t.Parts {
		part := t.Parts[i]
		if part.Kind == TargetLiteral {
			for j := 0; j < len(part.Text); j++ {
				if part.Text[j] == '\\' || part.Text[j] == '{' || part.Text[j] == '}' {
					b.WriteByte('\\')
				}
				b.WriteByte(part.Text[j])
			}
			continue
		}
		b.WriteByte('{')
		if part.Name == "" {
			if part.Regex != nil { b.WriteByte('~') }
			b.WriteString(part.Pattern)
			b.WriteByte('}')
			continue
		}
		b.WriteString(part.Name)
		if part.Regex != nil {
			b.WriteString(":~")
			b.WriteString(part.Pattern)
		} else if part.Pattern != "*" {
			b.WriteByte(':')
			b.WriteString(part.Pattern)
		}
		b.WriteByte('}')
	}
	value := owned(a, b.String())
	b.Free()
	return value
}
