package programcheck

import (
	"kame/host/wasm"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"strings"
)

func TestMixedKashLibraryFragments(t *testing.T) {
	a := t.Allocator()
	r := eval.NewRegistry(a)
	operations.Register(r)
	sources := []program.CompileSource{
		{Name: "lib.kash", Lang: "kash", Text: "def helper ()\n\t(write! \"./proof\" \"ok\")\n"},
		{Name: "Kamefile", Lang: "km", Text: "SHELL = kash\ntask default\n\t(helper ())\n"},
	}
	h := wasm.NewMemoryHost(a)
	compiled := program.CompileMany(a, sources, r, program.Options{Host: h, Directory: ".", Grants: []eval.Grant{{Capability: eval.Write}}})
	if compiled.Program == nil {
		t.Fatal("mixed Kash and rule sources failed to compile")
		compiled.Free(a)
		r.Free()
		return
	}
	p := compiled.Program
	result := p.Materialize("default")
	data, err := h.ReadFile(a, "proof")
	if result.Diagnostic.Code != "" || err != nil || string(data) != "ok" {
		t.Error("rule could not call included Kash definition: " + result.Diagnostic.Code)
	}
	mem.FreeSlice(a, data)
	result.Free(a)
	p.Free()
	compiled.Free(a)
	r.Free()
}

func TestKashLoopsRetainDeferredWritesWithOutputSink(t *testing.T) {
	a := t.Allocator()
	h := wasm.NewMemoryHost(a)
	s := script.ParseKameFile(a, "loops.km", "task default\n\tfor item in [\"first\", \"second\"]\n\t\tif true\n\t\t\t(write! (joinpath \".\" (str \"direct-\" item)) item)\n\t\t\tdef written = (write! (joinpath \".\" item) item)\n\t\t\t(nop written)\n\t\t(out! item)\n")
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
	definitionOutput := false
	p.Eval.SetDefinitionEffectSink(func(any, eval.Effect) { definitionOutput = true }, nil)
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
	var output strings.Builder
	for next := p.NextEvent(); next.OK; next = p.NextEvent() {
		if next.Event.Kind == program.Stdout && next.Event.Target == "default" {
			output.Write(next.Event.Data)
		}
		next.Event.Free(a)
	}
	if definitionOutput || output.String() != "firstsecond" {
		t.Error("loop stdout was lost, replayed, or sent to the standalone definition sink")
	}
	p.Free()
	compiled.Free(a)
	s.Free()
	r.Free()
}
