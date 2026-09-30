// Pattern parsing, matching, and expansion for expression pattern literals.
package expr

import (
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

// PatternPartKind classifies one segment of a pattern literal.
type PatternPartKind int

const (
	PatternLiteral PatternPartKind = iota
	PatternMatcher
	PatternReference
)

// PatternPart is one pattern segment. Text holds decoded literal text or a
// reference name. Pattern holds "*" or "**" for matchers. Index holds the
// capture slot for matchers and the positional index for {_N} references.
type PatternPart struct {
	Kind    PatternPartKind
	Text    string
	Pattern string
	Index   int
	Span    source.Span
}

// Pattern is a parsed path or string pattern. It owns its parts.
type Pattern struct {
	Alloc      mem.Allocator
	Parts      []PatternPart
	Names      []string
	Matchers   int
	References int
}

// Free releases pattern storage owned by allocator a.
func (p *Pattern) Free(a mem.Allocator) {
	if p == nil { return }
	for i := range p.Parts {
		mem.FreeString(a, p.Parts[i].Text)
		mem.FreeString(a, p.Parts[i].Pattern)
	}
	for i := range p.Names {
		mem.FreeString(a, p.Names[i])
	}
	slices.Free(a, p.Parts)
	slices.Free(a, p.Names)
	mem.Free(a, p)
}

// Clone returns an independent pattern owned by allocator a.
func (p *Pattern) Clone(a mem.Allocator) *Pattern {
	if p == nil { return nil }
	copy := mem.Alloc[Pattern](a)
	copy.Alloc = a
	for i := range p.Parts {
		part := p.Parts[i]
		part.Text = ownedText(a, part.Text)
		part.Pattern = ownedText(a, part.Pattern)
		copy.Parts = slices.Append(a, copy.Parts, part)
	}
	for i := range p.Names {
		copy.Names = slices.Append(a, copy.Names, ownedText(a, p.Names[i]))
	}
	copy.Matchers, copy.References = p.Matchers, p.References
	return copy
}

type patternParser struct {
	a      mem.Allocator
	text   string
	offset int
	pos    int
	diags  []source.Diagnostic
}

// PatternParse separates a parsed pattern from its diagnostics. Both are
// owned by the caller's allocator.
type PatternParse struct {
	Pattern     *Pattern
	Diagnostics []source.Diagnostic
}

// ParsePatternText parses pattern group syntax from text. Diagnostics span
// positions offset by the caller's source offset. A structurally invalid
// pattern returns diagnostics and no classification; callers may fall back to
// treating the text as a plain path or string. Classification of a valid
// parse is the caller's decision.
func ParsePatternText(a mem.Allocator, text string, offset int) PatternParse {
	p := patternParser{a: a, text: text, offset: offset}
	pattern := mem.Alloc[Pattern](a)
	pattern.Alloc = a
	p.parse(pattern)
	if p.pos < len(text) { p.fail(p.pos, len(text), "invalid pattern"); p.pos = len(text) }
	pattern.Matchers, pattern.References = countGroups(pattern)
	return PatternParse{Pattern: pattern, Diagnostics: p.diags}
}

func (p *patternParser) fail(start int, end int, message string) {
	p.diags = slices.Append(p.a, p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: p.offset + start, End: p.offset + end}, Message: message})
}

func countGroups(pattern *Pattern) (int, int) {
	matchers, references := 0, 0
	for i := range pattern.Parts {
		if pattern.Parts[i].Kind == PatternMatcher { matchers++ }
		if pattern.Parts[i].Kind == PatternReference { references++ }
	}
	return matchers, references
}

func (p *patternParser) parse(pattern *Pattern) {
	var literal strings.Builder
	literal = strings.NewBuilder(p.a)
	literalStart := p.pos
	for p.pos < len(p.text) {
		b := p.text[p.pos]
		if b == '\\' {
			if p.pos+1 == len(p.text) { p.fail(p.pos, p.pos+1, "unfinished pattern escape"); break }
			literal.WriteByte(p.text[p.pos+1])
			p.pos += 2
			continue
		}
		if b == '{' {
			p.flushLiteral(pattern, &literal, literalStart, p.pos)
			if !p.group(pattern) { literal.Free(); return }
			literalStart = p.pos
			continue
		}
		literal.WriteByte(b)
		p.pos++
	}
	p.flushLiteral(pattern, &literal, literalStart, p.pos)
	literal.Free()
}

func (p *patternParser) flushLiteral(pattern *Pattern, literal *strings.Builder, start int, end int) {
	if literal.Len() == 0 && start == end { return }
	pattern.Parts = slices.Append(p.a, pattern.Parts, PatternPart{Kind: PatternLiteral, Text: ownedText(p.a, literal.String()), Span: source.Span{Start: p.offset + start, End: p.offset + end}})
	literal.Reset()
}

func (p *patternParser) group(pattern *Pattern) bool {
	start := p.pos
	p.pos++
	if p.pos < len(p.text) && p.text[p.pos] == '*' {
		cross := p.pos+1 < len(p.text) && p.text[p.pos+1] == '*'
		end := p.pos + 1
		if cross { end++ }
		if end == len(p.text) || p.text[end] != '}' {
			p.fail(start, end+1, "invalid anonymous matcher")
			p.pos = end
			return false
		}
		patternStr := "*"
		if cross { patternStr = "**" }
		pattern.Parts = slices.Append(p.a, pattern.Parts, PatternPart{Kind: PatternMatcher, Pattern: ownedText(p.a, patternStr), Index: pattern.Matchers, Span: source.Span{Start: p.offset + start, End: p.offset + end + 1}})
		pattern.Names = slices.Append(p.a, pattern.Names, "")
		pattern.Matchers++
		p.pos = end + 1
		return true
	}
	if p.pos < len(p.text) && p.text[p.pos] == '_' && p.positional(p.pos+1) {
		digitsEnd := p.pos + 1
		for digitsEnd < len(p.text) && isPatternDigit(p.text[digitsEnd]) { digitsEnd++ }
		index := 0
		for i := p.pos + 1; i < digitsEnd; i++ { index = index*10 + int(p.text[i]-'0') }
		if digitsEnd == len(p.text) || p.text[digitsEnd] != '}' {
			p.fail(start, digitsEnd+1, "unclosed pattern group")
			p.pos = digitsEnd
			return false
		}
		pattern.Parts = slices.Append(p.a, pattern.Parts, PatternPart{Kind: PatternReference, Index: index, Span: source.Span{Start: p.offset + start, End: p.offset + digitsEnd + 1}})
		pattern.References++
		p.pos = digitsEnd + 1
		return true
	}
	nameStart := p.pos
	for p.pos < len(p.text) && isPatternNameContinue(p.text[p.pos]) { p.pos++ }
	name := p.text[nameStart:p.pos]
	if len(name) == 0 {
		p.fail(start, p.pos+1, "empty pattern group")
		return false
	}
	if p.pos < len(p.text) && p.text[p.pos] == ':' {
		p.pos++
		patternStart := p.pos
		for p.pos < len(p.text) && p.text[p.pos] != '}' {
			if p.text[p.pos] == '\\' { p.pos++ }
			p.pos++
		}
		glob := p.text[patternStart:p.pos]
		if p.pos == len(p.text) { p.fail(start, p.pos, "unclosed pattern group"); return false }
		if !validGlob(glob) { p.fail(patternStart, p.pos, "invalid capture pattern"); return false }
		pattern.Parts = slices.Append(p.a, pattern.Parts, PatternPart{Kind: PatternMatcher, Text: ownedText(p.a, name), Pattern: ownedText(p.a, glob), Index: pattern.Matchers, Span: source.Span{Start: p.offset + start, End: p.offset + p.pos + 1}})
		pattern.Names = slices.Append(p.a, pattern.Names, ownedText(p.a, name))
		pattern.Matchers++
		p.pos++
		return true
	}
	if p.pos == len(p.text) || p.text[p.pos] != '}' {
		p.fail(start, p.pos+1, "unclosed pattern group")
		return false
	}
	if !validPatternName(name) { p.fail(nameStart, p.pos, "invalid reference name"); return false }
	pattern.Parts = slices.Append(p.a, pattern.Parts, PatternPart{Kind: PatternReference, Text: ownedText(p.a, name), Index: -1, Span: source.Span{Start: p.offset + start, End: p.offset + p.pos + 1}})
	pattern.References++
	p.pos++
	return true
}

// positional reports whether text at start begins a canonical decimal index.
func (p *patternParser) positional(start int) bool {
	if start >= len(p.text) || !isPatternDigit(p.text[start]) { return false }
	if p.text[start] == '0' { return start+1 < len(p.text) && p.text[start+1] == '}' }
	return true
}

func isPatternDigit(b byte) bool { return b >= '0' && b <= '9' }
func isPatternNameContinue(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || isPatternDigit(b) || b == '-'
}

func validPatternName(name string) bool {
	if len(name) == 0 || !(name[0] == '_' || (name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) { return false }
	for i := 1; i < len(name); i++ {
		if !isPatternNameContinue(name[i]) { return false }
	}
	return true
}

// CanonicalPattern renders allocator-owned canonical pattern text. Literal
// braces and backslashes are re-escaped so the text round-trips.
func CanonicalPattern(a mem.Allocator, pattern *Pattern) string {
	var b strings.Builder
	b = strings.NewBuilder(a)
	defer b.Free()
	for i := range pattern.Parts {
		part := pattern.Parts[i]
		if part.Kind == PatternLiteral {
			for j := 0; j < len(part.Text); j++ {
				if part.Text[j] == '\\' || part.Text[j] == '{' || part.Text[j] == '}' { b.WriteByte('\\') }
				b.WriteByte(part.Text[j])
			}
			continue
		}
		b.WriteByte('{')
		if part.Kind == PatternMatcher {
			if part.Text != "" { b.WriteString(part.Text); b.WriteByte(':') }
			b.WriteString(part.Pattern)
		} else if part.Text != "" {
			b.WriteString(part.Text)
		} else {
			b.WriteByte('_')
			var buffer [strconv.MaxIntBase10Len]byte
			b.WriteString(strconv.FormatInt(buffer[:], int64(part.Index), 10))
		}
		b.WriteByte('}')
	}
	return ownedText(a, b.String())
}

// HasGroups reports whether the pattern contains any group.
func HasGroups(pattern *Pattern) bool { return pattern.Matchers != 0 || pattern.References != 0 }

// MatchResult carries positional capture texts and the match outcome.
type MatchResult struct {
	Captures []string
	Matched  bool
}

// MatchText matches the pattern against the complete subject. Matching is
// anchored and leftmost-shortest. Captures borrow the subject and the caller
// frees the slice.
func (p *Pattern) MatchText(a mem.Allocator, subject string) MatchResult {
	var slots []string
	if !p.match(a, subject, 0, 0, &slots) {
		slices.Free(a, slots)
		return MatchResult{}
	}
	return MatchResult{Captures: slots, Matched: true}
}

func (p *Pattern) match(a mem.Allocator, subject string, part int, offset int, slots *[]string) bool {
	if part == len(p.Parts) { return offset == len(subject) }
	current := p.Parts[part]
	if current.Kind == PatternLiteral {
		if len(subject)-offset < len(current.Text) || subject[offset:offset+len(current.Text)] != current.Text { return false }
		return p.match(a, subject, part+1, offset+len(current.Text), slots)
	}
	if current.Kind == PatternReference { return false }
	previous, found := "", false
	for i := range p.Names {
		if p.Names[i] != "" && p.Names[i] == current.Text && i < len(*slots) { previous, found = (*slots)[i], true; break }
	}
	for end := offset + 1; end <= len(subject); end++ {
		value := subject[offset:end]
		if found && value != previous { continue }
		if !matchGlob(current.Pattern, value) { continue }
		if !found {
			for len(*slots) <= current.Index { *slots = slices.Append(a, *slots, "") }
			(*slots)[current.Index] = value
		}
		if p.match(a, subject, part+1, end, slots) { return true }
		if !found { (*slots)[current.Index] = "" }
	}
	return false
}

// Expansion carries the expanded text and the name of the first missing
// reference. An empty Missing means every reference resolved. Text and
// Missing are owned by the allocator.
type Expansion struct {
	Text    string
	Missing string
}

// ExpandText renders the expansion pattern with captured values. Named
// references resolve through the match pattern's capture names. It returns
// the expanded text and the name of the first missing reference (empty when
// every reference resolved). Text and Missing are owned by the allocator.
func (p *Pattern) ExpandText(a mem.Allocator, match *Pattern, slots []string) Expansion {
	var b strings.Builder
	b = strings.NewBuilder(a)
	defer b.Free()
	for i := range p.Parts {
		part := p.Parts[i]
		if part.Kind != PatternReference { b.WriteString(part.Text); continue }
		index := part.Index
		if part.Text != "" {
			index = -1
			for j := range match.Names {
				if match.Names[j] == part.Text { index = j; break }
			}
		}
		if index < 0 || index >= len(slots) {
			name := part.Text
			if name == "" {
				var buffer [strconv.MaxIntBase10Len]byte
				name = "_" + strconv.FormatInt(buffer[:], int64(part.Index), 10)
			}
			return Expansion{Missing: ownedText(a, name)}
		}
		b.WriteString(slots[index])
	}
	return Expansion{Text: ownedText(a, b.String())}
}

func matchGlob(pattern string, value string) bool { return matchGlobAt(pattern, value, 0, 0) }

func matchGlobAt(pattern string, value string, pi int, vi int) bool {
	if pi == len(pattern) { return vi == len(value) }
	if pattern[pi] == '*' {
		cross := pi+1 < len(pattern) && pattern[pi+1] == '*'
		next := pi + 1
		if cross { next++ }
		for end := vi + 1; end <= len(value); end++ {
			if !cross && value[end-1] == '/' { break }
			if matchGlobAt(pattern, value, next, end) { return true }
		}
		return false
	}
	if pattern[pi] == '?' { return vi < len(value) && value[vi] != '/' && matchGlobAt(pattern, value, pi+1, vi+1) }
	if pattern[pi] == '\\' {
		return pi+1 < len(pattern) && vi < len(value) && pattern[pi+1] == value[vi] && matchGlobAt(pattern, value, pi+2, vi+1)
	}
	if pattern[pi] == '[' {
		end := pi + 1
		for end < len(pattern) && pattern[end] != ']' { end++ }
		return end < len(pattern) && vi < len(value) && classMatches(pattern[pi+1:end], value[vi]) && matchGlobAt(pattern, value, end+1, vi+1)
	}
	return vi < len(value) && pattern[pi] == value[vi] && matchGlobAt(pattern, value, pi+1, vi+1)
}

func classMatches(class string, value byte) bool {
	invert, i, matched := false, 0, false
	if len(class) != 0 && class[0] == '!' { invert, i = true, 1 }
	for i < len(class) {
		if i+2 < len(class) && class[i+1] == '-' {
			if value >= class[i] && value <= class[i+2] { matched = true }
			i += 3
		} else {
			if value == class[i] { matched = true }
			i++
		}
	}
	if invert { return !matched }
	return matched
}

func validGlob(pattern string) bool {
	if len(pattern) == 0 { return false }
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			if i == len(pattern) { return false }
			continue
		}
		if pattern[i] == '{' || pattern[i] == '}' || pattern[i] == ':' { return false }
		if pattern[i] == '[' {
			end := i + 1
			if end < len(pattern) && pattern[end] == '!' { end++ }
			if end == len(pattern) || pattern[end] == ']' { return false }
			for end < len(pattern) && pattern[end] != ']' { end++ }
			if end == len(pattern) { return false }
			if !validGlobClass(pattern[i+1:end]) { return false }
			i = end
		}
	}
	return true
}

func validGlobClass(class string) bool {
	start := 0
	if class[0] == '!' { start = 1 }
	for i := start; i < len(class); {
		if i+2 < len(class) && class[i+1] == '-' {
			if class[i] > class[i+2] { return false }
			i += 3
		} else {
			i++
		}
	}
	return true
}

func ownedText(a mem.Allocator, text string) string {
	if len(text) == 0 { return "" }
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
