package program_test

import (
	"kame/core"
	"kame/host/wasm"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestDeferredExpressionsFinalizeAndReleaseRetainedScopes(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	texts := []string{
		"if (nop :true)\n  value = \"retained\"\n  defer (write! ./cleanup value)\n(nop :nil)",
		"defer (write! ./cleanup \"retained\"); defer (fail! \"cleanup failure\"); (fail! \"primary failure\")",
		"call = (kash \"\"\"defer (write! ./cleanup \"retained\"); defer (fail! \"cleanup failure\"); (fail! \"primary failure\")\"\"\"); (call)",
	}
	for scenario := range texts {
		host := wasm.NewMemoryHost(a)
		fragments := []program.Fragment{{Name: "test.kash", Lang: "kash", Text: texts[scenario]}}
		compiled := program.CompileSession(a, fragments, registry, program.Options{Host: host, Directory: ".", Grants: []eval.Grant{{Capability: eval.Write}}})
		if compiled.Session == nil {
			compiled.Free(a)
			t.Fatal("defer session did not compile")
			return
		}
		s := compiled.Session
		failed := false
		for i := 0; i <= len(s.Work); i++ {
			start := s.Start(i)
			if start.Handle == nil {
				t.Error("cannot start statement")
				break
			}
			h := start.Handle
			for tick := 0; tick < 200 && !h.Node.Current && h.Node.State != core.NodeFailed; tick++ {
				s.Program.Tick(0)
			}
			if h.Node.State == core.NodeFailed {
				failed = true
				if h.Node.Diagnostic.Code != "USER_ERROR" || h.Node.Diagnostic.Message != "primary failure" {
					t.Error("cleanup masked primary failure")
				}
			} else if !h.Node.Current {
				t.Error("statement/finalization did not finish")
			}
			h.Free()
			if failed {
				break
			}
		}
		s.Program.Eval.BeginFinalization()
		for tick := 0; tick < 200 && !s.Program.Eval.AdvanceFinalization(); tick++ {
			s.Program.Tick(0)
		}
		if !s.Program.Eval.AdvanceFinalization() {
			t.Error("external finalization did not complete")
		}
		data, err := host.ReadFile(a, "cleanup")
		if err != nil || string(data) != "retained" {
			t.Error("deferred write lost scope or failed to run")
		}
		mem.FreeSlice(a, data)
		if (scenario != 0) != failed {
			t.Error("unexpected primary result")
		}
		s.Free()
		compiled.Free(a)
	}
}
