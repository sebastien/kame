package wasm_test

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/wasm"
	"solod.dev/so/testing"
)

func TestForwardedDeclaredFileInputsUseEmbeddingFilesystem(t *testing.T) {
	a := t.Allocator()
	for scenario := 0; scenario < 2; scenario++ {
		started := wasm.NewRuntime(a, "default : ./input\n\t@(out \"ok\")\n")
		if started.Runtime == nil {
			t.Fatal("create runtime")
			return
		}
		r := started.Runtime
		r.SetForwarding(true)
		preflight := r.RequestTarget("default")
		if preflight.Code != "" {
			t.Error("request target")
			preflight.Free(a)
			r.Free()
			return
		}
		preflight.Free(a)
		requests := 0
		for step := 0; step < 64; step++ {
			next := r.Step()
			if next.OK {
				requests++
				if next.Request.Kind != host.RequestReadFile || host.PayloadText(next.Request.Payload, "op") != host.OpFileContent {
					t.Error("declared file did not request host content")
				}
				value := core.Value{Kind: core.Nil}
				if scenario == 0 {
					value = core.NewBytes(a, []byte("input"))
				}
				r.Complete(next.Request, value, diagnostic.Diagnostic{})
				next.Request.Free(a)
			}
			probe := r.Result()
			done := probe.Done
			if done && ((scenario == 0 && probe.Diagnostic.Code != "") || (scenario == 1 && probe.Diagnostic.Code != "TGT_NO_RULE")) {
				t.Error("embedding file existence was ignored")
			}
			probe.Free(a)
			if done {
				break
			}
		}
		result := r.Result()
		if !result.Done || requests != 1 {
			t.Error("declared file did not complete with one request")
		}
		result.Free(a)
		r.Free()
	}
}
