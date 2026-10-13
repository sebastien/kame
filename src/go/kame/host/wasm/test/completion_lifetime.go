package wasm_test

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host/wasm"
	"solod.dev/so/testing"
)

func TestRuntimeAcceptsOwnedFailureAfterInputReuse(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "")
	if started.Runtime == nil {
		t.Fatal("runtime did not compile")
		started.Result.Free(a)
		return
	}
	r := started.Runtime
	defer r.Free()
	scheduled := r.RequestExpression("(exists? \"x\")")
	if scheduled.Code != "" {
		t.Fatal("expression did not schedule")
		scheduled.Free(a)
		return
	}
	scheduled.Free(a)
	next := r.Step()
	if !next.OK {
		t.Fatal("expression did not yield")
		return
	}
	code := []byte("CUSTOM_FAIL")
	message := []byte("original host message")
	d := diagnostic.Diagnostic{Severity: diagnostic.Error, Code: string(code), Message: string(message)}
	// Mirrors the ABI's allocator-owned transfer, not a borrowed Runtime input.
	r.Complete(next.Request, core.Value{}, d.Clone(a))
	next.Request.Free(a)
	clear(code)
	clear(message)
	for i := 0; i < 10; i++ {
		_ = r.Step()
		result := r.Result()
		if result.Done {
			if result.Diagnostic.Code != "CUSTOM_FAIL" || result.Diagnostic.Message != "original host message" {
				t.Error("owned completion changed after input reuse")
			}
			result.Free(a)
			return
		}
		result.Free(a)
	}
	t.Error("failure did not reach terminal result")
}
