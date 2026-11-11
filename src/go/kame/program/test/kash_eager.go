package programcheck

import (
	"kame/core"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
)

func TestSessionKashEagerBindingsFollowFragmentOrderAndExecutionPolicy(t *testing.T) {
	for mode := 0; mode < 3; mode++ {
		a := t.Allocator()
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		fragments := []program.Fragment{
			{Name: "first.kash", Lang: "kash", Text: "let value = 42\n", SkipStatements: mode == 2},
			{Name: "second.km", Lang: "km", Text: "value\n"},
		}
		compiled := program.CompileSession(a, fragments, registry, program.Options{DryRun: mode == 1})
		if compiled.Session == nil {
			t.Fatal("eager session compilation failed")
			return
		}
		s := compiled.Session
		expected := 2
		if mode == 2 {
			expected = 1
		}
		if len(s.Work) != expected {
			t.Error("eager statement ignored fragment execution policy")
		}
		for i := range s.Work {
			start := s.Start(i)
			if start.Handle == nil {
				t.Fatal("eager statement scheduling failed")
				return
			}
			h := start.Handle
			for j := 0; j < 100 && !h.Node.Current && h.Node.Diagnostic.Code == ""; j++ {
				s.Program.Tick(0)
			}
			r := h.Poll()
			if !r.Done {
				t.Error("eager statement did not finish")
			}
			if mode == 2 {
				if r.Result.Diagnostic.Code != "BINDING_UNINITIALIZED" {
					t.Error("skipped eager binding initialized implicitly")
				}
			} else if r.Result.Diagnostic.Code != "" {
				t.Error("eager session failed: " + r.Result.Diagnostic.Code)
			} else if mode == 0 && i == 1 && (r.Result.Value.Kind != core.Int || r.Result.Value.Int != 42) {
				t.Error("later fragment could not read initialized eager slot")
			}
			r.Result.Free(a)
			h.Free()
		}
		if s.Program.Eval.Requests.Next().OK {
			t.Error("pure eager session submitted a host request")
		}
		s.Free()
		compiled.Free(a)
		registry.Free()
	}
}

func TestSessionKashEagerBindingsRejectCrossFragmentCollisions(t *testing.T) {
	pairs := []string{"let value = 1", "value = 1", "value ?= 1", "value += [1]", "def value\n\treturn 0"}
	for i := range pairs {
		for order := 0; order < 2; order++ {
			a := t.Allocator()
			registry := eval.NewRegistry(a)
			texts := []string{"let value = 42", pairs[i]}
			fragments := []program.Fragment{
				{Name: "first.kash", Lang: "kash", Text: texts[order]},
				{Name: "second.kash", Lang: "kash", Text: texts[1-order]},
			}
			if i == 2 || i == 3 {
				fragments[1-order].Lang = "km"
			}
			compiled := program.CompileSession(a, fragments, registry, program.Options{})
			if compiled.Session != nil || len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Code != "DEF_INVALID" {
				t.Error("cross-fragment eager collision accepted")
			}
			if compiled.Session != nil {
				compiled.Session.Free()
			}
			compiled.Free(a)
			registry.Free()
		}
	}
}
