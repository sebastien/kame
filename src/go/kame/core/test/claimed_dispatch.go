package corecheck

import (
	"kame/core"
	"kame/internal/mem"
	"kame/internal/testing"
)

func TestCancelledHostClaimCannotDispatch(t *testing.T) {
	a := t.Allocator()
	state := mem.Alloc[producerState](a)
	defer mem.Free(a, state)
	state.Alloc, state.Text = a, "must-not-run"
	e := core.NewEngine(a)
	defer e.Free()
	n := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "cancelled"}, publishOnce, state)
	e.Request(n)
	ready := e.Ready(1)
	defer mem.FreeSlice(nil, ready)
	if len(ready) != 1 || !e.CanDispatch(n) {
		t.Error("live work was not claimed")
		return
	}
	e.Cancel(n)
	if e.CanDispatch(n) || e.Dispatch(n) != nil || state.Runs != 0 || n.State != core.NodeCancelled {
		t.Error("a cancelled host claim executed its producer")
	}
}
