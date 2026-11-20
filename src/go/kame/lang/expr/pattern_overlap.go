package expr

import (
	"kame/internal/mem"
	"strings"
)

type overlapBytes [4]uint64

func (b *overlapBytes) add(value byte)     { b[value/64] |= 1 << (value % 64) }
func (b overlapBytes) has(value byte) bool { return b[value/64]&(1<<(value%64)) != 0 }

func overlapByteRange(low, high byte) overlapBytes {
	var result overlapBytes
	for i := int(low); i <= int(high); i++ {
		if i != 0 {
			result.add(byte(i))
		}
	}
	return result
}

func overlapWildcardBytes(cross bool) overlapBytes {
	result := overlapByteRange(1, 255)
	if !cross {
		result[0] &^= 1 << '/'
	}
	return result
}

const (
	overlapAccept = iota
	overlapConsume
	overlapSplit
	overlapEnter
	overlapLeave
)

type overlapInstruction struct {
	kind       int
	next, alt  int
	bytes      overlapBytes
	slot       int
	first      bool
	last       bool
	occurrence int // One-based repeated capture occurrence for consuming instructions.
}

type overlapMachine struct {
	code        []overlapInstruction
	start       int
	slots       int
	occurrences []int // Owning slot for each repeated capture occurrence.
}

func (m *overlapMachine) add(instruction overlapInstruction) int {
	i := len(m.code)
	m.code = append(m.code, instruction)
	return i
}

func (m *overlapMachine) consume(bytes overlapBytes, next int) int {
	return m.add(overlapInstruction{kind: overlapConsume, bytes: bytes, next: next})
}

func (m *overlapMachine) split(left, right int) int {
	return m.add(overlapInstruction{kind: overlapSplit, next: left, alt: right})
}

func (m *overlapMachine) literal(text string, next int) int {
	for i := len(text) - 1; i >= 0; i-- {
		next = m.consume(overlapByteRange(text[i], text[i]), next)
	}
	return next
}

func (m *overlapMachine) star(bytes overlapBytes, next int, empty bool) int {
	split := m.split(-1, next)
	step := m.consume(bytes, split)
	m.code[split].next = step
	if empty {
		return split
	}
	return step
}

// glob builds a capture glob with the same nonempty stars and byte classes as
// MatchGlob. A standalone PathPattern glob cannot match slash in its classes.
func (m *overlapMachine) glob(text string, next int, pathClass bool) int {
	type token struct {
		bytes overlapBytes
		star  bool
	}
	var tokens []token
	for i := 0; i < len(text); {
		switch text[i] {
		case '*':
			cross := i+1 < len(text) && text[i+1] == '*'
			if cross {
				i++
			}
			tokens = append(tokens, token{overlapWildcardBytes(cross), true})
			i++
		case '?':
			tokens = append(tokens, token{bytes: overlapWildcardBytes(false)})
			i++
		case '[':
			end := i + 1
			for end < len(text) && text[end] != ']' {
				_, end = globClassByte(text, end)
			}
			bytes := overlapGlobClass(text[i+1 : end])
			if pathClass {
				bytes[0] &^= 1 << '/'
			}
			tokens = append(tokens, token{bytes: bytes})
			i = end + 1
		default:
			value, end := globClassByte(text, i)
			tokens = append(tokens, token{bytes: overlapByteRange(value, value)})
			i = end
		}
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		if tokens[i].star {
			next = m.star(tokens[i].bytes, next, false)
		} else {
			next = m.consume(tokens[i].bytes, next)
		}
	}
	return next
}

func overlapGlobClass(text string) overlapBytes {
	var bytes overlapBytes
	contents, negated := strings.CutPrefix(text, "!")
	for i := 0; i < len(contents); {
		low, next := globClassByte(contents, i)
		high := low
		if next+1 < len(contents) && contents[next] == '-' {
			high, next = globClassByte(contents, next+1)
		}
		for value := int(low); value <= int(high); value++ {
			bytes.add(byte(value))
		}
		i = next
	}
	if negated {
		for i := range bytes {
			bytes[i] = ^bytes[i]
		}
	}
	bytes[0] &^= 1
	return bytes
}

func (m *overlapMachine) regex(a mem.Allocator, text string, next int) int {
	compiled := compileRegex(a, text)
	if !compiled.Valid {
		return m.consume(overlapBytes{}, next)
	}
	p := compiled.Program
	defer p.Free(a)
	// Copy the portable regex program in two layers. The first layer cannot
	// accept until at least one byte was consumed, exactly like a capture.
	base := len(m.code)
	m.code = append(m.code, make([]overlapInstruction, len(p.Instructions)*2)...)
	for i, step := range p.Instructions {
		for layer := range 2 {
			origin := base + layer*len(p.Instructions)
			inst := overlapInstruction{}
			switch step.Op {
			case regexAccept:
				inst.kind, inst.next = overlapConsume, next
				if layer == 1 {
					inst.kind, inst.alt = overlapSplit, next
				}
			case regexSplit:
				inst.kind, inst.next, inst.alt = overlapSplit, origin+step.Out1, origin+step.Out2
			default:
				inst.kind, inst.next = overlapConsume, base+len(p.Instructions)+step.Out1
				for v := 1; v < 256; v++ {
					if regexInstructionMatches(p.Text, step, byte(v)) {
						inst.bytes.add(byte(v))
					}
				}
			}
			m.code[origin+i] = inst
		}
	}
	return base + p.Start
}

func makeOverlapMachine(a mem.Allocator, pattern *Pattern) overlapMachine {
	m := overlapMachine{code: []overlapInstruction{{kind: overlapAccept}}}
	type capture struct{ first, last, slot int }
	names := make(map[string]capture)
	for i, part := range pattern.Parts {
		if part.Kind == PatternMatcher && part.Text != "" {
			seen, known := names[part.Text]
			if !known {
				seen.first, seen.slot = i, -1
			} else if seen.slot < 0 {
				seen.slot = m.slots
				m.slots++
			}
			seen.last = i
			names[part.Text] = seen
		}
	}
	next := 0
	for i := len(pattern.Parts) - 1; i >= 0; i-- {
		part := pattern.Parts[i]
		switch part.Kind {
		case PatternLiteral:
			next = m.literal(part.Text, next)
		case PatternGlob:
			next = m.glob(part.Text, next, true)
		case PatternMatcher:
			capture, repeated := names[part.Text]
			repeated = repeated && capture.slot >= 0
			if repeated {
				next = m.add(overlapInstruction{kind: overlapLeave, slot: capture.slot, first: i == capture.first, last: i == capture.last, next: next})
			}
			begin := len(m.code)
			if part.Regex {
				next = m.regex(a, part.Pattern, next)
			} else {
				next = m.glob(part.Pattern, next, false)
			}
			if repeated {
				m.occurrences = append(m.occurrences, capture.slot)
				for i := begin - 1; i < len(m.code); i++ {
					m.code[i].occurrence = len(m.occurrences)
				}
				next = m.add(overlapInstruction{kind: overlapEnter, slot: capture.slot, first: i == capture.first, next: next, occurrence: len(m.occurrences)})
			}
		default:
			next = m.consume(overlapBytes{}, next)
		}
	}
	m.start = next
	return m
}

type overlapSide struct {
	pc, slot, offset int
	// Each saved capture ends in NUL, which never occurs in a pathname.
	saved, recording string
}

func (s overlapSide) capture() string {
	remaining := s.saved
	for i := 1; i < s.slot; i++ {
		remaining = remaining[strings.IndexByte(remaining, 0)+1:]
	}
	return remaining[:strings.IndexByte(remaining, 0)]
}

func (s overlapSide) replace(text string) string {
	start := 0
	for i := 1; i < s.slot; i++ {
		start += strings.IndexByte(s.saved[start:], 0) + 1
	}
	end := start + strings.IndexByte(s.saved[start:], 0)
	return s.saved[:start] + text + s.saved[end:]
}

func (s overlapSide) advance(inst overlapInstruction, budget *int) (overlapSide, bool) {
	s.pc = inst.next
	switch inst.kind {
	case overlapEnter:
		s.slot = inst.slot + 1
		if inst.first {
			s.offset = -1
		}
	case overlapLeave:
		if inst.first {
			if len(s.saved)+len(s.recording) > *budget {
				*budget = -1
				return s, false
			}
			*budget -= len(s.saved) + len(s.recording)
			s.saved = s.replace(s.recording)
		} else if len(s.capture()) != s.offset {
			return s, false
		} else if inst.last {
			s.saved = s.replace("")
		}
		s.slot, s.offset, s.recording = 0, 0, ""
	}
	return s, true
}

func (s overlapSide) consume(inst overlapInstruction, value byte, budget *int) (overlapSide, bool) {
	s.pc = inst.next
	if s.slot == 0 {
		return s, true
	}
	if s.offset < 0 {
		if len(s.recording)+1 > *budget {
			*budget = -1
			return s, false
		}
		*budget -= len(s.recording) + 1
		s.recording += string([]byte{value})
		return s, true
	}
	previous := s.capture()
	if s.offset == len(previous) || previous[s.offset] != value {
		return s, false
	}
	s.offset++
	return s, true
}

// Use one byte per equivalence class of all transition alphabets. Replacing
// bytes with their representatives preserves membership and capture equality.
func overlapAlphabet(left, right overlapMachine) []byte {
	var classes [256]int
	classes['.'], classes['/'] = 1, 2
	for _, machine := range []overlapMachine{left, right} {
		var known = make(map[overlapBytes]bool)
		for _, instruction := range machine.code {
			bytes := instruction.bytes
			if instruction.kind != overlapConsume || known[bytes] {
				continue
			}
			known[bytes] = true
			var refined [512]int
			count := 0
			for i := 1; i < 256; i++ {
				key := classes[i] * 2
				if bytes.has(byte(i)) {
					key++
				}
				if refined[key] == 0 {
					count++
					refined[key] = count
				}
				classes[i] = refined[key] - 1
			}
		}
	}
	var used [256]bool
	var alphabet []byte
	// Prefer ordinary letters and digits for wildcard witnesses.
	for i := 0; i < 256; i++ {
		v := byte(i + 'a')
		if v != 0 && !used[classes[v]] {
			used[classes[v]] = true
			alphabet = append(alphabet, v)
		}
	}
	return alphabet
}

type overlapPath struct {
	start, parents bool
	component      byte // 0 empty, 1 dot, 2 dot-dot, 3 ordinary
}

func (p overlapPath) step(value byte) (overlapPath, bool) {
	if value == '/' {
		if p.start || p.component == 3 || p.component == 2 && p.parents {
			p.parents = !p.start && p.component == 2
			p.start, p.component = false, 0
			return p, true
		}
		return p, false
	}
	p.start = false
	if value == '.' && p.component < 2 {
		p.component++
	} else {
		p.component = 3
	}
	return p, true
}

type overlapPair struct {
	left, right overlapSide
	path        overlapPath
}

type overlapEdge struct {
	node int
	byte byte // Zero denotes a transition that consumes no pathname byte.
}

type overlapProjection struct {
	predecessors [][]overlapEdge
	accepting    []int
	viable       map[overlapPair]bool
	forward      [][]overlapEdge
	left, right  overlapCaptureLanguage
}

func (p *overlapProjection) restrict(queue []overlapPair, left, right overlapMachine) {
	p.viable = make(map[overlapPair]bool)
	p.forward = make([][]overlapEdge, len(queue))
	p.left = makeOverlapLanguage(len(queue), left)
	p.right = makeOverlapLanguage(len(queue), right)
	for i := 0; i < len(p.accepting); i++ {
		index := p.accepting[i]
		if p.viable[queue[index]] {
			continue
		}
		p.viable[queue[index]] = true
		p.left.add(index, left.code[queue[index].left.pc])
		p.right.add(index, right.code[queue[index].right.pc])
		for _, edge := range p.predecessors[index] {
			p.accepting = append(p.accepting, edge.node)
			p.forward[edge.node] = append(p.forward[edge.node], overlapEdge{index, edge.byte})
		}
	}
	p.predecessors, p.accepting = nil, nil
}

func (s overlapPair) withoutCaptures() overlapPair {
	s.left, s.right = overlapSide{pc: s.left.pc}, overlapSide{pc: s.right.pc}
	return s
}

func searchOverlap(left, right overlapMachine, alphabet []byte, projection *overlapProjection, captures bool, budget *int) (bool, bool) {
	initial := overlapPair{left: overlapSide{pc: left.start}, right: overlapSide{pc: right.start}, path: overlapPath{start: true, parents: true}}
	if captures {
		initial.left.saved = strings.Repeat("\x00", left.slots)
		initial.right.saved = strings.Repeat("\x00", right.slots)
	}
	visited := map[overlapPair]int{initial: 0}
	queue := []overlapPair{initial}
	if projection != nil && !captures {
		projection.predecessors = append(projection.predecessors, nil)
	}
	add := func(from int, value byte, state overlapPair) {
		if captures && !projection.viable[state.withoutCaptures()] {
			return
		}
		index, known := visited[state]
		if !known {
			index = len(queue)
			visited[state] = index
			queue = append(queue, state)
			if projection != nil && !captures {
				projection.predecessors = append(projection.predecessors, nil)
			}
		}
		if projection != nil && !captures {
			projection.predecessors[index] = append(projection.predecessors[index], overlapEdge{from, value})
		}
	}
	for index := 0; index < len(queue); index++ {
		if !ChargeMatchStep(budget) {
			return false, true
		}
		state := queue[index]
		l, r := left.code[state.left.pc], right.code[state.right.pc]
		if l.kind == overlapAccept && r.kind == overlapAccept && state.path.component == 3 {
			if projection == nil || captures {
				return true, false
			}
			projection.accepting = append(projection.accepting, index)
			continue
		}
		if l.kind >= overlapSplit || r.kind >= overlapSplit {
			side, inst := &state.left, l
			leftSide := true
			if l.kind < overlapSplit {
				side, inst = &state.right, r
				leftSide = false
			}
			if inst.kind == overlapSplit && inst.alt != inst.next {
				side.pc = inst.alt
				add(index, 0, state)
			}
			if captures {
				if inst.kind == overlapLeave && inst.first && !projection.allows(leftSide, *side, true, budget) {
					continue
				}
				next, ok := side.advance(inst, budget)
				if !ok {
					continue
				}
				*side = next
			} else {
				side.pc = inst.next
			}
			add(index, 0, state)
			continue
		}
		if l.kind != overlapConsume || r.kind != overlapConsume {
			continue
		}
		for _, value := range alphabet {
			if !ChargeMatchStep(budget) {
				return false, true
			}
			if !l.bytes.has(value) || !r.bytes.has(value) {
				continue
			}
			next := state
			var ok bool
			if next.path, ok = state.path.step(value); !ok {
				continue
			}
			if next.left, ok = state.left.consume(l, value, budget); !ok {
				continue
			}
			if next.right, ok = state.right.consume(r, value, budget); !ok {
				continue
			}
			if captures && (!projection.allows(true, next.left, false, budget) || !projection.allows(false, next.right, false, budget)) {
				continue
			}
			add(index, value, next)
		}
	}
	if projection != nil && !captures && len(projection.accepting) != 0 {
		projection.restrict(queue, left, right)
		return true, false
	}
	return false, *budget < 0
}

// PathPatternsOverlap decides whether two normalized path patterns share an
// artifact name. When discovery is nil, scan is the ordinary filesystem glob.
// It checks capture equality as part of the search and reports budget exhaustion
// independently from proven disjointness.
func PathPatternsOverlap(a mem.Allocator, discovery, output *Pattern, scan string, budget *int) (bool, bool) {
	leftPrefix, rightPrefix := overlapPrefix(discovery, scan), overlapPrefix(output, "")
	if !strings.HasPrefix(leftPrefix, rightPrefix) && !strings.HasPrefix(rightPrefix, leftPrefix) {
		return false, false
	}
	var left overlapMachine
	if discovery == nil {
		left = makeHostGlobMachine(scan)
	} else {
		left = makeOverlapMachine(a, discovery)
	}
	right := makeOverlapMachine(a, output)
	alphabet := overlapAlphabet(left, right)
	if left.slots+right.slots == 0 {
		return searchOverlap(left, right, alphabet, nil, false, budget)
	}
	projection := &overlapProjection{}
	matched, limited := searchOverlap(left, right, alphabet, projection, false, budget)
	if !matched {
		return matched, limited
	}
	seedLimit := *budget / 4
	seedBudget := seedLimit
	if projection.left.seed(projection.forward, &seedBudget) && projection.right.seed(projection.forward, &seedBudget) {
		if seeded, _ := searchOverlap(left, right, alphabet, projection, true, &seedBudget); seeded {
			*budget -= seedLimit - max(seedBudget, 0)
			return true, false
		}
	}
	*budget -= seedLimit - max(seedBudget, 0)
	projection.left.seeds, projection.right.seeds = nil, nil
	return searchOverlap(left, right, alphabet, projection, true, budget)
}

func overlapPrefix(pattern *Pattern, scan string) string {
	var prefix strings.Builder
	defer prefix.Reset()
	if pattern != nil {
		for _, part := range pattern.Parts {
			if part.Kind != PatternLiteral {
				break
			}
			prefix.WriteString(part.Text)
		}
		return prefix.String()
	}
	for i := 0; i < len(scan); i++ {
		if strings.IndexByte("*?[", scan[i]) >= 0 {
			break
		}
		if scan[i] == '\\' && i+1 < len(scan) {
			i++
		}
		prefix.WriteByte(scan[i])
	}
	return prefix.String()
}
