package posix_test

import (
	"kame/host"
	"kame/host/posix"
	"solod.dev/so/testing"
)

func TestNativeEventAllocationFailure(t *testing.T) {
	a := t.Allocator()
	// Sweep node, chunk, retention, terminal-copy and diagnostic allocations.
	for after := 0; after < 12; after++ {
		h := posix.New(a)
		r := request(a, 901, "printf output; printf error >&2")
		posix.ForceEventAllocationFailureForTest(after)
		if !h.Start(r) {
			t.Error("event probe unexpectedly failed process setup")
		}
		var terminal posix.Event
		events := drain(t, h, &terminal)
		posix.ForceEventAllocationFailureForTest(-1)
		started, terminals := 0, 0
		for i := range events {
			if events[i].Kind == posix.Started { started++ }
			if events[i].Kind == posix.Terminal { terminals++ }
		}
		if started != 1 || terminals != 1 || h.Active() != 0 {
			t.Error("allocation failure lost or duplicated a mandatory event")
		}
		if terminal.Outcome == posix.Exited && (string(terminal.Stdout) != "output" || string(terminal.Stderr) != "error") {
			t.Error("successful terminal claimed missing capture")
		}
		if terminal.Outcome == posix.Failed && terminal.Diagnostic.Code != posix.DiagnosticHostFailure {
			t.Error("allocation failure lost HOST_FAIL diagnostic")
		}
		if !nativeChildReaped(terminal.PID) { t.Error("allocation failure leaked child ownership") }
		freeEvents(a, events)
		freeRequest(a, &r)
		h.Free()
	}
}

func TestNativeGraphEventAllocationFailure(t *testing.T) {
	a := t.Allocator()
	for after := 0; after < 16; after++ {
		h := posix.New(a)
		stages := []host.ProcessStage{{Argv: []string{"/bin/sh", "-c", "printf output"}}, {Argv: []string{"/bin/cat"}}}
		r := host.ProcessRequest{ID: 902, Stages: stages, Directory: ".", Environment: []string{"PATH=/bin:/usr/bin"}, RetainBytes: 1024}
		posix.ForceEventAllocationFailureForTest(after)
		if !h.Start(r) { t.Error("event probe unexpectedly failed graph setup") }
		var terminal posix.Event
		events := drain(t, h, &terminal)
		posix.ForceEventAllocationFailureForTest(-1)
		started, terminals := 0, 0
		for i := range events {
			if events[i].Kind == posix.Started { started++ }
			if events[i].Kind == posix.Terminal { terminals++ }
		}
		if started != 1 || terminals != 1 || h.Active() != 0 { t.Error("graph allocation failure lost mandatory event") }
		if terminal.Outcome == posix.Exited && (string(terminal.Stdout) != "output" || len(terminal.Stages) != 2) {
			t.Error("successful graph terminal lost capture or stage results")
		}
		if terminal.Outcome == posix.Failed && terminal.Diagnostic.Code != posix.DiagnosticHostFailure { t.Error("graph allocation failure lost diagnostic") }
		if !nativeChildReaped(terminal.PID) { t.Error("graph allocation failure leaked leader ownership") }
		freeEvents(a, events)
		h.Free()
	}
}

func TestNativeRejectedAllocationFailure(t *testing.T) {
	h := posix.New(t.Allocator())
	defer h.Free()
	posix.ForceEventAllocationFailureForTest(0)
	defer posix.ForceEventAllocationFailureForTest(-1)
	if h.Start(posix.Request{}) { t.Error("invalid request accepted") }
	r := h.Next()
	if !r.OK || r.Event.Kind != posix.Terminal || r.Event.Outcome != posix.Failed || r.Event.Diagnostic.Code != posix.DiagnosticHostFailure {
		t.Error("rejected request lost allocation-free terminal")
	}
	if r.OK { r.Event.Free(t.Allocator()) }
	if h.Next().OK { t.Error("duplicate rejected terminal") }
}
