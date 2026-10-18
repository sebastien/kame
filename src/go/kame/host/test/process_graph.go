package host_test

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/testing"
)

func TestProcessTopologyValidation(t *testing.T) {
	stages := []host.ProcessStage{{Argv: []string{"a"}}, {Argv: []string{"b"}}, {Kind: "merge"}, {Argv: []string{"c"}}}
	edges := []host.ProcessEdge{{From: 0, To: 2}, {From: 1, To: 2}, {From: 2, To: 3}}
	request := host.ProcessRequest{Topology: true, Stages: stages, Edges: edges}
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "" { t.Error("valid merger rejected") }
	stages[3].Input = "conflict"
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_DOUBLE_CONSUME" { t.Error("redirected graph input accepted") }
	stages[3].Input = ""
	edges[0].From = 9
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_MUX_ARITY" { t.Error("invalid endpoint accepted") }
	cycleStages := []host.ProcessStage{{Argv: []string{"a"}}, {Argv: []string{"b"}}}
	cycleEdges := []host.ProcessEdge{{From: 0, To: 1}, {From: 1, To: 0}}
	request.Stages, request.Edges = cycleStages, cycleEdges
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_GRAPH_CYCLE" { t.Error("cycle accepted") }
}

func TestProcessTopologyRejectsPrivateFramesAtByteBoundaries(t *testing.T) {
	stages := []host.ProcessStage{{Argv: []string{"source"}}, {Kind: "frame", Framed: true}, {Kind: "encode"}, {Argv: []string{"sink"}}}
	edges := []host.ProcessEdge{{From: 0, To: 1}, {From: 1, To: 2}, {From: 2, To: 3}}
	request := host.ProcessRequest{Topology: true, Stages: stages, Edges: edges}
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "" { t.Error("valid frame/encode rejected") }
	request.Stages = stages[:2]
	request.Edges = edges[:1]
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_FRAME_MISMATCH" { t.Error("private framed presentation accepted") }
}

func TestProcessTopologyArgumentPortsAreBoundedAndUnique(t *testing.T) {
	stages := []host.ProcessStage{{Argv: []string{"a"}}, {Argv: []string{"b"}}, {Argv: []string{"cat", "<stream>", "<stream>"}, Input: "independent-stdin"}}
	edges := []host.ProcessEdge{{From: 0, To: 2, Argument: 1}, {From: 1, To: 2, Argument: 2}}
	request := host.ProcessRequest{Topology: true, Stages: stages, Edges: edges}
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "" { t.Error("independent argument ports conflicted with stdin") }
	edges[1].Argument = 1
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_DOUBLE_CONSUME" { t.Error("duplicate argument slot accepted") }
	edges[1].Argument = 3
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_MUX_ARITY" { t.Error("out-of-bounds argument slot accepted") }
}

func TestPipelinePayloadAllowsLeadingNativeNodes(t *testing.T) {
	a := t.Allocator()
	operator := core.NewList(a, nil)
	argument := core.NewString(a, "/bin/cat")
	executable := core.NewList(a, []core.Value{argument})
	argument.Free(a)
	payload := host.PipelinePayload(a, []core.Value{operator, executable})
	if host.PayloadPath(payload) != "/bin/cat" { t.Error("leading native node hid executable identity") }
	payload.Free(a); executable.Free(a); operator.Free(a)
}

func TestBalanceRequiresFramesAndValidatedWorkingBudget(t *testing.T) {
	stages := []host.ProcessStage{{Argv: []string{"source"}}, {Kind: "frame", Framed: true}, {Kind: "balance", Framed: true, BufferBytes: 4}, {Kind: "encode"}, {Kind: "encode"}, {Argv: []string{"left"}}, {Argv: []string{"right"}}}
	edges := []host.ProcessEdge{{From: 0, To: 1}, {From: 1, To: 2}, {From: 2, To: 3}, {From: 2, To: 4}, {From: 3, To: 5}, {From: 4, To: 6}}
	request := host.ProcessRequest{Topology: true, Stages: stages, Edges: edges}
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "" { t.Errorf("framed balance rejected: %s", code) }
	stages[2].BufferBytes = 65537
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_MUX_ARITY" { t.Error("unbounded operator budget accepted") }
	stages[2].BufferBytes = 0
	stages[1].Kind, stages[1].Framed = "copy", false
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_FRAME_REQUIRED" { t.Errorf("byte balance accepted: %s", code) }
	stages[0].BufferBytes = 4
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "KASH_MUX_ARITY" { t.Error("working budget accepted on an executable") }
}

func TestLossyBroadcastRequiresFramesAndBoundedPendingStorage(t *testing.T) {
	stages := []host.ProcessStage{{Argv: []string{"source"}}, {Kind: "frame", Framed: true}, {Kind: "broadcast", Framed: true, Overflow: 1, BufferBytes: 4096, BufferItems: 4}, {Kind: "encode"}, {Kind: "encode"}, {Argv: []string{"left"}}, {Argv: []string{"right"}}}
	edges := []host.ProcessEdge{{From: 0, To: 1}, {From: 1, To: 2}, {From: 2, To: 3}, {From: 2, To: 4}, {From: 3, To: 5}, {From: 4, To: 6}}
	request := host.ProcessRequest{Topology: true, Stages: stages, Edges: edges}
	if code := host.ProcessTopologyError(t.Allocator(), request); code != "" { t.Errorf("lossy broadcast rejected: %s", code) }
	stages[2].BufferItems = 1025
	if host.ProcessTopologyError(t.Allocator(), request) != "KASH_MUX_ARITY" { t.Error("unbounded pending queue accepted") }
	stages[2].BufferItems, stages[2].Overflow = 2, 3
	if host.ProcessTopologyError(t.Allocator(), request) != "KASH_MUX_ARITY" { t.Error("latest accepted more than one pending item") }
	stages[2].BufferItems, stages[2].Overflow, stages[2].Kind = 1, 1, "balance"
	if host.ProcessTopologyError(t.Allocator(), request) != "KASH_MUX_ARITY" { t.Error("lossy exactly-once balance accepted") }
	stages[2].Kind = "broadcast"
	stages[1].Kind, stages[1].Framed, stages[2].Framed = "copy", false, false
	if host.ProcessTopologyError(t.Allocator(), request) != "KASH_FRAME_REQUIRED" { t.Error("lossy byte broadcast accepted") }
}

func TestMetricsPayloadPreservesUnknownsAndOwnedRows(t *testing.T) {
	metrics := []host.ProcessEdgeMetrics{{Measurement: 1, Bytes: 9, Items: 3}, {Items: -1, DroppedItems: -1}, {Measurement: 3, Bytes: 1024, Items: 1, DroppedItems: 511, QueueHighWaterBytes: 2048, QueueHighWaterItems: 2}}
	value := host.ProcessMetricsPayload(t.Allocator(), metrics)
	if len(value.List) != 3 || len(value.List[0].List) != 7 { t.Error("invalid metrics row schema") } else {
		if value.List[0].List[1].Int != 9 || value.List[0].List[2].Int != 3 { t.Error("payload counters were lost") }
		if value.List[1].List[2].Int != -1 { t.Error("an unavailable measurement became zero") }
		if value.List[2].List[3].Int != 511 || value.List[2].List[5].Int != 2 { t.Error("queue/drop counters were lost") }
	}
	value.Free(t.Allocator())
}

func TestOrderedMergeRequiresFramesAndClosedPolicy(t *testing.T) {
	stages := []host.ProcessStage{{Argv: []string{"a"}}, {Argv: []string{"b"}}, {Kind: "merge", Order: 1}, {Argv: []string{"c"}}}
	edges := []host.ProcessEdge{{From: 0, To: 2}, {From: 1, To: 2}, {From: 2, To: 3}}
	request := host.ProcessRequest{Topology: true, Stages: stages, Edges: edges}
	if host.ProcessTopologyError(t.Allocator(), request) != "KASH_FRAME_REQUIRED" { t.Error("ordered byte merge accepted") }
	stages[2].Order = 2
	if host.ProcessTopologyError(t.Allocator(), request) != "KASH_MUX_ARITY" { t.Error("unknown merge ordering accepted") }
	stages[2].Order = 0; stages[0].Order = 1
	if host.ProcessTopologyError(t.Allocator(), request) != "KASH_MUX_ARITY" { t.Error("ordering accepted on an executable") }
}
