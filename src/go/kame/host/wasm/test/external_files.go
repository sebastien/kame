package wasmcheck

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/wasm"
	"kame/internal/testing"
)

func TestForwardedDeclaredFileInputsUseEmbeddingFilesystem(t *testing.T) {
	a := t.Allocator()
	for scenario := 0; scenario < 2; scenario++ {
		started := wasm.NewRuntime(a, "default : ./input ; [shell: kash]\n\t(out! \"ok\")\n")
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
		wantRequests := 1
		if scenario == 0 {
			wantRequests++ // Successful production revalidates the consumed input.
		}
		if !result.Done || requests != wantRequests {
			t.Error("declared file did not complete with the expected validation reads")
		}
		result.Free(a)
		r.Free()
	}
}
