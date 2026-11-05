package program_test

import (
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

func TestSessionExitIsTypedAndStopsLaterFragments(t *testing.T) {
	texts := []string{
		"exit 23\n(out! \"forbidden\")",
		"defn stop\n\texit 23\n(stop) ?? \"fallback\"\n(out! \"forbidden\")",
		"runner = (kash \"exit 23\")\n(? (runner) \"fallback\")\n(out! \"forbidden\")",
		"defn stop\n\texit 23\nif (stop)\n\t(out! \"forbidden\")\n",
	}
	for i := range texts {
		a := t.Allocator()
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		fragments := []program.Fragment{{Name: "first.kash", Lang: "kash", Text: texts[i]}, {Name: "second.km", Lang: "km", Text: "(out! \"forbidden\")"}}
		compiled := program.CompileSession(a, fragments, registry, program.Options{})
		if compiled.Session == nil {
			t.Fatal("exit session compilation failed")
			return
		}
		s := compiled.Session
		start := s.Start(0)
		if start.Handle == nil {
			t.Fatal("exit statement not scheduled")
			return
		}
		h := start.Handle
		var result program.HandleResult
		for j := 0; j < 100; j++ {
			s.Program.Tick(0)
			result = h.Poll()
			if result.Done {
				break
			}
		}
		if !result.Done || !result.Result.Exit.Requested || result.Result.Exit.Status != 23 || result.Result.Diagnostic.Code != "" {
			t.Error("exit was lost or exposed as an ordinary failure")
		}
		result.Result.Free(a)
		h.Free()
		later := s.Start(len(s.Work) - 1)
		if later.Handle != nil || later.Diagnostic.Code != "PHASE_INVALID" {
			t.Error("terminated session accepted later work")
		}
		later.Diagnostic.Free(a)
		if later.Handle != nil {
			later.Handle.Free()
		}
		s.Free()
		compiled.Free(a)
		registry.Free()
	}
}
