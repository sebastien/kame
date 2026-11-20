package posix

import (
	"bytes"
	"kame/host"
	"testing"
	"time"
)

func TestCallableStageRoutesBytesAndStatus(t *testing.T) {
	h := New(nil)
	defer h.Free()
	const graph = 400
	if !h.Start(host.ProcessRequest{
		ID: graph, Topology: true, TimeoutMS: 3000, RetainBytes: 32,
		Stages: []host.ProcessStage{
			{Argv: []string{"/bin/sh", "-c", `printf 'a\000b'`}},
			{Kind: "callable", Accepted: [256]bool{7: true}},
			{Argv: []string{"/bin/cat"}},
		},
		Edges: []host.ProcessEdge{{From: 0, To: 1}, {From: 1, To: 2}},
	}) {
		t.Fatal("graph launch rejected")
	}
	var terminal host.ProcessEvent
	waitEvent := func(id int64, kind host.ProcessEventKind) host.ProcessEvent {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			h.Pump(10)
			for next := h.Next(); next.OK; next = h.Next() {
				event := next.Event
				if event.Kind == host.ProcessTerminal {
					terminal = event
				}
				if event.ID == id && event.Kind == kind {
					return event
				}
			}
		}
		t.Fatalf("no event kind %d for request %d", kind, id)
		return host.ProcessEvent{}
	}
	next := int64(graph)
	exchange := func(op string, data []byte, status int) host.ProcessEvent {
		t.Helper()
		next++
		message := host.StageIO{Graph: graph, Index: 1, Operation: op, Bytes: data, Status: status}
		if !h.StartStageIO(host.Request{ID: next, NodeID: 9, Generation: 3, Attempt: 2}, message) {
			t.Fatalf("stage exchange rejected: %s", op)
		}
		event := waitEvent(next, host.ProcessStageIOCompleted)
		if event.Diagnostic.Code != "" || event.StageIO.NodeID != 9 || event.StageIO.Generation != 3 || event.StageIO.Attempt != 2 {
			t.Fatalf("bad stage completion: %+v", event)
		}
		return event
	}
	var input []byte
	for {
		event := exchange(host.StageRead, nil, 0)
		if event.StageIO.Closed {
			break
		}
		input = append(input, event.Data...)
	}
	if !bytes.Equal(input, []byte{'a', 0, 'b'}) {
		t.Fatalf("input = %q", input)
	}
	exchange(host.StageStdout, bytes.ToUpper(input), 0)
	exchange(host.StageStderr, []byte("notice"), 0)
	exchange(host.StageFinish, nil, 7)
	if terminal.ID == 0 {
		terminal = waitEvent(graph, host.ProcessTerminal)
	}
	if terminal.Outcome != host.ProcessExited || terminal.EffectiveStatus != 0 || len(terminal.Stages) != 3 ||
		terminal.Stages[1].Status != 7 || !bytes.Equal(terminal.Stdout, []byte{'A', 0, 'B'}) || string(terminal.Stderr) != "notice" {
		t.Fatalf("unexpected terminal report: %+v", terminal)
	}
}

func TestCallableStageClosedPipeCanFinishCancelled(t *testing.T) {
	h := New(nil)
	defer h.Free()
	const graph = 410
	if !h.Start(host.ProcessRequest{
		ID: graph, Topology: true, TimeoutMS: 3000,
		Stages: []host.ProcessStage{{Kind: "callable"}, {Argv: []string{"/bin/true"}}},
		Edges:  []host.ProcessEdge{{From: 0, To: 1}},
	}) {
		t.Fatal("graph launch rejected")
	}
	chunk := make([]byte, host.StageChunkLimit)
	write := func(id int64) {
		t.Helper()
		if !h.StartStageIO(host.Request{ID: id}, host.StageIO{Graph: graph, Operation: host.StageStdout, Bytes: chunk}) {
			t.Fatal("write rejected")
		}
	}
	writeID := int64(411)
	write(writeID)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		h.Pump(10)
		for next := h.Next(); next.OK; next = h.Next() {
			event := next.Event
			switch event.Kind {
			case host.ProcessStageIOCompleted:
				if event.ID != writeID {
					continue
				}
				if event.Diagnostic.Code != "" {
					t.Fatalf("broken pipe reported as failure: %+v", event)
				}
				if !event.StageIO.Closed {
					writeID++
					write(writeID)
					continue
				}
				if !h.StartStageIO(host.Request{ID: writeID + 1}, host.StageIO{Graph: graph, Operation: host.StageFinish, Cancelled: true}) {
					t.Fatal("finish rejected")
				}
			case host.ProcessTerminal:
				if event.EffectiveStatus != 0 || event.Outcome != host.ProcessExited || len(event.Stages) != 2 || event.Stages[0].Outcome != host.ProcessCancelled {
					t.Fatalf("closed callable failed the graph: %+v", event)
				}
				return
			}
		}
	}
	t.Fatal("graph did not finish")
}

func TestCallableStageCancellationUnblocksRead(t *testing.T) {
	h := New(nil)
	defer h.Free()
	const graph = 420
	if !h.Start(host.ProcessRequest{
		ID: graph, Topology: true, TimeoutMS: 3000,
		Stages: []host.ProcessStage{{Argv: []string{"/bin/sleep", "30"}}, {Kind: "callable"}},
		Edges:  []host.ProcessEdge{{From: 0, To: 1}},
	}) {
		t.Fatal("graph launch rejected")
	}
	if !h.StartStageIO(host.Request{ID: 421}, host.StageIO{Graph: graph, Index: 1, Operation: host.StageRead}) {
		t.Fatal("read rejected")
	}
	var cancelled, readClosed, graphClosed bool
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		h.Pump(10)
		for next := h.Next(); next.OK; next = h.Next() {
			event := next.Event
			switch event.Kind {
			case host.ProcessSnapshot:
				if !cancelled && len(event.StageStates) > 1 && event.StageStates[1] == 2 {
					cancelled = h.Cancel(graph)
				}
			case host.ProcessStageIOCompleted:
				readClosed = event.ID == 421 && event.StageIO.Closed && event.Diagnostic.Code == ""
			case host.ProcessTerminal:
				graphClosed = event.ID == graph && event.Outcome == host.ProcessCancelled
			}
			if readClosed && graphClosed {
				return
			}
		}
	}
	t.Fatalf("cancelled=%t read closed=%t graph closed=%t", cancelled, readClosed, graphClosed)
}
