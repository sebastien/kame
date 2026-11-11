package programcheck

import (
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/program"
)

func TestSuppliedInvocationInputDisablesExecutionReuse(t *testing.T) {
	a := t.Allocator()
	for i := 0; i < 2; i++ {
		registry := eval.NewRegistry(a)
		fragments := []program.Fragment{{Name: "input.kash", Text: "", Lang: "kash"}}
		compiled := program.CompileSession(a, fragments, registry, program.Options{HasInput: i == 1, InputFD: 7})
		if compiled.Session == nil {
			t.Fatal("input session compilation failed")
			return
		}
		p := compiled.Session.Program
		if p.Options.HasInput != (i == 1) || p.Eval.InputSupplied != (i == 1) || p.Options.CacheDisabled != (i == 1) || p.Options.Force != (i == 1) || p.Options.InputFD != 7 {
			t.Error("borrowed input was lost or allowed execution reuse")
		}
		compiled.Session.Free()
		compiled.Free(a)
		registry.Free()
	}
}
