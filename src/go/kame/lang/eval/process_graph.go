package eval

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type graphPorts struct { Inputs []int; Outputs []int }

func (p *Program) captureTopology(e *expr.Expr, c *Context) Result {
	flat := mem.Alloc[expr.Expr](c.Run)
	defer expr.Free(c.Run, flat)
	flat.Kind, flat.Span, flat.Topology, flat.Async, flat.AcceptExit = e.Kind, e.Span, true, e.Async, e.AcceptExit
	for i := range e.Body { flat.Body = slices.Append(c.Run, flat.Body, expr.Clone(c.Run, e.Body[i])) }
	r := prepareProcessGraph(c, e, flat)
	if r.Diagnostic.Code != "" { return r }
	return p.capture(flat, c)
}

func hasProcessTopology(e *expr.Expr) bool {
	for i := range e.Items { if e.Items[i].Kind == expr.CommandGroup || e.Items[i].Kind == expr.CommandMux { return true } }
	return false
}

// Lower the complete authored topology before demanding any argument values.
// Build dependencies remain engine edges, not process-channel connections.
func prepareProcessGraph(c *Context, e *expr.Expr, flat *expr.Expr) Result {
	ports := lowerProcessGraph(c, e, flat)
	slices.Free(c.Run, ports.Inputs)
	slices.Free(c.Run, ports.Outputs)
	if flat.Text != "" { return failure(c.Run, flat.Text, flat.Span, "process connection has incompatible endpoint cardinality or disposition") }
	terminals := 0
	for i := range flat.Items {
		node := flat.Items[i]
		if node.Kind != expr.CommandStage { continue }
		incoming, outgoing := 0, 0
		for j := range flat.Edges { if flat.Edges[j].From == i { outgoing++ }; if flat.Edges[j].To == i { incoming++ } }
		redirected := false
		for j := range node.Items {
			word := node.Items[j]
			if word.Kind != expr.CommandRedirection { continue }
			if word.Text == "<" && incoming != 0 { return failure(c.Run, "KASH_DOUBLE_CONSUME", word.Span, "stdin redirection conflicts with graph input") }
			if word.Text != "<" { redirected = true; if outgoing != 0 { return failure(c.Run, "KASH_DOUBLE_CONSUME", word.Span, "stdout redirection conflicts with graph output") } }
		}
		if outgoing == 0 && !redirected { terminals++ }
	}
	if e.Kind == expr.CommandCapture && terminals > 1 { return failure(c.Run, "KASH_AMBIGUOUS_CAPTURE", e.Span, "capture requires one unmerged terminal stdout") }
	return Result{}
}

func lowerProcessGraph(c *Context, e *expr.Expr, flat *expr.Expr) graphPorts {
	var ports graphPorts
	if flat.Text != "" { return ports }
	if e.Kind == expr.CommandStage {
		index := len(flat.Items)
		flat.Items = slices.Append(c.Run, flat.Items, expr.Clone(c.Run, e))
		ports.Inputs = slices.Append(c.Run, ports.Inputs, index)
		ports.Outputs = slices.Append(c.Run, ports.Outputs, index)
		return ports
	}
	if e.Kind == expr.CommandGroup {
		for i := range e.Items {
			member := e.Items[i]
			if member.Async || member.AcceptExit || len(member.Body) != 0 { flat.Text, flat.Span = "KASH_MUX_ARITY", member.Span; return ports }
			child := lowerProcessGraph(c, member, flat)
			for j := range child.Inputs { ports.Inputs = slices.Append(c.Run, ports.Inputs, child.Inputs[j]) }
			for j := range child.Outputs { ports.Outputs = slices.Append(c.Run, ports.Outputs, child.Outputs[j]) }
			slices.Free(c.Run, child.Inputs); slices.Free(c.Run, child.Outputs)
		}
		return ports
	}
	ports = lowerProcessGraph(c, e.Items[0], flat)
	for i := 1; i < len(e.Items); i++ {
		policy := ""
		connection := e.Items[i]
		if connection.Kind == expr.CommandMux { policy = connection.Text; i++ }
		right := lowerProcessGraph(c, e.Items[i], flat)
		if policy == "" {
			if len(ports.Outputs) != 1 || len(right.Inputs) != 1 { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span } else { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[0], To: right.Inputs[0]}) }
		} else {
			if (policy == "merge" && (len(ports.Outputs) < 2 || len(right.Inputs) != 1)) || (policy == "broadcast" && (len(ports.Outputs) != 1 || len(right.Inputs) < 2)) { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span }
			index := len(flat.Items)
			flat.Items = slices.Append(c.Run, flat.Items, expr.Clone(c.Run, connection))
			for j := range ports.Outputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[j], To: index}) }
			for j := range right.Inputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: index, To: right.Inputs[j]}) }
		}
		slices.Free(c.Run, ports.Outputs); slices.Free(c.Run, right.Inputs)
		ports.Outputs = right.Outputs
	}
	return ports
}
