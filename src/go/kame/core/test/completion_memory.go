package core_test

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestCompletionTransfersLargeValueWithoutCopy(t *testing.T) {
	a := t.Allocator()
	tracker := a.(*mem.Tracker)
	e := core.NewEngine(a)
	defer e.Free()
	value := core.Value{Kind: core.Bytes, Bytes: mem.AllocSlice[byte](a, 1024*1024, 1024*1024)}
	before := tracker.Stats().TotalAlloc
	e.Complete(core.Completion{NodeID: 999, Value: value, HasValue: true})
	if tracker.Stats().TotalAlloc-before >= 1024*1024 {
		t.Error("completion copied the large payload instead of taking ownership")
	}
	e.Step() // A stale completion must still release its transferred payload.
}
