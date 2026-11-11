//go:build linux

package posixcheck

import (
	"kame/host"
	"kame/host/posix"
	"kame/internal/testing"
)

func TestNativeMandatoryEvents(t *testing.T) {
	a := t.Allocator()
	// Repeated completion must preserve exactly one start and terminal event.
	for after := 0; after < 12; after++ {
		h := posix.New(a)
		r := request(a, 901, "printf output; printf error >&2")
		if !h.Start(r) {
			t.Error("event probe unexpectedly failed process setup")
		}
		var terminal posix.Event
		events := drain(t, h, &terminal)
		started, terminals := 0, 0
		for i := range events {
			if events[i].Kind == posix.Started {
				started++
			}
			if events[i].Kind == posix.Terminal {
				terminals++
			}
		}
		if started != 1 || terminals != 1 || h.Active() != 0 {
			t.Error("event delivery lost or duplicated a mandatory event")
		}
		if terminal.Outcome == posix.Exited && (string(terminal.Stdout) != "output" || string(terminal.Stderr) != "error") {
			t.Error("successful terminal claimed missing capture")
		}
		if terminal.Outcome == posix.Failed && terminal.Diagnostic.Code != posix.DiagnosticHostFailure {
			t.Error("event delivery lost HOST_FAIL diagnostic")
		}
		if !nativeChildReaped(terminal.PID) {
			t.Error("event delivery leaked child ownership")
		}
		freeEvents(a, events)
		freeRequest(a, &r)
		h.Free()
	}
}

func TestNativeGraphMandatoryEvents(t *testing.T) {
	a := t.Allocator()
	for after := 0; after < 16; after++ {
		h := posix.New(a)
		stages := []host.ProcessStage{{Argv: []string{"/bin/sh", "-c", "printf output"}}, {Argv: []string{"/bin/cat"}}}
		r := host.ProcessRequest{ID: 902, Stages: stages, Directory: ".", Environment: []string{"PATH=/bin:/usr/bin"}, RetainBytes: 1024}
		if !h.Start(r) {
			t.Error("event probe unexpectedly failed graph setup")
		}
		var terminal posix.Event
		events := drain(t, h, &terminal)
		started, terminals := 0, 0
		for i := range events {
			if events[i].Kind == posix.Started {
				started++
			}
			if events[i].Kind == posix.Terminal {
				terminals++
			}
		}
		if started != 1 || terminals != 1 || h.Active() != 0 {
			t.Error("graph event delivery lost mandatory event")
		}
		if terminal.Outcome == posix.Exited && (string(terminal.Stdout) != "output" || len(terminal.Stages) != 2) {
			t.Error("successful graph terminal lost capture or stage results")
		}
		if terminal.Outcome == posix.Failed && terminal.Diagnostic.Code != posix.DiagnosticHostFailure {
			t.Error("graph event delivery lost diagnostic")
		}
		if !nativeChildReaped(terminal.PID) {
			t.Error("graph event delivery leaked leader ownership")
		}
		freeEvents(a, events)
		h.Free()
	}
}

func TestNativeRejectedMandatoryEvent(t *testing.T) {
	h := posix.New(t.Allocator())
	defer h.Free()
	if h.Start(posix.Request{}) {
		t.Error("invalid request accepted")
	}
	r := h.Next()
	if !r.OK || r.Event.Kind != posix.Terminal || r.Event.Outcome != posix.Failed || r.Event.Diagnostic.Code != posix.DiagnosticHostFailure {
		t.Error("rejected request lost terminal")
	}
	if r.OK {
		r.Event.Free(t.Allocator())
	}
	if h.Next().OK {
		t.Error("duplicate rejected terminal")
	}
}
