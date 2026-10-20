package program

import (
	"kame/core"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

type listedTarget struct {
	Name   string
	Score  int
	Rule   *rule.Rule
	Output int
	Status string
}

func compareListedTargets(left any, right any) int {
	a, b := left.(*listedTarget), right.(*listedTarget)
	if a.Score != b.Score {
		return a.Score - b.Score
	}
	return strings.Compare(a.Name, b.Name)
}

// ListTargets returns owned, unique declared rule outputs. It never selects a
// default, plans prerequisites, expands captures, or materializes a target.
// Empty queries list everything; other queries rank exact, partial and typo hits.
func (p *Program) ListTargets(query string) []string {
	if p == nil {
		return nil
	}
	candidates := p.listedTargets(query)
	defer slices.Free(p.Alloc, candidates)
	var names []string
	for i := range candidates {
		names = slices.Append(p.Alloc, names, cloneText(p.Alloc, candidates[i].Name))
	}
	return names
}

func (p *Program) listedTargets(query string) []listedTarget {
	var candidates []listedTarget
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if r == nil || r == p.automaticCleanRule {
			continue
		}
		for j := range r.Outputs {
			name := r.Outputs[j].Text
			score := targetMatchScore(p.Alloc, name, query)
			if score >= 0 {
				candidates = slices.Append(p.Alloc, candidates, listedTarget{Name: name, Score: score, Rule: r, Output: j})
			}
		}
	}
	slices.SortFunc(candidates, compareListedTargets)
	count := 0
	for i := range candidates {
		if count == 0 || candidates[i].Name != candidates[count-1].Name {
			candidates[count] = candidates[i]
			count++
		}
	}
	return candidates[:count]
}

func listedTargetGroup(item listedTarget) string {
	if item.Rule.Outputs[item.Output].Template && item.Rule.Kind != rule.FileRule {
		return "rules"
	}
	if item.Rule.Kind == rule.ServiceRule {
		return "services"
	}
	if item.Rule.Kind == rule.CachedTaskRule {
		return "tasks"
	}
	if item.Rule.Kind == rule.FileRule {
		return "artifacts"
	}
	return "rules"
}

// WriteTargetReport enriches the catalogue with read-only accepted-record checks.
// Unlike Plan, it never evaluates prerequisite expressions or selects a default.
// Forwarding hosts retry after servicing requests when InspectionWaiting is set.
func (p *Program) WriteTargetReport(out io.Writer, query string) {
	p.InspectionWaiting = false
	defer p.finishInspection()
	items := p.listedTargets(query)
	defer slices.Free(p.Alloc, items)
	for i := range items {
		if items[i].Rule.Outputs[items[i].Output].Template {
			continue
		}
		group := listedTargetGroup(items[i])
		if group != "artifacts" && group != "tasks" {
			continue
		}
		items[i].Status = "unknown"
		r := items[i].Rule
		unbound := false
		for j := range r.Arguments {
			if !r.Arguments[j].Optional {
				unbound = true
			}
		}
		for j := range r.Outputs {
			if r.Outputs[j].Template {
				unbound = true
			}
		}
		if unbound {
			continue
		}
		plan := p.listedTargetPlan(r, r.Outputs[0].Text, nil)
		result := p.inspectTargetListStatus(plan, nil)
		plan.Free(p.Alloc)
		if result.Waiting {
			p.InspectionWaiting = true
			return
		}
		if result.State != nil {
			items[i].Status = result.State.Statuses[items[i].Output]
		}
	}
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str("list")
	e.Str("entries")
	e.BeginArray()
	for i := range items {
		e.BeginObject()
		e.Str("name")
		e.Str(items[i].Name)
		e.Str("group")
		e.Str(listedTargetGroup(items[i]))
		if summary := listedTargetSummary(items[i].Rule); summary != "" {
			e.Str("summary")
			e.Str(summary)
		}
		e.Str("status")
		e.Str(items[i].Status)
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
	e.Flush()
}

func listedTargetSummary(r *rule.Rule) string {
	if r == nil || r.Metadata == nil {
		return ""
	}
	for i := range r.Metadata.Fields {
		field := r.Metadata.Fields[i]
		if field.Key != "summary" || field.Value == nil || field.Value.Kind != expr.String || len(field.Value.Parts) > 1 {
			continue
		}
		if len(field.Value.Parts) == 0 {
			return ""
		}
		if field.Value.Parts[0].Expr == nil {
			return field.Value.Parts[0].Text
		}
	}
	return ""
}

func (p *Program) listedTargetPlan(r *rule.Rule, target string, captures []template.CaptureValue) Plan {
	plan := Plan{Rule: r, Target: cloneText(p.Alloc, target), Captures: cloneCaptures(p.Alloc, captures), Freshness: Unknown}
	for i := range r.Outputs {
		plan.Outputs = slices.Append(p.Alloc, plan.Outputs, renderTarget(p.Alloc, r.Outputs[i], captures))
	}
	for i := range r.Arguments {
		plan.Arguments = slices.Append(p.Alloc, plan.Arguments, ArgumentValue{Name: cloneText(p.Alloc, r.Arguments[i].Name), Value: cloneText(p.Alloc, r.Arguments[i].Default)})
	}
	name := p.canonicalTarget(plan.Target, r.Kind == rule.FileRule)
	if r.Kind != rule.FileRule {
		mem.FreeString(p.Alloc, name)
		name = targetArgumentKey(p.Alloc, plan.Target, plan.Arguments)
	}
	plan.Key = core.NewResourceKey(p.Alloc, resourceKind(r), name)
	mem.FreeString(p.Alloc, name)
	for i := range r.Inputs {
		input := r.Inputs[i]
		if input.Kind != rule.InputName && input.Kind != rule.InputPath && input.Kind != rule.InputTemplate {
			continue
		}
		text := cloneText(p.Alloc, input.Text)
		if input.Kind == rule.InputTemplate {
			mem.FreeString(p.Alloc, text)
			text = renderInput(p.Alloc, input, captures)
		}
		plan.Inputs = slices.Append(p.Alloc, plan.Inputs, text)
		kind := core.ResourceTarget
		if input.Kind == rule.InputPath {
			kind = core.ResourceFile
		}
		plan.ResourceInputs = slices.Append(p.Alloc, plan.ResourceInputs, PlanInput{Display: cloneText(p.Alloc, text), Key: core.NewResourceKey(p.Alloc, kind, text), OrderOnly: input.OrderOnly})
	}
	return plan
}

// A matching consumer record cannot establish reuse while a consumed artifact's
// producer still needs work. Follow only recorded resources, never input code.
func (p *Program) inspectTargetListStatus(plan Plan, ancestors []string) inspectionStatusResult {
	result := p.inspectTargetStatus(plan)
	if result.Waiting || result.State == nil || !slices.Contains(result.State.Statuses, "built") {
		return result
	}
	seen := slices.Clone(p.Alloc, ancestors)
	seen = slices.Append(p.Alloc, seen, plan.Key.Name)
	defer slices.Free(p.Alloc, seen)
	ready := true
	for i := range result.State.Stored.Inputs {
		item := result.State.Stored.Inputs[i]
		if item.Key.Kind != core.ResourceFile {
			continue
		}
		request := p.relativePath(item.Key.Name)
		selected := p.selectRule(request)
		mem.FreeString(p.Alloc, request)
		if selected.Diagnostic.Code != "" || selected.Ambiguous {
			ready = false
		}
		if selected.Rule != nil && selected.Diagnostic.Code == "" && !selected.Ambiguous {
			if slices.Contains(seen, item.Key.Name) {
				ready = false
			} else {
				dependency := p.listedTargetPlan(selected.Rule, item.Key.Name, selected.Captures)
				status := p.inspectTargetListStatus(dependency, seen)
				dependency.Free(p.Alloc)
				if status.Waiting {
					result.Waiting = true
				} else if status.State == nil {
					ready = false
				} else {
					for j := range status.State.Statuses {
						if status.State.Statuses[j] != "built" {
							ready = false
						}
					}
				}
			}
		}
		freeCaptures(p.Alloc, selected.Captures)
		freeArguments(p.Alloc, selected.Arguments)
		mem.FreeString(p.Alloc, selected.Target)
		selected.Diagnostic.Free(p.Alloc)
		if result.Waiting {
			return result
		}
	}
	if !ready {
		for i := range result.State.Statuses {
			if result.State.Statuses[i] == "built" {
				result.State.Statuses[i] = "unknown"
			}
		}
	}
	return result
}

// WriteTargetList writes the same catalogue used by both CLI hosts as JSON.
func (p *Program) WriteTargetList(out io.Writer, query string) {
	names := p.ListTargets(query)
	defer FreeStrings(p.Alloc, names)
	e := json.NewEncoder(out)
	e.BeginArray()
	for i := range names {
		e.Str(names[i])
	}
	e.EndArray()
	e.Flush()
}

func targetMatchScore(a mem.Allocator, name string, query string) int {
	if query == "" || name == query {
		return 0
	}
	name, query = strings.TrimPrefix(name, "./"), strings.TrimPrefix(query, "./")
	if name == query {
		return 0
	}
	if strings.HasPrefix(name, query) {
		return 1
	}
	if strings.Contains(name, query) {
		return 2
	}
	limit := 1
	if utf8.RuneCountInString(query) >= 5 {
		limit = 2
	}
	distance := targetEditDistance(a, query, name, limit)
	base := path.Base(name)
	if base != name {
		distance = min(distance, targetEditDistance(a, query, base, limit))
	}
	if distance <= limit {
		return 2 + distance
	}
	return -1
}

// Banded optimal-string-alignment distance: insert/delete/replace and adjacent
// swaps, on Unicode code points. Only distances within limit are relevant.
func targetEditDistance(a mem.Allocator, left string, right string, limit int) int {
	leftLen, rightLen := utf8.RuneCountInString(left), utf8.RuneCountInString(right)
	if leftLen-rightLen > limit || rightLen-leftLen > limit {
		return limit + 1
	}
	runes := slices.Make[rune](a, rightLen)
	defer slices.Free(a, runes)
	index := 0
	for _, r := range right {
		runes[index] = r
		index++
	}
	width := rightLen + 1
	cells := slices.Make[int](a, 3*width)
	defer slices.Free(a, cells)
	for i := range cells {
		cells[i] = limit + 1
	}
	older, previous, current := cells[:width], cells[width:2*width], cells[2*width:]
	for j := 0; j <= rightLen; j++ {
		previous[j] = min(j, limit+1)
	}
	i := 0
	var last rune
	for _, r := range left {
		i++
		current[0] = i
		start, end := max(1, i-limit), min(rightLen, i+limit)
		if start > 1 {
			current[start-1] = limit + 1
		}
		if end < rightLen {
			current[end+1] = limit + 1
		}
		best := current[0]
		for j := start; j <= end; j++ {
			cost := 1
			if r == runes[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
			if i > 1 && j > 1 && r == runes[j-2] && last == runes[j-1] {
				current[j] = min(current[j], older[j-2]+1)
			}
			best = min(best, current[j])
		}
		if best > limit {
			return limit + 1
		}
		temporary := older
		older, previous, current = previous, current, temporary
		last = r
	}
	return previous[rightLen]
}
