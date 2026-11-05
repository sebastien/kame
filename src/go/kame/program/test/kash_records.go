package program_test

import (
	"kame/core"
	"kame/host/wasm"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type recordLoopTestState struct {
	Program       *program.Program
	Calls         int
	Sources       int
	MaximumStates int
	MaximumNodes  int
	MaximumScopes int
}

func recordLoopSource(c *eval.Context, state any, args []core.Value) eval.Result {
	_ = args
	s := state.(*recordLoopTestState)
	s.Sources++
	return eval.Result{Value: core.NewString(c.Run, "items")}
}

func recordLoopCheck(c *eval.Context, state any, args []core.Value) eval.Result {
	_ = c
	s := state.(*recordLoopTestState)
	s.Calls++
	if args[0].Kind != core.String || args[0].Text != "value" {
		return eval.Result{Diagnostic: core.Diagnostic{Code: "BAD_RECORD"}}
	}
	if len(s.Program.Eval.OperationStates) > s.MaximumStates {
		s.MaximumStates = len(s.Program.Eval.OperationStates)
	}
	if len(s.Program.Eval.DeferScopes) > s.MaximumScopes {
		s.MaximumScopes = len(s.Program.Eval.DeferScopes)
	}
	nodes := s.Program.Engine.TrackedNodes()
	if len(nodes) > s.MaximumNodes {
		s.MaximumNodes = len(nodes)
	}
	slices.Free(s.Program.Alloc, nodes)
	return eval.Result{}
}

func TestKashFileRecordLoopsRetireCompletedScopeAndOperationHistory(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := recordLoopTestState{}
	registry.Add(eval.Operation{Name: "record-source", Context: &state, Call: recordLoopSource, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "record-check", Context: &state, Call: recordLoopCheck, MinArity: 1, MaxArity: 1})
	memory := wasm.NewMemoryHost(a)
	data := slices.Make[byte](a, 512*6)
	for i := 0; i < 512; i++ {
		copy(data[i*6:], []byte("value\n"))
	}
	memory.SetFile("items", data)
	slices.Free(a, data)
	fragments := []program.Fragment{{Name: "records.kash", Lang: "kash", Text: "for line from (record-source) :lines\n\twith\n\t\tlet local = line\n\t\tdefer (record-check local)\n\t\tcontinue\n"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{Host: memory, Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Session == nil {
		t.Fatal("record session compilation failed")
		return
	}
	s := compiled.Session
	state.Program = s.Program
	start := s.Start(0)
	if start.Handle == nil {
		t.Fatal("record loop not scheduled")
		return
	}
	h := start.Handle
	for i := 0; i < 40000 && !h.Node.Current && h.Node.Diagnostic.Code == ""; i++ {
		s.Program.Tick(0)
	}
	r := h.Poll()
	if !r.Done || r.Result.Diagnostic.Code != "" || state.Calls != 512 || state.Sources != 1 || len(memory.Readers) != 0 {
		t.Errorf("record iteration failed: calls=%d sources=%d diagnostic=%s", state.Calls, state.Sources, r.Result.Diagnostic.Code)
	}
	if state.MaximumStates > 32 || state.MaximumNodes > 32 || state.MaximumScopes > 4 {
		t.Errorf("record execution retained history: operations=%d nodes=%d scopes=%d", state.MaximumStates, state.MaximumNodes, state.MaximumScopes)
	}
	r.Result.Free(a)
	h.Free()
	s.Free()
	compiled.Free(a)
	registry.Free()
}

func TestKashFileRecordReaderClosesAfterTransitiveDependencyFailure(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	memory := wasm.NewMemoryHost(a)
	memory.SetFile("items", []byte("one\ntwo\n"))
	fragments := []program.Fragment{{Name: "failure.kash", Lang: "kash", Text: "bad = (fail! \"boom\")\nfor line from ./items :lines\n\tlet unused = bad\n"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{Host: memory, Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Session == nil {
		t.Fatal("record session compilation failed")
		return
	}
	s := compiled.Session
	start := s.Start(0)
	if start.Handle == nil {
		t.Fatal("record loop not scheduled")
		return
	}
	h := start.Handle
	for i := 0; i < 200 && !h.Node.Current && h.Node.Diagnostic.Code == ""; i++ {
		s.Program.Tick(0)
	}
	s.Program.Tick(0)
	r := h.Poll()
	if !r.Done || r.Result.Diagnostic.Code == "" || len(memory.Readers) != 0 || len(s.Program.RecordReaders) != 0 {
		t.Error("transitive failure left a record reader open before boundary disposal")
	}
	r.Result.Free(a)
	h.Free()
	s.Free()
	compiled.Free(a)
	registry.Free()
}
