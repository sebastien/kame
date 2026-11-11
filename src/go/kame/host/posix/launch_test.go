package posix

import (
	"kame/host"
	"strings"
	"testing"
	"time"
)

func TestFailedLaunchReleasesBackpressuredOutput(t *testing.T) {
	// E2BIG occurs during actual process launch, after an earlier stage can
	// already have filled the host's event queue. No event consumer runs until
	// Start returns; process retirement must still complete.
	oversized := "KAME_LARGE=" + strings.Repeat("x", 2*1024*1024)
	for attempt := 0; attempt < 4; attempt++ {
		h := New(nil)
		done := make(chan struct{})
		go func() {
			h.Start(host.ProcessRequest{ID: 8002, Environment: []string{"PATH=/bin:/usr/bin"}, Stages: []host.ProcessStage{
				{Argv: []string{"/bin/sh", "-c", "head -c 4194304 /dev/zero >&2; sleep 30"}},
				{Argv: []string{"/bin/sh", "-c", "exit 0"}, Environment: []string{oversized}},
			}})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			h.Free()
			t.Fatal("failed launch waited for a consumer of its output events")
		}
		if h.Active() != 0 {
			t.Error("failed launch retained an active process")
		}
		terminal := h.Next()
		if !terminal.OK || terminal.Event.Kind != host.ProcessTerminal || terminal.Event.Outcome != host.ProcessFailed {
			t.Errorf("failed launch lost its terminal diagnostic: %+v", terminal)
		}
		h.Free()
	}
}

func TestCompletedStageDoesNotTimeOutWhilePeerRuns(t *testing.T) {
	h := New(nil)
	defer h.Free()
	if !h.Start(host.ProcessRequest{ID: 8003, TimeoutMS: 2000, Stages: []host.ProcessStage{
		{Argv: []string{"/bin/sh", "-c", "sleep 0.3"}},
		{Argv: []string{"/bin/true"}, TimeoutMS: 100},
	}}) {
		t.Fatal("cannot start pipeline")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.Pump(10)
		for next := h.Next(); next.OK; next = h.Next() {
			if next.Event.Kind == host.ProcessTerminal {
				if next.Event.Outcome != host.ProcessExited || next.Event.Status != 0 {
					t.Fatalf("completed stage expired while waiting for its peer: %+v", next.Event)
				}
				return
			}
		}
	}
	t.Fatal("pipeline did not finish")
}
