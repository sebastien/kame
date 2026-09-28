package program

import (
	"kame/diagnostic"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type graphNode struct {
	Target string
	Depth  int
}

type graphResult struct {
	Values     []string
	Diagnostic diagnostic.Diagnostic
}

type spanResult struct {
	Static     []string
	Dynamic    []string
	Outputs    []string
	Diagnostic diagnostic.Diagnostic
}

// WriteGraph emits the inputs or outputs JSON array for one target.
func (p *Program) WriteGraph(out io.Writer, target string, depth int, kind string) diagnostic.Diagnostic {
	traversal := p.graphValues(target, depth, kind)
	if traversal.Diagnostic.Code != "" {
		return traversal.Diagnostic
	}
	e := json.NewEncoder(out)
	e.BeginArray()
	for i := range traversal.Values {
		e.Str(traversal.Values[i])
	}
	e.EndArray()
	e.Flush()
	io.WriteString(out, "\n")
	FreeStrings(p.Alloc, traversal.Values)
	return diagnostic.Diagnostic{}
}

// WriteSpan emits the span JSON object for one target.
func (p *Program) WriteSpan(out io.Writer, target string, depth int, expand bool) diagnostic.Diagnostic {
	span := p.spanValues(target, depth, expand)
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
	writeStringArray(&e, span.Static)
	e.Str("outputs")
	writeStringArray(&e, span.Outputs)
	e.EndObject()
	e.Str("dynamic")
	writeStringArray(&e, span.Dynamic)
	e.Str("expanded")
	e.Bool(expand)
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
	FreeStrings(p.Alloc, span.Static)
	FreeStrings(p.Alloc, span.Dynamic)
	FreeStrings(p.Alloc, span.Outputs)
	return diagnostic.Diagnostic{}
}

// spanValues walks only authored static edges. When expansion is requested it
// also resolves expression-form inputs for every visited target, keeping their
// results separate from the static graph.
func (p *Program) spanValues(target string, depth int, expand bool) spanResult {
	if depth == 0 {
		return spanResult{}
	}
	var pending []graphNode
	pending = slices.Append(p.Alloc, pending, graphNode{Target: cloneText(p.Alloc, target)})
	var visited []string
	result := spanResult{}
	for cursor := 0; cursor < len(pending); cursor++ {
		node := pending[cursor]
		if graphContains(visited, node.Target) {
			mem.FreeString(p.Alloc, node.Target)
			continue
		}
		visited = slices.Append(p.Alloc, visited, node.Target)
		planned := p.Plan(node.Target)
		if planned.Diagnostic.Code != "" {
			if node.Depth == 0 {
				FreeStrings(p.Alloc, result.Static)
				FreeStrings(p.Alloc, result.Dynamic)
				FreeStrings(p.Alloc, result.Outputs)
				FreeStrings(p.Alloc, visited)
				slices.Free(p.Alloc, pending)
				return spanResult{Diagnostic: planned.Diagnostic}
			}
			planned.Diagnostic.Free(p.Alloc)
			continue
		}
		if expand {
			planned.Plan.Free(p.Alloc)
			planned = p.ExpandPlan(node.Target)
			if planned.Diagnostic.Code != "" {
				if node.Depth == 0 {
					FreeStrings(p.Alloc, result.Static)
					FreeStrings(p.Alloc, result.Dynamic)
					FreeStrings(p.Alloc, result.Outputs)
					FreeStrings(p.Alloc, visited)
					slices.Free(p.Alloc, pending)
					return spanResult{Diagnostic: planned.Diagnostic}
				}
				planned.Diagnostic.Free(p.Alloc)
				continue
			}
		}
		plan := planned.Plan
		for i := range plan.StaticInputs {
			graphAppend(p.Alloc, &result.Static, plan.StaticInputs[i])
		}
		if expand {
			for i := range plan.DynamicInputs {
				graphAppend(p.Alloc, &result.Dynamic, plan.DynamicInputs[i])
			}
		}
		for i := range plan.Outputs {
			graphAppend(p.Alloc, &result.Outputs, plan.Outputs[i])
		}
		if depth == -1 || node.Depth+1 < depth {
			for i := range plan.StaticInputs {
				if !p.HasTarget(plan.StaticInputs[i]) || graphContains(visited, plan.StaticInputs[i]) {
					continue
				}
				pending = slices.Append(p.Alloc, pending, graphNode{Target: cloneText(p.Alloc, plan.StaticInputs[i]), Depth: node.Depth + 1})
			}
		}
		plan.Free(p.Alloc)
	}
	if len(pending) != 0 {
		slices.Free(p.Alloc, pending)
	}
	FreeStrings(p.Alloc, visited)
	return result
}

// graphValues walks dependency plans breadth-first. Each discovered resource is
// emitted once in discovery order, which keeps graph output stable and makes
// cycles finite. A depth of one includes only the selected target's edges.
func (p *Program) graphValues(target string, depth int, kind string) graphResult {
	if depth == 0 {
		return graphResult{}
	}
	var pending []graphNode
	pending = slices.Append(p.Alloc, pending, graphNode{Target: cloneText(p.Alloc, target)})
	var visited []string
	var values []string
	for cursor := 0; cursor < len(pending); cursor++ {
		node := pending[cursor]
		if graphContains(visited, node.Target) {
			mem.FreeString(p.Alloc, node.Target)
			continue
		}
		visited = slices.Append(p.Alloc, visited, node.Target)
		result := p.Plan(node.Target)
		if result.Diagnostic.Code != "" {
			if node.Depth == 0 {
				FreeStrings(p.Alloc, values)
				FreeStrings(p.Alloc, visited)
				slices.Free(p.Alloc, pending)
				return graphResult{Diagnostic: result.Diagnostic}
			}
			result.Diagnostic.Free(p.Alloc)
			continue // An external input is a graph leaf rather than an error.
		}
		plan := result.Plan
		if kind == "inputs" {
			for i := range plan.Inputs {
				graphAppend(p.Alloc, &values, plan.Inputs[i])
			}
		} else {
			for i := range plan.Outputs {
				graphAppend(p.Alloc, &values, plan.Outputs[i])
			}
		}
		if depth == -1 || node.Depth+1 < depth {
			for i := range plan.Inputs {
				if !p.HasTarget(plan.Inputs[i]) || graphContains(visited, plan.Inputs[i]) {
					continue
				}
				pending = slices.Append(p.Alloc, pending, graphNode{Target: cloneText(p.Alloc, plan.Inputs[i]), Depth: node.Depth + 1})
			}
		}
		plan.Free(p.Alloc)
	}
	if len(pending) != 0 {
		slices.Free(p.Alloc, pending)
	}
	FreeStrings(p.Alloc, visited)
	return graphResult{Values: values}
}

func graphContains(values []string, value string) bool {
	for i := range values {
		if values[i] == value {
			return true
		}
	}
	return false
}

func graphAppend(a mem.Allocator, values *[]string, value string) {
	if graphContains(*values, value) {
		return
	}
	*values = slices.Append(a, *values, cloneText(a, value))
}
