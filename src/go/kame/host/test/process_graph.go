package host_test

import (
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
