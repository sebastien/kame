package expr

import (
	"slices"
	"strings"
	"unicode/utf8"
)

type overlapRunes struct{ low, high rune }

// makeHostGlobMachine models the filesystem traversal glob. Unlike a capture
// glob, ordinary * can be empty, ? and classes consume a rune, and a complete
// ** component can skip any number of ancestor directories.
func makeHostGlobMachine(pattern string) overlapMachine {
	m := overlapMachine{code: []overlapInstruction{{kind: overlapAccept}}}
	segments := strings.Split(pattern, "/")
	next := 0
	for i := len(segments) - 1; i >= 0; i-- {
		segment := segments[i]
		last := i == len(segments)-1
		if segment == "**" {
			if last {
				next = m.star(overlapWildcardBytes(true), next, false)
			} else {
				split := m.split(-1, next)
				slash := m.literal("/", split)
				m.code[split].next = m.star(overlapWildcardBytes(false), slash, false)
				next = split
			}
			continue
		}
		if !last {
			next = m.literal("/", next)
		}
		next = m.hostGlobSegment(segment, next)
	}
	m.start = next
	return m
}

func (m *overlapMachine) hostGlobSegment(text string, next int) int {
	type token struct {
		kind    byte
		literal byte
		class   []overlapRunes
		negated bool
	}
	var tokens []token
	for i := 0; i < len(text); i++ {
		value := text[i]
		switch value {
		case '*', '?':
			tokens = append(tokens, token{kind: value})
		case '[':
			ranges, negated, end, ok := overlapHostClass(text, i+1)
			if !ok {
				return m.consume(overlapBytes{}, next)
			}
			tokens = append(tokens, token{kind: '[', class: ranges, negated: negated})
			i = end - 1
		default:
			if value == '\\' {
				i++
				if i >= len(text) {
					return m.consume(overlapBytes{}, next)
				}
				value = text[i]
			}
			tokens = append(tokens, token{literal: value})
		}
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		token := tokens[i]
		switch token.kind {
		case '*':
			next = m.star(overlapWildcardBytes(false), next, true)
		case '?':
			next = m.runeClass(nil, true, next)
		case '[':
			next = m.runeClass(token.class, token.negated, next)
		default:
			next = m.consume(overlapByteRange(token.literal, token.literal), next)
		}
	}
	return next
}

func overlapHostClass(text string, start int) ([]overlapRunes, bool, int, bool) {
	i := start
	negated := i < len(text) && text[i] == '^'
	if negated {
		i++
	}
	var ranges []overlapRunes
	for i < len(text) && text[i] != ']' {
		low, end, ok := overlapClassRune(text, i)
		if !ok {
			return nil, false, 0, false
		}
		i = end
		high := low
		if i < len(text) && text[i] == '-' {
			high, i, ok = overlapClassRune(text, i+1)
			if !ok {
				return nil, false, 0, false
			}
		}
		ranges = append(ranges, overlapRunes{low, high})
	}
	return ranges, negated, i + 1, i < len(text) && len(ranges) != 0
}

func overlapClassRune(text string, i int) (rune, int, bool) {
	if i >= len(text) || text[i] == ']' || text[i] == '-' {
		return 0, 0, false
	}
	if text[i] == '\\' {
		i++
		if i == len(text) {
			return 0, 0, false
		}
	}
	r, width := utf8.DecodeRuneInString(text[i:])
	return r, i + width, r != utf8.RuneError || width != 1
}

func (m *overlapMachine) runeClass(ranges []overlapRunes, negated bool, next int) int {
	// Each domain interval has one UTF-8 width, excludes slash and NUL, and
	// does not cross the surrogate range.
	domain := [...]overlapRunes{{1, '/' - 1}, {'/' + 1, 0x7f}, {0x80, 0x7ff}, {0x800, 0xd7ff}, {0xe000, 0xffff}, {0x10000, utf8.MaxRune}}
	slices.SortFunc(ranges, func(a, b overlapRunes) int { return int(a.low - b.low) })
	var alternatives []int
	for _, block := range domain {
		if !negated {
			for _, accepted := range ranges {
				low, high := max(block.low, accepted.low), min(block.high, accepted.high)
				if low <= high {
					alternatives = append(alternatives, m.utf8Interval(string(low), string(high), next))
				}
			}
			continue
		}
		cursor := block.low
		for _, excluded := range ranges {
			if excluded.low > block.high || cursor > block.high {
				break
			}
			if excluded.low > excluded.high || excluded.high < cursor {
				continue
			}
			if cursor < excluded.low {
				alternatives = append(alternatives, m.utf8Interval(string(cursor), string(excluded.low-1), next))
			}
			cursor = excluded.high + 1
		}
		if cursor <= block.high {
			alternatives = append(alternatives, m.utf8Interval(string(cursor), string(block.high), next))
		}
	}
	if len(alternatives) == 0 {
		return m.consume(overlapBytes{}, next)
	}
	entry := alternatives[0]
	for _, alternative := range alternatives[1:] {
		entry = m.split(entry, alternative)
	}
	return entry
}

func (m *overlapMachine) utf8Interval(low, high string, next int) int {
	if len(low) == 1 {
		return m.consume(overlapByteRange(low[0], high[0]), next)
	}
	if low[0] == high[0] {
		return m.consume(overlapByteRange(low[0], low[0]), m.utf8Interval(low[1:], high[1:], next))
	}
	minSuffix := strings.Repeat("\x80", len(low)-1)
	maxSuffix := strings.Repeat("\xbf", len(low)-1)
	left := m.consume(overlapByteRange(low[0], low[0]), m.utf8Interval(low[1:], maxSuffix, next))
	right := m.consume(overlapByteRange(high[0], high[0]), m.utf8Interval(minSuffix, high[1:], next))
	entry := m.split(left, right)
	if low[0]+1 < high[0] {
		middle := m.consume(overlapByteRange(low[0]+1, high[0]-1), m.utf8Interval(minSuffix, maxSuffix, next))
		entry = m.split(entry, middle)
	}
	return entry
}
