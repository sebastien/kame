package program

import (
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"
	"slices"
	"strings"
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
	mem.FreeSlice(nil, r.Uses)
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
	mem.FreeSlice(nil, stack)
	return result
}

func (p *Program) collectTools(target string, visited *[]string, stack *[]string, result *ToolsResult) diagnostic.Diagnostic {
	if slices.Contains(*stack, target) {
		d := failure(p.Alloc, "DEP_CYCLE", "dependency cycle while checking tools: "+target)
		d.Target = cloneText(p.Alloc, target)
		d.TargetStack = cloneStrings(p.Alloc, *stack)
		d.TargetStack = append(d.TargetStack, cloneText(p.Alloc, target))
		return d
	}
	if slices.Contains(*visited, target) {
		return diagnostic.Diagnostic{}
	}
	*stack = append(*stack, target)
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
				result.Uses = append(result.Uses, use)
			}
		}
		globalKash := false
		for i := range p.Parsed.Items {
			definition := p.Parsed.Items[i].Definition
			if definition != nil && definition.Name == "SHELL" && definition.Expression != nil {
				globalKash = definition.Expression.Kind == expr.Name && definition.Expression.Text == "kash"
			}
		}
		if ruleSelectsShell(plan.Rule, globalKash, "kash") {
			p.collectKashTools(plan.Rule, target, *stack, result)
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
	*visited = append(*visited, cloneText(p.Alloc, target))
	return diagnostic.Diagnostic{}
}

// Kash recipes are inspected as authored, never evaluated or executed.
func (p *Program) collectKashTools(r *rule.Rule, target string, stack []string, result *ToolsResult) {
	if len(r.Body) == 0 {
		return
	}
	b := strings.Builder{}
	for i := range r.Body {
		if i != 0 {
			b.WriteByte('\n')
		}
		b.WriteString(r.Body[i].Text)
	}
	parsed := script.ParseKash(p.Alloc, "<recipe>", b.String())
	for i := range parsed.Items {
		item := parsed.Items[i]
		if result == nil {
			p.declareExpressionTools(item.Expression)
			if item.Definition != nil {
				p.declareExpressionTools(item.Definition.Expression)
			}
		} else {
			p.collectKashExpressionTools(item.Expression, r, target, stack, result)
			if item.Definition != nil {
				p.collectKashExpressionTools(item.Definition.Expression, r, target, stack, result)
			}
		}
	}
	parsed.Free()
	b.Reset()
}

func (p *Program) collectKashExpressionTools(value *expr.Expr, r *rule.Rule, target string, stack []string, result *ToolsResult) {
	if value == nil {
		return
	}
	if value.Kind == expr.CommandStage {
		for i := range value.Items {
			word := value.Items[i]
			if word.Kind != expr.CommandWord {
				continue
			}
			if len(word.Parts) == 1 && word.Parts[0].Expr == nil && !hasToolUse(result.Uses, word.Parts[0].Text) {
				start, end := kashToolOffset(r, word.Span.Start), kashToolOffset(r, word.Span.End)
				location := p.Eval.LocateSource(p.Parsed.Source.Name, source.Span{Start: start, End: end})
				result.Uses = append(result.Uses, ToolUse{Name: cloneText(p.Alloc, word.Parts[0].Text), Source: cloneText(p.Alloc, location.Source), Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}, Target: cloneText(p.Alloc, target), TargetStack: cloneStrings(p.Alloc, stack)})
			}
			break
		}
	}
	for i := range value.Items {
		p.collectKashExpressionTools(value.Items[i], r, target, stack, result)
	}
	for i := range value.Body {
		p.collectKashExpressionTools(value.Body[i], r, target, stack, result)
	}
	for i := range value.Fields {
		p.collectKashExpressionTools(value.Fields[i].Value, r, target, stack, result)
	}
	for i := range value.Parts {
		p.collectKashExpressionTools(value.Parts[i].Expr, r, target, stack, result)
	}
}

func kashToolOffset(r *rule.Rule, offset int) int {
	for i := range r.Body {
		line := r.Body[i]
		if offset <= len(line.Text) || i == len(r.Body)-1 {
			position := line.Span.Start + offset
			if position > line.Span.End {
				return line.Span.End
			}
			return position
		}
		offset -= len(line.Text) + 1
	}
	return r.Header.End
}

func hasToolUse(uses []ToolUse, name string) bool {
	for i := range uses {
		if uses[i].Name == name {
			return true
		}
	}
	return false
}
