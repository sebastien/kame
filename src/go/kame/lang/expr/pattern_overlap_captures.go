package expr

import "strings"

type overlapCaptureState struct {
	occurrence int
	exit       bool
}

type overlapCaptureMatch struct {
	prefix, whole bool
}

// The regular product bounds each repeated capture occurrence independently.
// Every eventual capture value must be a word in each of its occurrence
// languages. Checking prefixes prevents blind enumeration of saved captures.
type overlapCaptureLanguage struct {
	states      []overlapCaptureState
	entry       [][]int
	occurrences [][]int
	memo        []map[string]overlapCaptureMatch
	seeds       []string
}

func makeOverlapLanguage(size int, machine overlapMachine) overlapCaptureLanguage {
	if machine.slots == 0 {
		return overlapCaptureLanguage{}
	}
	language := overlapCaptureLanguage{
		states: make([]overlapCaptureState, size), entry: make([][]int, len(machine.occurrences)+1),
		occurrences: make([][]int, machine.slots), memo: make([]map[string]overlapCaptureMatch, machine.slots),
	}
	for i, slot := range machine.occurrences {
		language.occurrences[slot] = append(language.occurrences[slot], i+1)
	}
	for i := range language.memo {
		language.memo[i] = make(map[string]overlapCaptureMatch)
	}
	return language
}

func (l *overlapCaptureLanguage) add(index int, inst overlapInstruction) {
	if inst.occurrence != 0 {
		l.states[index] = overlapCaptureState{occurrence: inst.occurrence, exit: inst.kind == overlapLeave}
		if inst.kind == overlapEnter {
			l.entry[inst.occurrence] = append(l.entry[inst.occurrence], index)
		}
	}
}

func (p *overlapProjection) allows(left bool, side overlapSide, complete bool, budget *int) bool {
	if side.offset >= 0 {
		return true
	}
	language := &p.right
	if left {
		language = &p.left
	}
	slot := side.slot - 1
	if len(language.seeds) != 0 {
		seed := language.seeds[slot]
		if complete {
			return seed == side.recording
		}
		return strings.HasPrefix(seed, side.recording)
	}
	result, known := language.memo[slot][side.recording]
	if !known {
		result = overlapCaptureMatch{prefix: true, whole: true}
		for _, occurrence := range language.occurrences[slot] {
			match := language.match(p.forward, occurrence, side.recording, budget)
			result.prefix = result.prefix && match.prefix
			result.whole = result.whole && match.whole
			if !result.prefix {
				break
			}
		}
		language.memo[slot][side.recording] = result
	}
	if complete {
		return result.whole
	}
	return result.prefix
}

// Try words from the individual occurrence languages before exploring arbitrary
// capture strings. This often supplies a fixed suffix from a rule or discovery.
// A failed seed does not exclude other words from the full search.
func (l *overlapCaptureLanguage) seed(edges [][]overlapEdge, budget *int) bool {
	seeds := make([]string, len(l.occurrences))
	for slot, occurrences := range l.occurrences {
		for _, occurrence := range occurrences {
			candidate := l.witness(edges, occurrence, budget)
			if candidate == "" {
				continue
			}
			common := true
			for _, other := range occurrences {
				if other != occurrence && !l.match(edges, other, candidate, budget).whole {
					common = false
					break
				}
			}
			if common {
				seeds[slot] = candidate
				break
			}
		}
		if seeds[slot] == "" {
			return false
		}
	}
	l.seeds = seeds
	return true
}

func (l *overlapCaptureLanguage) witness(edges [][]overlapEdge, occurrence int, budget *int) string {
	type step struct {
		node, previous int
		byte           byte
	}
	var queue []step
	seen := make(map[int]bool)
	add := func(node, previous int, value byte) {
		if !seen[node] {
			seen[node] = true
			queue = append(queue, step{node, previous, value})
		}
	}
	for _, node := range l.entry[occurrence] {
		add(node, -1, 0)
	}
	for i := 0; i < len(queue); i++ {
		if !ChargeMatchStep(budget) {
			return ""
		}
		state := queue[i]
		if l.states[state.node].exit {
			var reversed []byte
			for index := i; index >= 0; index = queue[index].previous {
				if value := queue[index].byte; value != 0 {
					reversed = append(reversed, value)
				}
			}
			for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
				reversed[i], reversed[j] = reversed[j], reversed[i]
			}
			return string(reversed)
		}
		for _, edge := range edges[state.node] {
			if !ChargeMatchStep(budget) {
				return ""
			}
			if l.states[edge.node].occurrence == occurrence {
				add(edge.node, i, edge.byte)
			}
		}
	}
	return ""
}

func (l *overlapCaptureLanguage) match(edges [][]overlapEdge, occurrence int, text string, budget *int) overlapCaptureMatch {
	queue := append([]int(nil), l.entry[occurrence]...)
	next := []int(nil)
	seen := make(map[int]int)
	for offset := 0; offset <= len(text) && len(queue) != 0; offset++ {
		for i := 0; i < len(queue); i++ {
			if !ChargeMatchStep(budget) {
				return overlapCaptureMatch{}
			}
			state := queue[i]
			if seen[state] == offset+1 {
				continue
			}
			seen[state] = offset + 1
			if offset == len(text) && l.states[state].exit {
				return overlapCaptureMatch{prefix: true, whole: true}
			}
			for _, edge := range edges[state] {
				if !ChargeMatchStep(budget) {
					return overlapCaptureMatch{}
				}
				if l.states[edge.node].occurrence != occurrence {
					continue
				}
				if edge.byte == 0 {
					queue = append(queue, edge.node)
				} else if offset < len(text) && edge.byte == text[offset] {
					next = append(next, edge.node)
				}
			}
		}
		if offset == len(text) {
			return overlapCaptureMatch{prefix: true}
		}
		queue, next = next, queue[:0]
	}
	return overlapCaptureMatch{}
}
