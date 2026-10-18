package eval

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type graphPorts struct { Inputs []int; Outputs []int }
type graphJoin struct { Target int; Source int }

// InspectProcessTopology lowers syntax only: no predicates, argument values,
// tools, grants, dependencies, or host requests are evaluated. Caller owns flat.
func InspectProcessTopology(a mem.Allocator, e *expr.Expr, flat *expr.Expr) Result {
	flat.Kind, flat.Span = e.Kind, e.Span
	c := Context{Run: a}
	return prepareProcessGraph(&c, e, flat)
}

func (p *Program) captureTopology(e *expr.Expr, c *Context) Result {
	flat := mem.Alloc[expr.Expr](c.Run)
	defer expr.Free(c.Run, flat)
	flat.Kind, flat.Span, flat.Topology, flat.Async, flat.AcceptExit = e.Kind, e.Span, true, e.Async, e.AcceptExit
	for i := range e.Body { flat.Body = slices.Append(c.Run, flat.Body, expr.Clone(c.Run, e.Body[i])) }
	r := prepareProcessGraph(c, e, flat)
	if r.Diagnostic.Code != "" { return r }
	previous := c.PrepareDeferred
	if previous == e { c.PrepareDeferred = flat }
	r = p.capture(flat, c)
	c.PrepareDeferred = previous
	return r
}

func hasProcessTopology(e *expr.Expr) bool {
	for i := range e.Items { if e.Items[i].Kind == expr.CommandGroup || e.Items[i].Kind == expr.CommandMux || e.Items[i].Kind == expr.CommandReader || e.Items[i].Kind == expr.CommandWriter { return true } }
	for i := range e.Items { for j := range e.Items[i].Items { if e.Items[i].Items[j].Kind == expr.CommandOperand { return true } } }
	return false
}

// Lower the complete authored topology before demanding any argument values.
// Build dependencies remain engine edges, not process-channel connections.
func prepareProcessGraph(c *Context, e *expr.Expr, flat *expr.Expr) Result {
	ports := lowerProcessGraph(c, e, flat)
	slices.Free(c.Run, ports.Inputs)
	slices.Free(c.Run, ports.Outputs)
	if flat.Text != "" { return failure(c.Run, flat.Text, flat.Span, "process connection has incompatible endpoint cardinality or disposition") }
	for i := range flat.Items {
		node := flat.Items[i]
		if node.StreamFailure == 0 { continue }
		if flat.StreamFailure != 0 && flat.StreamFailure != node.StreamFailure { return failure(c.Run, "KASH_FAILURE_POLICY", node.Span, "conflicting graph-wide failure policies") }
		flat.StreamFailure = node.StreamFailure
	}
	for i := range flat.Items {
		if flat.Items[i].Kind != expr.CommandStage || flat.Items[i].Int == 0 { continue }
		claims := 0
		for j := range flat.Edges { if flat.Edges[j].To == i && flat.Edges[j].Argument == 0 { flat.Edges[j].Argument = int(flat.Items[i].Int); claims++ } }
		if claims != 1 { return failure(c.Run, "KASH_CHANNEL_UNBOUND", flat.Items[i].Span, "<& requires exactly one current upstream channel") }
	}
	if r := resolveProcessChannels(c, e, flat); r.Diagnostic.Code != "" { return r }
	if !acyclicProcessGraph(c, flat) { return failure(c.Run, "KASH_GRAPH_CYCLE", e.Span, "named channels introduce unsupported feedback") }
	if r := prepareStreamKinds(c, flat); r.Diagnostic.Code != "" { return r }
	terminals := 0
	for i := range flat.Items {
		node := flat.Items[i]
		incoming, outgoing := 0, 0
		for j := range flat.Edges { if flat.Edges[j].From == i { outgoing++ }; if flat.Edges[j].To == i && flat.Edges[j].Argument == 0 { incoming++ } }
		redirected := false
		for j := 0; node.Kind == expr.CommandStage && j < len(node.Items); j++ {
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
	if e.Kind == expr.CommandReader || e.Kind == expr.CommandWriter {
		index := len(flat.Items)
		flat.Items = slices.Append(c.Run, flat.Items, expr.Clone(c.Run, e))
		if e.Kind == expr.CommandReader { ports.Outputs = slices.Append(c.Run, ports.Outputs, index) } else { ports.Inputs = slices.Append(c.Run, ports.Inputs, index) }
		return ports
	}
	if e.Kind == expr.CommandStage { return lowerStreamOperandStage(c, e, flat) }
	if e.Kind == expr.CommandInput {
		index := len(flat.Items)
		node := expr.Clone(c.Run, e)
		if e.Kind == expr.CommandInput { node.Kind, node.Text = expr.CommandMux, "copy" }
		flat.Items = slices.Append(c.Run, flat.Items, node)
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
	var joins []graphJoin
	defer slices.Free(c.Run, joins)
	for i := 1; i < len(e.Items); i++ {
		connection := e.Items[i]
		joined := []int{}
		for j := range joins { if joins[j].Target == i { joined = slices.Append(c.Run, joined, joins[j].Source) } }
		if len(joined) != 0 {
			if len(ports.Outputs) != 1 { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span }
			merge := addStreamNode(c, flat, "merge", connection.Span)
			for j := range ports.Outputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[j], To: merge}) }
			for j := range joined { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: joined[j], To: merge}) }
			slices.Free(c.Run, ports.Outputs); ports.Outputs = slices.Append(c.Run, []int{}, merge)
		}
		slices.Free(c.Run, joined)
		if connection.Kind == expr.CommandMux && connection.Text == "each" {
			if len(ports.Outputs) != 1 || i + 1 >= len(e.Items) || e.Items[i + 1].Kind != expr.CommandStage { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span; return ports }
			i++
			template := expr.Clone(c.Run, e.Items[i]); template.Each = true
			for j := range template.Items { if template.Items[j].Kind == expr.CommandOperand { flat.Text, flat.Span = "KASH_DOUBLE_CONSUME", template.Items[j].Span } }
			right := lowerProcessGraph(c, template, flat); expr.Free(c.Run, template)
			if len(right.Inputs) != 1 || len(right.Outputs) != 1 { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span
			} else { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[0], To: right.Inputs[0]}) }
			slices.Free(c.Run, ports.Outputs); slices.Free(c.Run, right.Inputs); ports.Outputs = right.Outputs
			continue
		}
		if connection.Kind == expr.CommandMux && (connection.Text == "frame" || connection.Text == "encode" || connection.Text == "filter") {
			if len(ports.Outputs) != 1 { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span }
			index := len(flat.Items)
			node := expr.Clone(c.Run, connection)
			for j := range node.Body { expr.Free(c.Run, node.Body[j]) }; slices.Free(c.Run, node.Body); node.Body = nil
			flat.Items = slices.Append(c.Run, flat.Items, node)
			for j := range ports.Outputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[j], To: index}) }
			slices.Free(c.Run, ports.Outputs); ports.Outputs = slices.Append(c.Run, []int{}, index)
			if connection.Text == "filter" {
				branch := connection.Body[0]
				selected := lowerProcessGraph(c, branch, flat)
				if len(selected.Inputs) != 1 || (len(selected.Outputs) != 1 && !(len(selected.Outputs) == 0 && branch.Exit == "")) { flat.Text, flat.Span = "KASH_MUX_ARITY", branch.Span }
				for j := range selected.Inputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: index, To: selected.Inputs[j]}) }
				if branch.Exit != "" {
					target := i+1
					if branch.Exit == "otherwise" { for target < len(e.Items) && e.Items[target].Kind == expr.CommandMux && e.Items[target].Text == "filter" { target++ } }
					if target >= len(e.Items) { flat.Text, flat.Span = "KASH_REJOIN_OUT_OF_SCOPE", branch.Span } else { for j := range selected.Outputs { joins = slices.Append(c.Run, joins, graphJoin{Target: target, Source: selected.Outputs[j]}) } }
				}
				slices.Free(c.Run, selected.Inputs); slices.Free(c.Run, selected.Outputs)
			}
			continue
		}
		policy := ""
		if connection.Kind == expr.CommandWriter {
			right := lowerProcessGraph(c, connection, flat)
			if len(ports.Outputs) != 1 { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span } else { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[0], To: right.Inputs[0]}) }
			slices.Free(c.Run, ports.Outputs); slices.Free(c.Run, right.Inputs)
			ports.Outputs = right.Outputs
			continue
		}
		if connection.Kind == expr.CommandMux { policy = connection.Text }
		if policy == "merge" && (i+1 == len(e.Items) || e.Items[i+1].Kind == expr.CommandMux) {
			if len(ports.Outputs) < 2 { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span }
			merge := len(flat.Items)
			flat.Items = slices.Append(c.Run, flat.Items, expr.Clone(c.Run, connection))
			for j := range ports.Outputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[j], To: merge}) }
			slices.Free(c.Run, ports.Outputs); ports.Outputs = slices.Append(c.Run, []int{}, merge)
			continue
		}
		if policy != "" { i++; if i == len(e.Items) { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span; break } }
		right := lowerProcessGraph(c, e.Items[i], flat)
		if policy == "" {
			if len(ports.Outputs) != 1 || len(right.Inputs) != 1 { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span } else { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[0], To: right.Inputs[0]}) }
		} else {
			if (policy == "merge" && (len(ports.Outputs) < 2 || len(right.Inputs) != 1)) || ((policy == "broadcast" || policy == "balance") && (len(ports.Outputs) != 1 || len(right.Inputs) < 2)) { flat.Text, flat.Span = "KASH_MUX_ARITY", connection.Span }
			index := len(flat.Items)
			flat.Items = slices.Append(c.Run, flat.Items, expr.Clone(c.Run, connection))
			for j := range ports.Outputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[j], To: index}) }
			for j := range right.Inputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: index, To: right.Inputs[j]}) }
		}
		slices.Free(c.Run, ports.Outputs); slices.Free(c.Run, right.Inputs)
		ports.Outputs = right.Outputs
	}
	if len(e.Items) != 0 && e.Items[len(e.Items)-1].Kind == expr.CommandMux && e.Items[len(e.Items)-1].Text == "filter" {
		sink := addStreamNode(c, flat, "copy", e.Span)
		for i := range ports.Outputs { flat.Edges = slices.Append(c.Run, flat.Edges, expr.CommandEdge{From: ports.Outputs[i], To: sink}) }
		slices.Free(c.Run, ports.Outputs); ports.Outputs = slices.Append(c.Run, []int{}, sink)
	}
	return ports
}
