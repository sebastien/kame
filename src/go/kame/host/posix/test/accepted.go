package posix_test

import (
	"kame/host"
	"kame/host/posix"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestAcceptedExitKeepsRawAndEffectiveFailureSeparate(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	defer h.Free()
	stages := []host.ProcessStage{{Argv: []string{"/bin/sh", "-c", "exit 7"}}, {Argv: []string{"/bin/sh", "-c", "exit 1"}}, {Kind: "merge"}, {Argv: []string{"/bin/cat"}}}
	stages[1].Accepted[1] = true
	edges := []host.ProcessEdge{{From: 0, To: 2}, {From: 1, To: 2}, {From: 2, To: 3}}
	if !h.Start(host.ProcessRequest{ID: 901, Topology: true, CollectFailures: true, Stages: stages, Edges: edges}) {
		t.Fatal("selective graph rejected")
		return
	}
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Status != 1 || !terminal.HasEffectiveStatus || terminal.EffectiveStatus != 7 || len(terminal.Stages) != 4 {
		t.Error("accepted later stage erased raw aggregate or earlier effective failure")
	} else if terminal.Stages[1].Status != 1 || terminal.Stages[1].EffectiveStatus != 0 || terminal.Stages[0].EffectiveStatus != 7 {
		t.Error("stage provenance lost")
	}
	for i := range events {
		events[i].Free(a)
	}
	slices.Free(a, events)
}
