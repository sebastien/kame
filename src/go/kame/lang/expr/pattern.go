// Pattern parsing, matching, and expansion for expression pattern literals.
package expr

import (
	"kame/internal/mem"
	"kame/lang/source"
	"path"

	"strconv"
	"strings"
)

// PatternPartKind classifies one segment of a pattern literal.
type PatternPartKind int

const (
	PatternLiteral PatternPartKind = iota
	PatternMatcher
	PatternReference
	// PatternInterpolation indexes an expression operand owned by Expr.Parts.
	// It exists only in computed path ASTs, never in runtime pattern text.
	PatternInterpolation
	PatternGlob
)

// PatternPart is one pattern segment. Text holds decoded literal text or a
// reference name. Pattern holds "*" or "**" for matchers. Index holds the
// capture slot for matchers, positional index for {_N} references, or expression
// operand ordinal for AST-only interpolation placeholders.
type PatternPart struct {
	Kind    PatternPartKind
	Text    string
	Pattern string
	Regex   bool
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
	Globs      int
}

// Free releases pattern storage owned by allocator a.
func (p *Pattern) Free(a mem.Allocator) {
	if p == nil {
		return
	}
	for i := range p.Parts {
		mem.FreeString(a, p.Parts[i].Text)
		mem.FreeString(a, p.Parts[i].Pattern)
	}
	for i := range p.Names {
		mem.FreeString(a, p.Names[i])
	}
	mem.FreeSlice(nil, p.Parts)
	mem.FreeSlice(nil, p.Names)
	mem.Free(a, p)
}

// Clone returns an independent pattern owned by allocator a.
func (p *Pattern) Clone(a mem.Allocator) *Pattern {
	if p == nil {
		return nil
	}
	copy := mem.Alloc[Pattern](a)
	copy.Alloc = a
	for i := range p.Parts {
		part := p.Parts[i]
		part.Text = ownedText(a, part.Text)
		part.Pattern = ownedText(a, part.Pattern)
		copy.Parts = append(copy.Parts, part)
	}
	for i := range p.Names {
		copy.Names = append(copy.Names, ownedText(a, p.Names[i]))
	}
	copy.Matchers, copy.References, copy.Globs = p.Matchers, p.References, p.Globs
	return copy
}

type patternParser struct {
	a      mem.Allocator
	text   string
	offset int
	pos    int
	diags  []source.Diagnostic
	path   bool
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
	if p.pos < len(text) {
		p.fail(p.pos, len(text), "invalid pattern")
		p.pos = len(text)
	}
	pattern.Matchers, pattern.References = countGroups(pattern)
	return PatternParse{Pattern: pattern, Diagnostics: p.diags}
}

// ParsePathPatternParts parses authored chunks without interpreting inserted
// values. Groups must be wholly contained in one text part. Spans retain source
// byte offsets; interpolation indices follow expression operand order.
func ParsePathPatternParts(a mem.Allocator, parts []StringPart) PatternParse {
	p := patternParser{a: a, path: true}
	pattern := mem.Alloc[Pattern](a)
	pattern.Alloc = a
	next := 0
	for i := range parts {
		part := parts[i]
		if part.Expr != nil {
			pattern.Parts = append(pattern.Parts, PatternPart{Kind: PatternInterpolation, Index: next, Span: part.Span})
			next++
			continue
		}
		p.text, p.offset, p.pos = part.Text, part.Span.Start, 0
		p.parse(pattern)
		if len(p.diags) != 0 {
			break
		}
	}
	pattern.Matchers, pattern.References = countGroups(pattern)
	return PatternParse{Pattern: pattern, Diagnostics: p.diags}
}

// ParsePathPatternText reads canonical runtime path pattern or template text.
func ParsePathPatternText(a mem.Allocator, text string) PatternParse {
	return ParsePathPatternParts(a, []StringPart{{Text: text, Span: source.Span{End: len(text)}}})
}

// NormalizePathPatternRoot resolves the fixed leading directory as path space.
// Matcher syntax and the part of the first component before a matcher stay literal.
func NormalizePathPatternRoot(a mem.Allocator, pattern *Pattern, cwd string) {
	var leading string
	literal := len(pattern.Parts) != 0 && pattern.Parts[0].Kind == PatternLiteral
	if literal {
		leading = pattern.Parts[0].Text
	}
	cut := strings.LastIndexByte(leading, '/') + 1
	directory := leading[:cut]
	if path.IsAbs(directory) {
		directory = path.Clean(directory)
	} else {
		directory = path.Join(cwd, directory)
	}
	defer mem.FreeString(a, directory)
	if directory == "." || directory == "" {
		leading = leading[cut:]
	} else {
		leading = strings.TrimSuffix(directory, "/") + "/" + leading[cut:]
	}
	text := ownedText(a, leading)
	if literal {
		mem.FreeString(a, pattern.Parts[0].Text)
		pattern.Parts[0].Text = text
	} else if text != "" {
		pattern.Parts = append(pattern.Parts, PatternPart{})
		copy(pattern.Parts[1:], pattern.Parts[:len(pattern.Parts)-1])
		pattern.Parts[0] = PatternPart{Kind: PatternLiteral, Text: text}
	}
}

func (p *patternParser) fail(start int, end int, message string) {
	p.diags = append(p.diags, source.Diagnostic{Code: "PARSE_ERR", Severity: source.Error, Span: source.Span{Start: p.offset + start, End: p.offset + end}, Message: message})
}

func countGroups(pattern *Pattern) (int, int) {
	matchers, references := 0, 0
	for i := range pattern.Parts {
		if pattern.Parts[i].Kind == PatternMatcher {
			matchers++
		}
		if pattern.Parts[i].Kind == PatternReference {
			references++
		}
	}
	return matchers, references
}

func (p *Pattern) PromoteNamedCaptures() (source.Span, bool) {
	for _, part := range p.Parts {
		if part.Kind == PatternReference && part.Text == "" {
			return part.Span, false
		}
	}
	for _, name := range p.Names {
		mem.FreeString(p.Alloc, name)
	}
	p.Names = p.Names[:0]
	for i := range p.Parts {
		part := &p.Parts[i]
		if part.Kind == PatternReference {
			part.Kind = PatternMatcher
			part.Pattern = ownedText(p.Alloc, "*")
		}
		if part.Kind == PatternMatcher {
			part.Index = len(p.Names)
			p.Names = append(p.Names, ownedText(p.Alloc, part.Text))
		}
	}
	p.Matchers, p.References = len(p.Names), 0
	return source.Span{}, true
}

func (p *patternParser) parse(pattern *Pattern) {
	var literal strings.Builder
	literal = strings.Builder{}
	literalStart := p.pos
	for p.pos < len(p.text) {
		b := p.text[p.pos]
		if b == '\\' {
			if p.pos+1 == len(p.text) {
				p.fail(p.pos, p.pos+1, "unfinished pattern escape")
				break
			}
			literal.WriteByte(p.text[p.pos+1])
			p.pos += 2
			continue
		}
		if p.path && (b == '*' || b == '?' || b == '[') {
			p.flushLiteral(pattern, &literal, literalStart, p.pos)
			start := p.pos
			p.pos++
			if b == '*' && p.pos < len(p.text) && p.text[p.pos] == '*' {
				p.pos++
			}
			if b == '[' {
				for p.pos < len(p.text) && p.text[p.pos] != ']' {
					if p.text[p.pos] == '\\' && p.pos+1 < len(p.text) {
						p.pos++
					}
					p.pos++
				}
				if p.pos == len(p.text) {
					p.fail(start, p.pos, "unclosed path glob class")
					return
				}
				if !validGlobClass(p.text[start+1 : p.pos]) {
					p.fail(start, p.pos+1, "invalid glob character class")
					return
				}
				p.pos++
			}
			pattern.Parts = append(pattern.Parts, PatternPart{Kind: PatternGlob, Text: ownedText(p.a, p.text[start:p.pos]),
				Span: source.Span{Start: p.offset + start, End: p.offset + p.pos}})
			pattern.Globs++
			literalStart = p.pos
			continue
		}
		if b == '{' {
			p.flushLiteral(pattern, &literal, literalStart, p.pos)
			if !p.group(pattern) {
				literal.Reset()
				return
			}
			literalStart = p.pos
			continue
		}
		literal.WriteByte(b)
		p.pos++
	}
	p.flushLiteral(pattern, &literal, literalStart, p.pos)
	literal.Reset()
}

func (p *patternParser) flushLiteral(pattern *Pattern, literal *strings.Builder, start int, end int) {
	if literal.Len() == 0 && start == end {
		return
	}
	pattern.Parts = append(pattern.Parts, PatternPart{Kind: PatternLiteral, Text: ownedText(p.a, literal.String()), Span: source.Span{Start: p.offset + start, End: p.offset + end}})
	literal.Reset()
}

func (p *patternParser) group(pattern *Pattern) bool {
	start := p.pos
	p.pos++
	if p.pos < len(p.text) && p.text[p.pos] == '~' {
		return p.regexMatcher(pattern, "", start)
	}
	if p.pos < len(p.text) && p.text[p.pos] == '*' {
		cross := p.pos+1 < len(p.text) && p.text[p.pos+1] == '*'
		end := p.pos + 1
		if cross {
			end++
		}
		if end == len(p.text) || p.text[end] != '}' {
			p.fail(start, end+1, "invalid anonymous matcher")
			p.pos = end
			return false
		}
		patternStr := "*"
		if cross {
			patternStr = "**"
		}
		pattern.Parts = append(pattern.Parts, PatternPart{Kind: PatternMatcher, Pattern: ownedText(p.a, patternStr), Index: pattern.Matchers, Span: source.Span{Start: p.offset + start, End: p.offset + end + 1}})
		pattern.Names = append(pattern.Names, "")
		pattern.Matchers++
		p.pos = end + 1
		return true
	}
	if p.pos < len(p.text) && p.text[p.pos] == '_' && p.positional(p.pos+1) {
		digitsEnd := p.pos + 1
		for digitsEnd < len(p.text) && isPatternDigit(p.text[digitsEnd]) {
			digitsEnd++
		}
		index := 0
		for i := p.pos + 1; i < digitsEnd; i++ {
			digit := int(p.text[i] - '0')
			if index > (int(^uint(0)>>1)-digit)/10 {
				p.fail(start, digitsEnd, "pattern position exceeds integer range")
				p.pos = digitsEnd
				return false
			}
			index = index*10 + digit
		}
		if digitsEnd == len(p.text) || p.text[digitsEnd] != '}' {
			p.fail(start, digitsEnd+1, "unclosed pattern group")
			p.pos = digitsEnd
			return false
		}
		pattern.Parts = append(pattern.Parts, PatternPart{Kind: PatternReference, Index: index, Span: source.Span{Start: p.offset + start, End: p.offset + digitsEnd + 1}})
		pattern.References++
		p.pos = digitsEnd + 1
		return true
	}
	nameStart := p.pos
	for p.pos < len(p.text) && isPatternNameContinue(p.text[p.pos]) {
		p.pos++
	}
	name := p.text[nameStart:p.pos]
	if len(name) == 0 {
		p.fail(start, p.pos+1, "empty pattern group")
		return false
	}
	if p.pos < len(p.text) && p.text[p.pos] == ':' {
		p.pos++
		if p.pos < len(p.text) && p.text[p.pos] == '~' {
			return p.regexMatcher(pattern, name, start)
		}
		patternStart := p.pos
		for p.pos < len(p.text) && p.text[p.pos] != '}' {
			if p.text[p.pos] == '\\' {
				if p.pos+1 == len(p.text) {
					p.fail(p.pos, p.pos+1, "unfinished pattern escape")
					p.pos = len(p.text)
					return false
				}
				p.pos++
			}
			p.pos++
		}
		glob := p.text[patternStart:p.pos]
		if p.pos == len(p.text) {
			p.fail(start, p.pos, "unclosed pattern group")
			return false
		}
		if !ValidGlob(glob) {
			p.fail(patternStart, p.pos, "invalid capture pattern")
			return false
		}
		pattern.Parts = append(pattern.Parts, PatternPart{Kind: PatternMatcher, Text: ownedText(p.a, name), Pattern: ownedText(p.a, glob), Index: pattern.Matchers, Span: source.Span{Start: p.offset + start, End: p.offset + p.pos + 1}})
		pattern.Names = append(pattern.Names, ownedText(p.a, name))
		pattern.Matchers++
		p.pos++
		return true
	}
	if p.pos == len(p.text) || p.text[p.pos] != '}' {
		p.fail(start, p.pos+1, "unclosed pattern group")
		return false
	}
	if !validPatternName(name) {
		p.fail(nameStart, p.pos, "invalid reference name")
		return false
	}
	pattern.Parts = append(pattern.Parts, PatternPart{Kind: PatternReference, Text: ownedText(p.a, name), Index: -1, Span: source.Span{Start: p.offset + start, End: p.offset + p.pos + 1}})
	pattern.References++
	p.pos++
	return true
}

func (p *patternParser) regexMatcher(pattern *Pattern, name string, start int) bool {
	p.pos++ // leading '~'
	regexStart := p.pos
	for p.pos < len(p.text) {
		if p.text[p.pos] == '\\' {
			if p.pos+1 == len(p.text) {
				p.pos = len(p.text)
				break
			}
			p.pos += 2
			continue
		}
		if p.text[p.pos] == '}' {
			break
		}
		p.pos++
	}
	if p.pos == len(p.text) {
		p.fail(start, p.pos, "unclosed regular-expression matcher")
		return false
	}
	regex := p.text[regexStart:p.pos]
	compiled := compileRegex(p.a, regex)
	if !compiled.Valid {
		p.fail(regexStart+compiled.ErrorOffset, regexStart+compiled.ErrorOffset+1, "invalid regular-expression matcher")
		p.pos = len(p.text)
		return false
	}
	compiled.Program.Free(p.a)
	end := p.pos + 1
	pattern.Parts = append(pattern.Parts, PatternPart{Kind: PatternMatcher, Text: ownedText(p.a, name), Pattern: ownedText(p.a, regex), Regex: true, Index: pattern.Matchers, Span: source.Span{Start: p.offset + start, End: p.offset + end}})
	pattern.Names = append(pattern.Names, ownedText(p.a, name))
	pattern.Matchers++
	p.pos = end
	return true
}

// positional reports whether text at start begins a canonical decimal index.
func (p *patternParser) positional(start int) bool {
	if start >= len(p.text) || !isPatternDigit(p.text[start]) {
		return false
	}
	if p.text[start] == '0' {
		return start+1 < len(p.text) && p.text[start+1] == '}'
	}
	return true
}

func isPatternDigit(b byte) bool { return b >= '0' && b <= '9' }
func isPatternNameContinue(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || isPatternDigit(b) || b == '-'
}

func validPatternName(name string) bool {
	if len(name) == 0 || !(name[0] == '_' || (name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isPatternNameContinue(name[i]) {
			return false
		}
	}
	return true
}

// CanonicalPattern renders allocator-owned canonical pattern text. Literal
// braces and backslashes are re-escaped so the text round-trips. Computed path
// placeholders must first be instantiated as PatternLiteral parts.
func CanonicalPattern(a mem.Allocator, pattern *Pattern) string {
	return canonicalPattern(a, pattern, false)
}

func CanonicalPathPattern(a mem.Allocator, pattern *Pattern) string {
	return canonicalPattern(a, pattern, true)
}

func canonicalPattern(a mem.Allocator, pattern *Pattern, path bool) string {
	var b strings.Builder
	b = strings.Builder{}
	defer b.Reset()
	for i := range pattern.Parts {
		part := pattern.Parts[i]
		if part.Kind == PatternGlob {
			b.WriteString(part.Text)
			continue
		}
		if part.Kind == PatternLiteral {
			for j := 0; j < len(part.Text); j++ {
				ch := part.Text[j]
				if strings.IndexByte("\\{}", ch) >= 0 || path && strings.IndexByte(" *?[](),;#\t", ch) >= 0 {
					b.WriteByte('\\')
				}
				b.WriteByte(ch)
			}
			continue
		}
		b.WriteByte('{')
		if part.Kind == PatternMatcher {
			if part.Text != "" {
				b.WriteString(part.Text)
				b.WriteByte(':')
			}
			if part.Regex {
				b.WriteByte('~')
			}
			b.WriteString(part.Pattern)
		} else if part.Text != "" {
			b.WriteString(part.Text)
		} else {
			b.WriteByte('_')

			b.WriteString(strconv.FormatInt(int64(part.Index), 10))
		}
		b.WriteByte('}')
	}
	return ownedText(a, b.String())
}

// HasGroups reports whether the pattern contains any group.
func HasGroups(pattern *Pattern) bool { return pattern.Matchers != 0 || pattern.References != 0 }

// MayCrossSlash reports whether a matching part can consume a path separator.
func (part PatternPart) MayCrossSlash(a mem.Allocator) bool {
	if part.Kind == PatternGlob {
		return part.Text == "**"
	}
	if part.Kind != PatternMatcher {
		return false
	}
	if part.Regex {
		compiled := compileRegex(a, part.Pattern)
		defer compiled.Program.Free(a)
		if !compiled.Valid {
			return true
		}
		for _, instruction := range compiled.Program.Instructions {
			if regexInstructionMatches(compiled.Program.Text, instruction, '/') {
				return true
			}
		}
		return false
	}
	// Character classes may include a separator by range or negation.
	return strings.Contains(part.Pattern, "**") || strings.ContainsAny(part.Pattern, "/[")
}

// MatchResult carries positional capture texts and the match outcome.
type MatchResult struct {
	Captures []string
	Matched  bool
	Limited  bool
}

// MatchText matches the pattern against the complete subject. Matching is
// anchored and leftmost-shortest. Captures borrow the subject and the caller
// frees the slice.
func (p *Pattern) MatchText(a mem.Allocator, subject string) MatchResult {
	budget := maxRegexSteps
	return p.MatchTextBudget(a, subject, &budget)
}

// MatchTextBudget charges recursive matching, capture candidates, glob work,
// and regex execution to one caller-owned bound. Exhaustion (including an
// initially zero budget) fails closed with Limited and no captures.
func (p *Pattern) MatchTextBudget(a mem.Allocator, subject string, budget *int) MatchResult {
	if budget == nil || *budget <= 0 {
		return MatchResult{Limited: true}
	}
	compiled := CompilePattern(a, p)
	defer compiled.Free(a)
	if !compiled.Valid {
		return MatchResult{}
	}
	return compiled.MatchTextBudget(a, subject, budget)
}

// CompiledPattern retains a pattern's compiled regex programs so repeated
// matches skip regex compilation. It borrows Pattern, and it is not safe for
// concurrent matching: each program reuses its matcher buffers.
type CompiledPattern struct {
	Pattern  *Pattern
	programs []*regexProgram
	Valid    bool
}

func CompilePattern(a mem.Allocator, p *Pattern) CompiledPattern {
	programs := make([]*regexProgram, p.Matchers)
	for i := range p.Parts {
		part := p.Parts[i]
		if part.Kind != PatternMatcher || !part.Regex {
			continue
		}
		compiled := compileRegex(a, part.Pattern)
		if !compiled.Valid {
			result := CompiledPattern{programs: programs}
			result.Free(a)
			return CompiledPattern{}
		}
		programs[part.Index] = compiled.Program
	}
	return CompiledPattern{Pattern: p, programs: programs, Valid: true}
}

func (c *CompiledPattern) Free(a mem.Allocator) {
	for i := range c.programs {
		if c.programs[i] != nil {
			c.programs[i].Free(a)
		}
	}
	mem.FreeSlice(nil, c.programs)
	*c = CompiledPattern{}
}

// MatchText is Pattern.MatchText with the precompiled programs.
func (c *CompiledPattern) MatchText(a mem.Allocator, subject string) MatchResult {
	budget := maxRegexSteps
	return c.MatchTextBudget(a, subject, &budget)
}

func (c *CompiledPattern) MatchTextBudget(a mem.Allocator, subject string, budget *int) MatchResult {
	if budget == nil || *budget <= 0 {
		return MatchResult{Limited: true}
	}
	var slots []string
	matched, limited := c.Pattern.match(a, subject, 0, 0, &slots, c.programs, budget)
	if !matched {
		mem.FreeSlice(nil, slots)
		return MatchResult{Limited: limited}
	}
	return MatchResult{Captures: slots, Matched: true}
}

func (p *Pattern) match(a mem.Allocator, subject string, part int, offset int, slots *[]string, programs []*regexProgram, budget *int) (bool, bool) {
	if !ChargeMatchStep(budget) {
		return false, true
	}
	if part == len(p.Parts) {
		return offset == len(subject), false
	}
	current := p.Parts[part]
	if current.Kind == PatternLiteral {
		if len(subject)-offset < len(current.Text) || subject[offset:offset+len(current.Text)] != current.Text {
			return false, false
		}
		return p.match(a, subject, part+1, offset+len(current.Text), slots, programs, budget)
	}
	if current.Kind == PatternGlob {
		return p.matchPathGlob(a, subject, part, offset, slots, programs, budget)
	}
	if current.Kind == PatternReference {
		return false, false
	}
	previous, found := "", false
	for i := 0; i < current.Index && i < len(p.Names) && i < len(*slots); i++ {
		if p.Names[i] != "" && p.Names[i] == current.Text {
			previous, found = (*slots)[i], true
			break
		}
	}
	for end := offset + 1; end <= len(subject); end++ {
		if !ChargeMatchStep(budget) {
			return false, true
		}
		value := subject[offset:end]
		if found && value != previous {
			continue
		}
		if current.Regex {
			matched, limited := regexFullMatch(a, programs[current.Index], value, budget)
			if limited {
				return false, true
			}
			if !matched {
				continue
			}
		} else {
			matched, limited := MatchGlobBudget(current.Pattern, value, budget)
			if limited {
				return false, true
			}
			if !matched {
				continue
			}
		}
		for len(*slots) <= current.Index {
			*slots = append(*slots, "")
		}
		(*slots)[current.Index] = value
		matched, limited := p.match(a, subject, part+1, end, slots, programs, budget)
		if matched {
			return true, false
		}
		if limited {
			return false, true
		}
		(*slots)[current.Index] = ""
	}
	return false, false
}

func (p *Pattern) matchPathGlob(a mem.Allocator, subject string, part, offset int, slots *[]string, programs []*regexProgram, budget *int) (bool, bool) {
	glob := p.Parts[part].Text
	if glob == "?" || strings.HasPrefix(glob, "[") {
		if offset == len(subject) || subject[offset] == '/' {
			return false, false
		}
		matched, limited := MatchGlobBudget(glob, subject[offset:offset+1], budget)
		if !matched || limited {
			return false, limited
		}
		return p.match(a, subject, part+1, offset+1, slots, programs, budget)
	}
	for end := offset + 1; end <= len(subject); end++ {
		if !ChargeMatchStep(budget) {
			return false, true
		}
		if glob == "*" && subject[end-1] == '/' {
			break
		}
		if matched, limited := p.match(a, subject, part+1, end, slots, programs, budget); matched || limited {
			return matched, limited
		}
	}
	return false, false
}

// Expansion carries the expanded text and the name of the first missing
// reference. An empty Missing means every reference resolved. Text and Missing
// are owned by the allocator.
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
	b = strings.Builder{}
	defer b.Reset()
	for i := range p.Parts {
		part := p.Parts[i]
		if part.Kind != PatternReference {
			b.WriteString(part.Text)
			continue
		}
		index := part.Index
		if part.Text != "" {
			index = -1
			for j := range match.Names {
				if match.Names[j] == part.Text {
					index = j
					break
				}
			}
		}
		if index < 0 || index >= len(slots) {
			name := part.Text
			if name == "" {

				name = "_" + strconv.FormatInt(int64(part.Index), 10)
			}
			return Expansion{Missing: ownedText(a, name)}
		}
		b.WriteString(slots[index])
	}
	return Expansion{Text: ownedText(a, b.String())}
}

// ChargeMatchStep charges shared regex/glob/capture work. A negative budget
// records an attempted step past the limit; zero remaining alone is not failure.
func ChargeMatchStep(budget *int) bool {
	if budget == nil {
		return false
	}
	if *budget <= 0 {
		*budget = -1
		return false
	}
	*budget--
	return true
}

// MatchGlob matches the complete value within the pattern work limit.
func MatchGlob(pattern, value string) (matched, limited bool) {
	budget := maxRegexSteps
	return MatchGlobBudget(pattern, value, &budget)
}

// MatchGlobBudget uses the existing nonempty-star glob matcher and shared bound.
func MatchGlobBudget(pattern string, value string, budget *int) (bool, bool) {
	matched := matchGlobAt(pattern, value, 0, 0, budget)
	return matched, budget == nil || *budget < 0
}

func matchGlobAt(pattern string, value string, pi int, vi int, budget *int) bool {
	if !ChargeMatchStep(budget) {
		return false
	}
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
			if !ChargeMatchStep(budget) {
				return false
			}
			if !cross && value[end-1] == '/' {
				break
			}
			if matchGlobAt(pattern, value, next, end, budget) {
				return true
			}
			if *budget < 0 {
				return false
			}
		}
		return false
	}
	if pattern[pi] == '?' {
		return vi < len(value) && value[vi] != '/' && matchGlobAt(pattern, value, pi+1, vi+1, budget)
	}
	if pattern[pi] == '\\' {
		return pi+1 < len(pattern) && vi < len(value) && pattern[pi+1] == value[vi] && matchGlobAt(pattern, value, pi+2, vi+1, budget)
	}
	if pattern[pi] == '[' {
		end := pi + 1
		for end < len(pattern) && pattern[end] != ']' {
			if !ChargeMatchStep(budget) {
				return false
			}
			_, end = globClassByte(pattern, end)
		}
		return end < len(pattern) && vi < len(value) && classMatches(pattern[pi+1:end], value[vi], budget) && matchGlobAt(pattern, value, end+1, vi+1, budget)
	}
	return vi < len(value) && pattern[pi] == value[vi] && matchGlobAt(pattern, value, pi+1, vi+1, budget)
}

func classMatches(class string, value byte, budget *int) bool {
	invert, i, matched := false, 0, false
	if len(class) != 0 && class[0] == '!' {
		invert, i = true, 1
	}
	for i < len(class) {
		if !ChargeMatchStep(budget) {
			return false
		}
		first, next := globClassByte(class, i)
		if next+1 < len(class) && class[next] == '-' {
			last, after := globClassByte(class, next+1)
			matched = matched || (value >= first && value <= last)
			i = after
		} else {
			matched = matched || value == first
			i = next
		}
	}
	return matched != invert
}

func globClassByte(text string, offset int) (byte, int) {
	if text[offset] == '\\' && offset+1 < len(text) {
		offset++
	}
	return text[offset], offset + 1
}

// ValidGlob reports whether a capture glob has valid escapes and character classes.
func ValidGlob(pattern string) bool {
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
				_, end = globClassByte(pattern, end)
			}
			if end == len(pattern) {
				return false
			}
			if !validGlobClass(pattern[i+1 : end]) {
				return false
			}
			i = end
		}
	}
	return true
}

func validGlobClass(class string) bool {
	start := 0
	if len(class) != 0 && class[0] == '!' {
		start = 1
	}
	if start == len(class) {
		return false
	}
	for i := start; i < len(class); {
		first, next := globClassByte(class, i)
		if next+1 < len(class) && class[next] == '-' {
			last, after := globClassByte(class, next+1)
			if first > last {
				return false
			}
			i = after
		} else {
			i = next
		}
	}
	return true
}

func ownedText(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
