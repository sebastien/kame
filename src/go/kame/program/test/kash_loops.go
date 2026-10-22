package program_test

import (
	"kame/host/wasm"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

type loopOutputState struct{ Count int }

func loopOutputSink(value any, effect eval.Effect) {
	state := value.(*loopOutputState)
	if effect.Kind != eval.EffectOut {
		state.Count += 100
	}
	state.Count++
}

func TestKashLoopsRetainDeferredWritesWithOutputSink(t *testing.T) {
	a := t.Allocator()
	h := wasm.NewMemoryHost(a)
	s := script.Parse(a, "loops.kmk", "SHELL = kash\ndefault :\n\tfor item in [\"first\" \"second\"]\n\t\tif (nop :true)\n\t\t\t(write! (joinpath \".\" (str \"direct-\" item)) item)\n\t\t\twritten = (write! (joinpath \".\" item) item)\n\t\t\t(nop written)\n\t\t(out! item)\n")
	r := eval.NewRegistry(a)
	if !operations.Register(r) {
		t.Error("library registration failed")
	}
	compiled := program.Compile(a, s, r, program.Options{Host: h, Directory: ".", Grants: []eval.Grant{{Capability: eval.Write}}})
	if compiled.Program == nil {
		t.Fatal("loop recipe compile failed")
		compiled.Free(a)
		s.Free()
		r.Free()
		return
	}
	p := compiled.Program
	state := loopOutputState{}
	p.Eval.SetDefinitionEffectSink(loopOutputSink, &state)
	result := p.Materialize("default")
	if result.Diagnostic.Code != "" {
		t.Error("loop recipe failed: " + result.Diagnostic.Code)
	}
	result.Free(a)
	names := []string{"first", "second", "direct-first", "direct-second"}
	expected := []string{"first", "second", "first", "second"}
	for i := range names {
		name := names[i]
		data, err := h.ReadFile(a, name)
		if err != nil || string(data) != expected[i] {
			t.Error("loop deferred write lost bytes or destination: " + name)
		}
		mem.FreeSlice(a, data)
	}
	if state.Count != 2 {
		t.Error("stream sink received deferred writes or replayed output")
	}
	p.Free()
	compiled.Free(a)
	s.Free()
	r.Free()
}
