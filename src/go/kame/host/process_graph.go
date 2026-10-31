package host

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// ProcessTopologyError validates a host request independently of language
// parsing. No descriptors, files, or processes are created here.
func ProcessTopologyError(a mem.Allocator, request ProcessRequest) string {
	n := len(request.Stages)
	if !request.Topology || n == 0 {
		return "KASH_MUX_ARITY"
	}
	if request.Input != "" || request.Output != "" {
		return "KASH_DOUBLE_CONSUME"
	}
	incoming := slices.Make[int](a, n)
	outgoing := slices.Make[int](a, n)
	stdin := slices.Make[int](a, n)
	defer slices.Free(a, incoming)
	defer slices.Free(a, outgoing)
	defer slices.Free(a, stdin)
	for i := range request.Edges {
		edge := request.Edges[i]
		if edge.From < 0 || edge.From >= n || edge.To < 0 || edge.To >= n {
			return "KASH_MUX_ARITY"
		}
		if edge.Argument < 0 {
			return "KASH_MUX_ARITY"
		}
		if edge.Argument == 0 {
			stdin[edge.To]++
		} else {
			if request.Stages[edge.To].Kind != "" || edge.Argument >= len(request.Stages[edge.To].Argv) {
				return "KASH_MUX_ARITY"
			}
			for j := 0; j < i; j++ {
				previous := request.Edges[j]
				if previous.To == edge.To && previous.Argument == edge.Argument {
					return "KASH_DOUBLE_CONSUME"
				}
			}
		}
		incoming[edge.To]++
		outgoing[edge.From]++
	}
	for i := range request.Stages {
		stage := request.Stages[i]
		operator := LookupOperatorKind(stage.Kind)
		if operator == nil {
			return "KASH_MUX_ARITY"
		}
		if stage.Order < 0 || stage.Order > 1 || (stage.Order != 0 && !operator.Ordered) {
			return "KASH_MUX_ARITY"
		}
		if stage.BufferBytes < 0 || stage.BufferBytes > 65536 || (stage.BufferBytes != 0 && !operator.Queue) {
			return "KASH_MUX_ARITY"
		}
		if stage.Overflow < 0 || stage.Overflow > 3 || stage.BufferItems < 0 || stage.BufferItems > 1024 || (stage.Overflow == 0 && stage.BufferItems != 0) || (stage.Overflow != 0 && !operator.Lossy) || (stage.Overflow == 3 && stage.BufferItems > 1) {
			return "KASH_MUX_ARITY"
		}
		if stage.Framed && outgoing[i] == 0 {
			return "KASH_FRAME_MISMATCH"
		}
		if operator.Argv {
			if len(stage.Argv) == 0 || stdin[i] < operator.MinIn || (operator.MaxIn >= 0 && stdin[i] > operator.MaxIn) || (operator.MaxOut >= 0 && outgoing[i] > operator.MaxOut) {
				return "KASH_MUX_ARITY"
			}
			if (stdin[i] != 0 && stage.Input != "") || (outgoing[i] != 0 && stage.Output != "") {
				return "KASH_DOUBLE_CONSUME"
			}
			continue
		}
		if len(stage.Argv) != 0 || stage.Input != "" || (stage.Output != "" && !operator.OutputFile) {
			return "KASH_MUX_ARITY"
		}
		if incoming[i] < operator.MinIn || (operator.MaxIn >= 0 && incoming[i] > operator.MaxIn) || outgoing[i] < operator.MinOut || (operator.MaxOut >= 0 && outgoing[i] > operator.MaxOut) {
			return "KASH_MUX_ARITY"
		}
	}
	// Kahn's traversal also catches cycles through otherwise valid 1:1 nodes.
	// ponytail: O(nodes*edges) for small invocation graphs; index adjacency if
	// large named graphs make validation a measured bottleneck.
	var ready []int
	defer slices.Free(a, ready)
	for i := range incoming {
		if incoming[i] == 0 {
			ready = slices.Append(a, ready, i)
		}
	}
	for cursor := 0; cursor < len(ready); cursor++ {
		for j := range request.Edges {
			edge := request.Edges[j]
			if edge.From == ready[cursor] {
				incoming[edge.To]--
				if incoming[edge.To] == 0 {
					ready = slices.Append(a, ready, edge.To)
				}
			}
		}
	}
	if len(ready) != n {
		return "KASH_GRAPH_CYCLE"
	}
	for cursor := range ready {
		i := ready[cursor]
		stage := request.Stages[i]
		operator := LookupOperatorKind(stage.Kind)
		if operator == nil {
			return "KASH_MUX_ARITY"
		}
		input, found := false, false
		for j := range request.Edges {
			edge := request.Edges[j]
			if edge.To != i {
				continue
			}
			kind := request.Stages[edge.From].Framed
			if found && input != kind {
				return "KASH_FRAME_MISMATCH"
			}
			input, found = kind, true
		}
		expected := input
		if operator.Frames == FramesFrame {
			expected = true
		}
		if operator.Frames == FramesUnframe {
			expected = false
		}
		if operator.InputFrames == InputsRequireFrames && !input {
			return "KASH_FRAME_REQUIRED"
		}
		if operator.InputFrames == InputsFramed && !input {
			return "KASH_FRAME_MISMATCH"
		}
		if operator.InputFrames == InputsUnframed && input {
			return "KASH_FRAME_MISMATCH"
		}
		if stage.Overflow != 0 && !input {
			return "KASH_FRAME_REQUIRED"
		}
		if stage.Order != 0 && !input {
			return "KASH_FRAME_REQUIRED"
		}
		if stage.Framed != expected {
			return "KASH_FRAME_MISMATCH"
		}
	}
	return ""
}
