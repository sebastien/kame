package wasm_test

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/wasm"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestStructuredRecipeAsyncUsesInstanceHeap(t *testing.T) {
	a := t.Allocator()
	buffer := slices.Make[byte](a, 1024*1024)
	defer slices.Free(a, buffer)
	heap := wasm.NewHeap(buffer)
	started := wasm.NewRuntimeWithHeap(&heap, "recipe.kmk", "chosen : ; [shell: kash]\n\t/bin/sh -c \"printf async\" &\n")
	if started.Runtime == nil {
		t.Fatal("compile async recipe")
		return
	}
	r := started.Runtime
	defer r.Free()
	r.SetForwarding(true)
	r.SetEnvironment("MODE", "scoped")
	begin := r.RequestTarget("chosen")
	if begin.Code != "" {
		begin.Free(a)
		t.Fatal("request async recipe")
		return
	}
	begin.Free(a)
	processes := 0
	for step := 0; step < 128; step++ {
		next := r.Step()
		if next.OK {
			if next.Request.Kind == host.RequestProcess {
				processes++
				r.ProcessTerminal(next.Request, nil, nil, 0, 0, 0, "", "")
			} else {
				r.Complete(next.Request, core.Value{Kind: core.Nil}, diagnostic.Diagnostic{})
			}
			next.Request.Free(r.Alloc)
		}
		result := r.Result()
		done := result.Done
		if done && result.Diagnostic.Code != "" {
			t.Error("async recipe failed: " + result.Diagnostic.Code)
		}
		result.Free(r.Alloc)
		if done {
			if processes != 1 {
				t.Error("async recipe did not launch exactly once")
			}
			return
		}
	}
	t.Error("async recipe did not complete")
}
