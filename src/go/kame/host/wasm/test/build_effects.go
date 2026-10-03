package wasm_test

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/wasm"
	"solod.dev/so/testing"
)

func TestForwardedBuildWritesWaitForHostAndPreserveBytes(t *testing.T) {
	a := t.Allocator()
	for scenario := 0; scenario < 2; scenario++ {
		started := wasm.NewRuntime(a, "./out :\n\t@(write \"./side\" \"side\")\n\t@(yield \"one\")\n\t@(yield \"two\")\n")
		if started.Runtime == nil {
			t.Fatal("create runtime")
			return
		}
		r := started.Runtime
		r.SetForwarding(true)
		preflight := r.RequestTarget("./out")
		if preflight.Code != "" {
			t.Error("request target")
			preflight.Free(a)
			r.Free()
			return
		}
		preflight.Free(a)
		requests := 0
		for step := 0; step < 128; step++ {
			next := r.Step()
			if next.OK {
				requests++
				data := string(host.PayloadBytes(next.Request.Payload, "data"))
				if next.Request.Kind != host.RequestWriteFile || (requests == 1 && data != "side") || (requests == 2 && data != "onetwo") {
					t.Error("forwarded effect bytes or ordering")
				}
				probe := r.Result()
				if probe.Done {
					t.Error("target completed before host write")
				}
				probe.Free(a)
				d := diagnostic.Diagnostic{}
				if scenario == 1 {
					d = diagnostic.Diagnostic{Code: "FS_ERR", Severity: diagnostic.Error, Message: "host write failed"}
				}
				r.Complete(next.Request, core.Value{Kind: core.Nil}, d)
				next.Request.Free(a)
			}
			probe := r.Result()
			done := probe.Done
			probe.Free(a)
			if done {
				break
			}
		}
		result := r.Result()
		if !result.Done || (scenario == 0 && (requests != 2 || result.Diagnostic.Code != "")) || (scenario == 1 && (requests != 1 || result.Diagnostic.Code != "FS_ERR")) {
			t.Error("host write completion or failure was ignored")
		}
		result.Free(a)
		r.Free()
	}
}
