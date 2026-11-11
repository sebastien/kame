package wasmcheck

import (
	"kame/host/wasm"
	"kame/internal/mem"
	"kame/internal/testing"
)

func TestTargetSnapshotReadsUseExplicitCapabilityPolicy(t *testing.T) {
	_ = t.Allocator()
	buffer := make([]byte, 1024*1024)
	defer mem.FreeSlice(nil, buffer)
	heap := wasm.NewHeap(buffer)
	started := wasm.NewRuntimeWithHeap(&heap, "policy.kmk", "chosen : ; [shell: kash]\n\t(out! (env \"MODE\"))\n")
	if started.Runtime == nil {
		t.Fatal("compile policy recipe")
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	runtime.SetEnvironment("MODE", "secret")
	runtime.InspectionGrant("", "")
	begin := runtime.RequestTarget("chosen")
	if begin.Code != "" {
		t.Error("request policy recipe failed")
		begin.Free(runtime.Alloc)
		return
	}
	begin.Free(runtime.Alloc)
	for step := 0; step < 32; step++ {
		next := runtime.Step()
		if next.OK {
			next.Request.Free(runtime.Alloc)
			t.Error("denied snapshot read submitted a host request")
			return
		}
		result := runtime.Result()
		done := result.Done
		if done && result.Diagnostic.Code != "CAP_DENIED" {
			t.Error("target snapshot read ignored explicit empty grants")
		}
		result.Free(runtime.Alloc)
		if done {
			return
		}
	}
	t.Error("denied snapshot read did not finish")
}
