package program

import (
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

type listedTarget struct {
	Name string
	Score int
}

func compareListedTargets(left any, right any) int {
	a, b := left.(*listedTarget), right.(*listedTarget)
	if a.Score != b.Score { return a.Score - b.Score }
	return strings.Compare(a.Name, b.Name)
}

// ListTargets returns owned, unique declared rule outputs. It never selects a
// default, plans prerequisites, expands captures, or materializes a target.
// Empty queries list everything; other queries rank exact, partial and typo hits.
func (p *Program) ListTargets(query string) []string {
	if p == nil { return nil }
	var candidates []listedTarget
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if r == nil || r == p.automaticCleanRule { continue }
		for j := range r.Outputs {
			name := r.Outputs[j].Text
			score := targetMatchScore(p.Alloc, name, query)
			if score >= 0 { candidates = slices.Append(p.Alloc, candidates, listedTarget{Name: name, Score: score}) }
		}
	}
	slices.SortFunc(candidates, compareListedTargets)
	var names []string
	for i := range candidates {
		if i == 0 || candidates[i].Name != candidates[i-1].Name {
			names = slices.Append(p.Alloc, names, cloneText(p.Alloc, candidates[i].Name))
		}
	}
	slices.Free(p.Alloc, candidates)
	return names
}

// WriteTargetList writes the same catalogue used by both CLI hosts as JSON.
func (p *Program) WriteTargetList(out io.Writer, query string) {
	names := p.ListTargets(query)
	defer FreeStrings(p.Alloc, names)
	e := json.NewEncoder(out)
	e.BeginArray()
	for i := range names { e.Str(names[i]) }
	e.EndArray()
	e.Flush()
}

func targetMatchScore(a mem.Allocator, name string, query string) int {
	if query == "" || name == query { return 0 }
	name, query = strings.TrimPrefix(name, "./"), strings.TrimPrefix(query, "./")
	if name == query { return 0 }
	if strings.HasPrefix(name, query) { return 1 }
	if strings.Contains(name, query) { return 2 }
	limit := 1
	if utf8.RuneCountInString(query) >= 5 { limit = 2 }
	distance := targetEditDistance(a, query, name, limit)
	base := path.Base(name)
	if base != name { distance = min(distance, targetEditDistance(a, query, base, limit)) }
	if distance <= limit { return 2 + distance }
	return -1
}

// Banded optimal-string-alignment distance: insert/delete/replace and adjacent
// swaps, on Unicode code points. Only distances within limit are relevant.
func targetEditDistance(a mem.Allocator, left string, right string, limit int) int {
	leftLen, rightLen := utf8.RuneCountInString(left), utf8.RuneCountInString(right)
	if leftLen-rightLen > limit || rightLen-leftLen > limit { return limit+1 }
	runes := slices.Make[rune](a, rightLen)
	defer slices.Free(a, runes)
	index := 0
	for _, r := range right { runes[index] = r; index++ }
	width := rightLen+1
	cells := slices.Make[int](a, 3*width)
	defer slices.Free(a, cells)
	for i := range cells { cells[i] = limit+1 }
	older, previous, current := cells[:width], cells[width:2*width], cells[2*width:]
	for j := 0; j <= rightLen; j++ { previous[j] = min(j, limit+1) }
	i := 0
	var last rune
	for _, r := range left {
		i++
		current[0] = i
		start, end := max(1, i-limit), min(rightLen, i+limit)
		if start > 1 { current[start-1] = limit+1 }
		if end < rightLen { current[end+1] = limit+1 }
		best := current[0]
		for j := start; j <= end; j++ {
			cost := 1
			if r == runes[j-1] { cost = 0 }
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			if i > 1 && j > 1 && r == runes[j-2] && last == runes[j-1] { current[j] = min(current[j], older[j-2]+1) }
			best = min(best, current[j])
		}
		if best > limit { return limit+1 }
		temporary := older
		older, previous, current = previous, current, temporary
		last = r
	}
	return previous[rightLen]
}
