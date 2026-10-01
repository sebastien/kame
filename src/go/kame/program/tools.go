package program

import (
	"kame/diagnostic"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// ToolUse retains the authored reference and the dependency path that reaches it.
type ToolUse struct {
	Name        string
	Source      string
	Span        diagnostic.Span
	Target      string
	TargetStack []string
}

type ToolsResult struct {
	Uses       []ToolUse
	Diagnostic diagnostic.Diagnostic
	Waiting    bool
}

func (r *ToolsResult) Free(a mem.Allocator) {
	for i := range r.Uses {
		mem.FreeString(a, r.Uses[i].Name)
		mem.FreeString(a, r.Uses[i].Source)
		mem.FreeString(a, r.Uses[i].Target)
		freeStrings(a, r.Uses[i].TargetStack)
	}
	slices.Free(a, r.Uses)
	r.Diagnostic.Free(a)
	*r = ToolsResult{}
}

// RequiredTools follows selected plans, resolving read-only dynamic inputs but
// never rendering or executing a recipe. Each name is reported once, at its
// first reached authored reference.
// Forwarding hosts may receive Waiting: service NextOutbound, complete it, then
// retry the check. The pending inspection root remains owned by the Program.
// ponytail: replay traversal after host yields; keep a traversal stack if large
// plans make replay costly. Completed input resolvers are reused, not rerun.
func (p *Program) RequiredTools(target string) ToolsResult {
	result := ToolsResult{}
	var visited []string
	var stack []string
	result.Diagnostic = p.collectTools(target, &visited, &stack, &result)
	freeStrings(p.Alloc, visited)
	slices.Free(p.Alloc, stack)
	return result
}

func (p *Program) collectTools(target string, visited *[]string, stack *[]string, result *ToolsResult) diagnostic.Diagnostic {
	if slices.Contains(*stack, target) {
		d := failure(p.Alloc, "DEP_CYCLE", "dependency cycle while checking tools: "+target)
		d.Target = cloneText(p.Alloc, target)
		d.TargetStack = cloneStrings(p.Alloc, *stack)
		d.TargetStack = slices.Append(p.Alloc, d.TargetStack, cloneText(p.Alloc, target))
		return d
	}
	if slices.Contains(*visited, target) {
		return diagnostic.Diagnostic{}
	}
	*stack = slices.Append(p.Alloc, *stack, target)
	planned := p.expandPlan(target, p.Forwarding)
	if planned.Waiting {
		result.Waiting = true
		*stack = (*stack)[:len(*stack)-1]
		return diagnostic.Diagnostic{}
	}
	if planned.Diagnostic.Code != "" {
		if planned.Diagnostic.Code == "TGT_NO_RULE" && len(*stack) > 1 && isFileName(target) {
			planned.Diagnostic.Free(p.Alloc)
			*stack = (*stack)[:len(*stack)-1]
			return diagnostic.Diagnostic{}
		}
		d := planned.Diagnostic
		d.Target, d.TargetStack = cloneText(p.Alloc, target), cloneStrings(p.Alloc, *stack)
		*stack = (*stack)[:len(*stack)-1]
		return d
	}
	plan := planned.Plan
	defer plan.Free(p.Alloc)
	if plan.Rule != nil {
		for i := range plan.Rule.Body {
			value := plan.Rule.Body[i].Template
			if value == nil {
				continue
			}
			for j := range value.Parts {
				part := value.Parts[j]
				if part.Kind != template.Tool || hasToolUse(result.Uses, part.Text) {
					continue
				}
				location := p.Eval.LocateSource(p.Parsed.Source.Name, part.Span)
				use := ToolUse{Name: cloneText(p.Alloc, part.Text), Source: cloneText(p.Alloc, location.Source), Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}, Target: cloneText(p.Alloc, target), TargetStack: cloneStrings(p.Alloc, *stack)}
				result.Uses = slices.Append(p.Alloc, result.Uses, use)
			}
		}
	}
	for i := range plan.StaticInputs {
		if d := p.collectTools(plan.StaticInputs[i], visited, stack, result); d.Code != "" || result.Waiting {
			*stack = (*stack)[:len(*stack)-1]
			return d
		}
	}
	for i := range plan.DynamicInputs {
		if d := p.collectTools(plan.DynamicInputs[i], visited, stack, result); d.Code != "" || result.Waiting {
			*stack = (*stack)[:len(*stack)-1]
			return d
		}
	}
	*stack = (*stack)[:len(*stack)-1]
	*visited = slices.Append(p.Alloc, *visited, cloneText(p.Alloc, target))
	return diagnostic.Diagnostic{}
}

func hasToolUse(uses []ToolUse, name string) bool {
	for i := range uses {
		if uses[i].Name == name {
			return true
		}
	}
	return false
}
