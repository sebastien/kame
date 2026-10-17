package host

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// ProcessTopologyError validates a host request independently of language
// parsing. No descriptors, files, or processes are created here.
func ProcessTopologyError(a mem.Allocator, request ProcessRequest) string {
	n := len(request.Stages)
	if !request.Topology || n == 0 { return "KASH_MUX_ARITY" }
	if request.Input != "" || request.Output != "" { return "KASH_DOUBLE_CONSUME" }
	incoming := slices.Make[int](a, n)
	outgoing := slices.Make[int](a, n)
	defer slices.Free(a, incoming)
	defer slices.Free(a, outgoing)
	for i := range request.Edges {
		edge := request.Edges[i]
		if edge.From < 0 || edge.From >= n || edge.To < 0 || edge.To >= n { return "KASH_MUX_ARITY" }
		incoming[edge.To]++; outgoing[edge.From]++
	}
	for i := range request.Stages {
		stage := request.Stages[i]
		if stage.Kind == "" {
			if len(stage.Argv) == 0 || incoming[i] > 1 || outgoing[i] > 1 { return "KASH_MUX_ARITY" }
			if (incoming[i] != 0 && stage.Input != "") || (outgoing[i] != 0 && stage.Output != "") { return "KASH_DOUBLE_CONSUME" }
		} else {
			if len(stage.Argv) != 0 || stage.Input != "" || stage.Output != "" { return "KASH_MUX_ARITY" }
			if stage.Kind == "merge" { if incoming[i] < 2 || outgoing[i] != 1 { return "KASH_MUX_ARITY" }
			} else if stage.Kind == "broadcast" { if incoming[i] != 1 || outgoing[i] < 2 { return "KASH_MUX_ARITY" }
			} else { return "KASH_MUX_ARITY" }
		}
	}
	// Kahn's traversal also catches cycles through otherwise valid 1:1 nodes.
	// ponytail: O(nodes*edges) for small invocation graphs; index adjacency if
	// large named graphs make validation a measured bottleneck.
	var ready []int
	defer slices.Free(a, ready)
	for i := range incoming { if incoming[i] == 0 { ready = slices.Append(a, ready, i) } }
	for cursor := 0; cursor < len(ready); cursor++ {
		for j := range request.Edges {
			edge := request.Edges[j]
			if edge.From == ready[cursor] { incoming[edge.To]--; if incoming[edge.To] == 0 { ready = slices.Append(a, ready, edge.To) } }
		}
	}
	if len(ready) != n { return "KASH_GRAPH_CYCLE" }
	return ""
}
