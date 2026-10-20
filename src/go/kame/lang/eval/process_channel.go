package eval

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Resolve endpoints only inside this one simultaneously owned graph. A declared
// name never makes separate sequential commands into an implicit async graph.
// ponytail: quadratic scans/compaction for small invocation graphs; index ports
// if large named graphs make construction a measured bottleneck.
func resolveProcessChannels(c *Context, authored *expr.Expr, graph *expr.Expr) Result {
	for i := range graph.Items {
		node := graph.Items[i]
		if node.Kind != expr.CommandReader && node.Kind != expr.CommandWriter {
			continue
		}
		if !slices.Contains(authored.Channels, node.Text) {
			return failure(c.Run, "KASH_CHANNEL_UNBOUND", node.Span, "channel is not declared in this graph's lexical scope")
		}
		writer, reader := -1, -1
		for j := range graph.Items {
			peer := graph.Items[j]
			if peer.Text != node.Text {
				continue
			}
			if peer.Kind == expr.CommandReader {
				if reader >= 0 {
					return failure(c.Run, "KASH_CHANNEL_DUP_READER", peer.Span, "channel has more than one reader; use broadcast")
				}
				reader = j
			}
			if peer.Kind == expr.CommandWriter {
				if writer >= 0 {
					return failure(c.Run, "KASH_CHANNEL_DUP_WRITER", peer.Span, "channel has more than one writer; use merge")
				}
				writer = j
			}
		}
		if writer < 0 || reader < 0 {
			return failure(c.Run, "KASH_CHANNEL_UNBOUND", node.Span, "both channel endpoints must belong to the same constructed graph")
		}
		if i == writer {
			graph.Edges = slices.Append(c.Run, graph.Edges, expr.CommandEdge{From: writer, To: reader})
		}
	}
	// Named connections are aliases, not worker processes. Splice identity ports
	// out of the graph before host launch; only a terminal reader needs a copy
	// node to present bytes. Never splice away a self-loop and hide feedback.
	for i := 0; i < len(graph.Items); i++ {
		node := graph.Items[i]
		if node.Kind != expr.CommandReader && node.Kind != expr.CommandWriter {
			continue
		}
		input, output := -1, -1
		for j := range graph.Edges {
			if graph.Edges[j].To == i {
				input = j
			}
			if graph.Edges[j].From == i {
				output = j
			}
		}
		if input < 0 {
			return failure(c.Run, "KASH_CHANNEL_UNBOUND", node.Span, "channel writer has no connected source")
		}
		if output >= 0 && graph.Edges[input].From != i && graph.Edges[output].To != i {
			graph.Edges[input].To = graph.Edges[output].To
			graph.Edges[input].Argument = graph.Edges[output].Argument
			copy(graph.Edges[output:], graph.Edges[output+1:])
			graph.Edges = graph.Edges[:len(graph.Edges)-1]
			expr.Free(c.Run, node)
			copy(graph.Items[i:], graph.Items[i+1:])
			graph.Items = graph.Items[:len(graph.Items)-1]
			for j := range graph.Edges {
				if graph.Edges[j].From > i {
					graph.Edges[j].From--
				}
				if graph.Edges[j].To > i {
					graph.Edges[j].To--
				}
			}
			i--
		} else {
			if node.TextOwned {
				mem.FreeString(c.Run, node.Text)
			}
			node.Kind, node.Text, node.TextOwned = expr.CommandMux, "copy", false
		}
	}
	return Result{}
}

func acyclicProcessGraph(c *Context, graph *expr.Expr) bool {
	indegree := slices.Make[int](c.Run, len(graph.Items))
	defer slices.Free(c.Run, indegree)
	var ready []int
	for i := range graph.Edges {
		indegree[graph.Edges[i].To]++
	}
	for i := range indegree {
		if indegree[i] == 0 {
			ready = slices.Append(c.Run, ready, i)
		}
	}
	for i := 0; i < len(ready); i++ {
		for j := range graph.Edges {
			edge := graph.Edges[j]
			if edge.From == ready[i] {
				indegree[edge.To]--
				if indegree[edge.To] == 0 {
					ready = slices.Append(c.Run, ready, edge.To)
				}
			}
		}
	}
	ok := len(ready) == len(graph.Items)
	slices.Free(c.Run, ready)
	return ok
}
