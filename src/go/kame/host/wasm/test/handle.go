package wasm_test

import (
	"kame/host/wasm"
	"solod.dev/so/testing"
)

func TestTableRepeatedRetirementPreservesLiveHandles(t *testing.T) {
	table := wasm.NewTable(t.Allocator())
	defer table.FreeTable()
	active := table.Add(1, 10)
	parked := table.Add(1, 20)
	var late wasm.Handle
	for i := 0; i < 100000; i++ {
		next := table.Add(1, uint64(i)+30)
		if uint64(next)&0xffffffff > 4 {
			t.Error("retired requests grew table history")
			return
		}
		if late != 0 {
			if !table.Free(1, late) {
				t.Error("could not retire previous completion")
			}
			if _, ok := table.Get(1, late); ok {
				t.Error("retired generation resolved again")
			}
		}
		late = next
		if value, ok := table.Get(1, active); !ok || value != 10 {
			t.Error("retirement invalidated active request")
		}
		if value, ok := table.Get(1, parked); !ok || value != 20 {
			t.Error("retirement invalidated parked request")
		}
		if _, ok := table.Get(2, next); ok || table.Free(2, next) {
			t.Error("foreign owner accessed reusable request")
		}
	}
}
