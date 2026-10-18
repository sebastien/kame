package eval

import (
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func addStreamNode(c *Context, graph *expr.Expr, kind string, span source.Span) int {
	node := mem.Alloc[expr.Expr](c.Run)
	node.Kind, node.Text, node.Span = expr.CommandMux, kind, span
	index := len(graph.Items)
	graph.Items = slices.Append(c.Run, graph.Items, node)
	return index
}

// Bytes are 0; framed lines are 1. On framed edges the hosts transport a
// length-prefixed raw payload, so a partial EOF line remains a distinct item.
func prepareStreamKinds(c *Context, graph *expr.Expr) Result {
	for i := range graph.Items { kind := -1; if graph.Items[i].Kind == expr.CommandStage { kind = 0 }; graph.StreamKinds = slices.Append(c.Run, graph.StreamKinds, kind) }
	for {
		progress, remaining := false, false
		for i := range graph.Items {
			if graph.StreamKinds[i] >= 0 { continue }
			remaining = true
			input, ready, count := -1, true, 0
			for j := range graph.Edges {
				edge := graph.Edges[j]
				if edge.To != i { continue }
				kind := graph.StreamKinds[edge.From]
				if kind < 0 { ready = false; continue }
				if input >= 0 && input != kind { return failure(c.Run, "KASH_FRAME_MISMATCH", graph.Items[i].Span, "merge/rejoin inputs have incompatible framing") }
				input = kind; count++
			}
			if !ready { continue }
			node := graph.Items[i]
			if count == 0 { return failure(c.Run, "KASH_MUX_ARITY", node.Span, "stream operator requires an input") }
			if node.StreamOverflow != 0 && input != 1 { return failure(c.Run, "KASH_FRAME_REQUIRED", node.Span, "lossy broadcast requires explicit record framing") }
			output := input
			if node.Text == "frame" {
				if input != 0 { return failure(c.Run, "KASH_FRAME_MISMATCH", node.Span, "frame :lines requires bytes") }
				output = 1
			} else if node.Text == "encode" || node.Text == "filter" || node.Text == "balance" {
				if input != 1 { return failure(c.Run, "KASH_FRAME_REQUIRED", node.Span, "item-aware operators require explicit line framing") }
				if node.Text == "encode" { output = 0 }
			}
			graph.StreamKinds[i], progress = output, true
		}
		if !remaining { break }
		if !progress { return failure(c.Run, "KASH_GRAPH_CYCLE", graph.Span, "stream topology has an unresolved feedback cycle") }
	}
	// External stdin and presentation sinks receive payload bytes, never the
	// private frame envelope. These adapters are explicit prepared graph nodes.
	edges := len(graph.Edges)
	for i := 0; i < edges; i++ {
		edge := graph.Edges[i]
		if graph.StreamKinds[edge.From] == 1 && graph.Items[edge.To].Kind == expr.CommandStage {
			adapter := addStreamNode(c, graph, "encode", graph.Items[edge.To].Span)
			graph.StreamKinds = slices.Append(c.Run, graph.StreamKinds, 0)
			graph.Edges[i].To = adapter
			graph.Edges[i].Argument = 0
			graph.Edges = slices.Append(c.Run, graph.Edges, expr.CommandEdge{From: adapter, To: edge.To, Argument: edge.Argument})
		}
	}
	nodes := len(graph.Items)
	for i := 0; i < nodes; i++ {
		if graph.StreamKinds[i] != 1 { continue }
		connected := false
		for j := range graph.Edges { if graph.Edges[j].From == i { connected = true } }
		if !connected {
			adapter := addStreamNode(c, graph, "encode", graph.Items[i].Span)
			graph.StreamKinds = slices.Append(c.Run, graph.StreamKinds, 0)
			graph.Edges = slices.Append(c.Run, graph.Edges, expr.CommandEdge{From: i, To: adapter})
		}
	}
	return Result{}
}
