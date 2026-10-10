package program

import (
	"kame/diagnostic"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type spanNode struct {
	Target string
	Depth  int
}

type spanResult struct {
	Waiting bool
	Static     []string
	Dynamic    []string
	Outputs    []string
	Diagnostic diagnostic.Diagnostic
}

// WriteSpan emits the span JSON object for one target.
func (p *Program) WriteSpan(out io.Writer, target string, depth int, expand bool) diagnostic.Diagnostic {
	p.InspectionWaiting = false
	defer p.finishInspection()
	if depth < -1 { p.clearInspection(); return failure(p.Alloc, "OPT_VALUE_INVALID", "invalid span depth") }
	targets := []string{target}
	kind := "span"
	if expand { kind = "expanded-span" }
	g := inspectionGraph{Program: p}
	defer g.free()
	var pending *inspectionGraph
	if p.pendingInspection != nil { pending = p.pendingInspection.(*inspectionGraph) }
	if pending != nil && pending.sameQuery(targets, depth, kind) {
		g = *pending
		mem.Free(p.Alloc, pending)
		p.pendingInspection = nil
	} else {
		p.clearInspection()
		g.QueryTargets, g.QueryKind, g.QueryDepth = cloneStrings(p.Alloc, targets), cloneText(p.Alloc, kind), depth
	}
	span := p.spanValues(target, depth, expand)
	if span.Waiting { p.retainInspection(&g); return diagnostic.Diagnostic{} }
	if span.Diagnostic.Code != "" {
		return span.Diagnostic
	}
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str("span")
	e.Str("static")
	e.BeginObject()
	e.Str("inputs")
	encodeStringArray(&e, span.Static)
	e.Str("outputs")
	encodeStringArray(&e, span.Outputs)
	e.EndObject()
	e.Str("dynamic")
	encodeStringArray(&e, span.Dynamic)
	e.Str("expanded")
	e.Bool(expand)
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
	freeStrings(p.Alloc, span.Static)
	freeStrings(p.Alloc, span.Dynamic)
	freeStrings(p.Alloc, span.Outputs)
	return diagnostic.Diagnostic{}
}

// spanValues walks only authored static edges. When expansion is requested it
// also resolves expression-form inputs for every visited target, keeping their
// results separate from the static graph.
func (p *Program) spanValues(target string, depth int, expand bool) spanResult {
	if depth == 0 {
		return spanResult{}
	}
	var pending []spanNode
	pending = slices.Append(p.Alloc, pending, spanNode{Target: cloneText(p.Alloc, target)})
	var visited []string
	result := spanResult{}
	for cursor := 0; cursor < len(pending); cursor++ {
		node := pending[cursor]
		if slices.Contains(visited, node.Target) {
			mem.FreeString(p.Alloc, node.Target)
			continue
		}
		visited = slices.Append(p.Alloc, visited, node.Target)
		planned := p.Plan(node.Target)
		if planned.Diagnostic.Code != "" {
			if node.Depth == 0 {
				freeStrings(p.Alloc, result.Static)
				freeStrings(p.Alloc, result.Dynamic)
				freeStrings(p.Alloc, result.Outputs)
				freeStrings(p.Alloc, visited)
				slices.Free(p.Alloc, pending)
				return spanResult{Diagnostic: planned.Diagnostic}
			}
			planned.Diagnostic.Free(p.Alloc)
			continue
		}
		if expand {
			planned.Plan.Free(p.Alloc)
			planned = p.expandPlan(node.Target, p.Forwarding)
			if planned.Waiting {
				freeStrings(p.Alloc, result.Static)
				freeStrings(p.Alloc, result.Dynamic)
				freeStrings(p.Alloc, result.Outputs)
				freeStrings(p.Alloc, visited)
				for i := cursor+1; i < len(pending); i++ { mem.FreeString(p.Alloc, pending[i].Target) }
				slices.Free(p.Alloc, pending)
				return spanResult{Waiting: true}
			}
			if planned.Diagnostic.Code != "" {
				if node.Depth == 0 {
					freeStrings(p.Alloc, result.Static)
					freeStrings(p.Alloc, result.Dynamic)
					freeStrings(p.Alloc, result.Outputs)
					freeStrings(p.Alloc, visited)
					slices.Free(p.Alloc, pending)
					return spanResult{Diagnostic: planned.Diagnostic}
				}
				planned.Diagnostic.Free(p.Alloc)
				continue
			}
		}
		plan := planned.Plan
		for i := range plan.StaticInputs {
			appendUniqueString(p.Alloc, &result.Static, plan.StaticInputs[i])
		}
		if expand {
			for i := range plan.DynamicInputs {
				appendUniqueString(p.Alloc, &result.Dynamic, plan.DynamicInputs[i])
			}
		}
		for i := range plan.Outputs {
			appendUniqueString(p.Alloc, &result.Outputs, plan.Outputs[i])
		}
		if depth == -1 || node.Depth+1 < depth {
			for i := range plan.StaticInputs {
				if !p.HasTarget(plan.StaticInputs[i]) || slices.Contains(visited, plan.StaticInputs[i]) {
					continue
				}
				pending = slices.Append(p.Alloc, pending, spanNode{Target: cloneText(p.Alloc, plan.StaticInputs[i]), Depth: node.Depth + 1})
			}
		}
		plan.Free(p.Alloc)
	}
	slices.Free(p.Alloc, pending)
	freeStrings(p.Alloc, visited)
	return result
}
