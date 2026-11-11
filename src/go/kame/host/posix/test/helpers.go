package posixcheck

import (
	"kame/host/posix"
	"kame/internal/mem"
	"kame/internal/testing"
	"slices"
	"time"
)

const waitBudget = 5 * time.Second

func request(a mem.Allocator, id int64, script string) posix.Request {
	return posix.Request{ID: id, Shell: slices.Clone([]string{"/bin/sh", "-c"}), Script: slices.Clone([]byte(script)), Directory: ".", Environment: slices.Clone([]string{"PATH=/usr/bin:/bin"}), RetainBytes: 1024}
}

func freeRequest(a mem.Allocator, r *posix.Request) {
	mem.FreeSlice(nil, r.Shell)
	mem.FreeSlice(nil, r.Script)
	mem.FreeSlice(nil, r.Environment)
	*r = posix.Request{}
}

func drain(t *testing.T, h *posix.Host, terminal *posix.Event) []posix.Event {
	return drainFor(t, h, terminal, 0)
}
func drainFor(t *testing.T, h *posix.Host, terminal *posix.Event, id int64) []posix.Event {
	var events []posix.Event
	deadline := time.Now().Add(waitBudget)
	for terminal.Kind != posix.Terminal && time.Now().Before(deadline) {
		if !h.Pump(10) {
			t.Error("host pump failed")
			break
		}
		for {
			result := h.Next()
			if !result.OK {
				break
			}
			events = append(events, result.Event)
			if result.Event.Kind == posix.Terminal && (id == 0 || result.Event.ID == id) {
				*terminal = result.Event
			}
		}
	}
	if terminal.Kind != posix.Terminal {
		t.Error("timed out waiting for terminal event")
	}
	return events
}

func freeEvents(a mem.Allocator, events []posix.Event) {
	for i := range events {
		events[i].Free(a)
	}
	mem.FreeSlice(nil, events)
}
