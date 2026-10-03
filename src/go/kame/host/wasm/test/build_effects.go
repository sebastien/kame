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

func TestForwardedFileRecipesVerifyOutputsBeforeCompletion(t *testing.T) {
 a := t.Allocator()
 for scenario := 0; scenario < 4; scenario++ {
  text := "./out :\n\ttrue\n"
  if scenario == 0 { text = "./out :\n\t@(out \"\")\n" }
  started := wasm.NewRuntime(a, text)
  if started.Runtime == nil { t.Fatal("create output verification runtime"); return }
  r := started.Runtime
  r.SetForwarding(true)
  begin := r.RequestTarget("./out")
  if begin.Code != "" { begin.Free(a); r.Free(); t.Fatal("request file rule"); return }
  begin.Free(a)
  processes, checks := 0, 0
  for step := 0; step < 128; step++ {
   next := r.Step()
   if next.OK {
    probe := r.Result()
    if probe.Done { t.Error("file rule completed before host verification") }
    probe.Free(a)
    if next.Request.Kind == host.RequestProcess {
     processes++
     if host.PayloadText(next.Request.Payload, host.FieldOp) != "recipe" { t.Error("file recipe omitted output descriptor") }
     r.Complete(next.Request, core.Value{Kind: core.Nil}, diagnostic.Diagnostic{})
    } else {
     checks++
     if next.Request.Kind != host.RequestReadFile || host.PayloadText(next.Request.Payload, host.FieldOp) != host.OpOutputExists { t.Error("invalid output verification request") }
     d := diagnostic.Diagnostic{}
     if scenario == 3 { d = diagnostic.Diagnostic{Code: "FS_ERR", Severity: diagnostic.Error, Message: "host output check failed"} }
     r.Complete(next.Request, core.Value{Kind: core.Bool, Bool: scenario == 2}, d)
    }
    next.Request.Free(a)
   }
   probe := r.Result()
   done := probe.Done
   probe.Free(a)
   if done { break }
  }
  result := r.Result()
  wanted := "OUTPUT_MISSING"
  if scenario == 2 { wanted = "" } else if scenario == 3 { wanted = "FS_ERR" }
  if !result.Done || result.Diagnostic.Code != wanted || checks != 1 || (scenario == 0 && processes != 0) || (scenario != 0 && processes != 1) { t.Error("file rule ignored host output verification") }
  result.Free(a)
  r.Free()
 }
}
