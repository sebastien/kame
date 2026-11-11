package programcheck

import (
	"kame/host/wasm"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
)

func TestStdinRecordWaitIsReleasedOnCancellationAndShutdown(t *testing.T) {
	for mode := 0; mode < 2; mode++ {
		a := t.Allocator()
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		host := countingReadDirHost{memory: wasm.NewMemoryHost(a), recordWaiting: true}
		fragments := []program.Fragment{{Name: "stdin.kash", Lang: "kash", Text: "for line from :stdin :lines\n\t(nop :nil)\n"}}
		compiled := program.CompileSession(a, fragments, registry, program.Options{Host: &host, HasInput: true, InputFD: 7})
		if compiled.Session == nil {
			t.Fatal("stdin session compilation failed")
			return
		}
		s := compiled.Session
		start := s.Start(0)
		if start.Handle == nil {
			t.Fatal("stdin loop not scheduled")
			return
		}
		h := start.Handle
		for i := 0; i < 30 && len(s.Program.RecordRequests) == 0; i++ {
			s.Program.Tick(0)
		}
		if len(s.Program.RecordRequests) != 1 || !host.recordOpen {
			t.Error("stdin wait was not retained as host progress")
		}
		if mode == 0 {
			h.Cancel()
		} else {
			s.Program.Eval.BeginFinalization()
		}
		for i := 0; i < 30; i++ {
			s.Program.Tick(0)
		}
		r := h.Poll()
		if !r.Done || r.Result.Diagnostic.Code != "EXEC_CANCELLED" || len(s.Program.RecordRequests) != 0 || len(s.Program.RecordReaders) != 0 || host.recordOpen || host.recordCloses != 1 {
			t.Error("cancelled stdin wait was replayed, leaked, or not acknowledged")
		}
		r.Result.Free(a)
		h.Free()
		s.Free()
		compiled.Free(a)
		registry.Free()
	}
}

func TestClosedStdinRecordLoopRequiresNoHostReaderOrGrant(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	fragments := []program.Fragment{{Name: "closed.kash", Lang: "kash", Text: "for line from :stdin :lines\n\tbreak\n"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	if compiled.Session == nil {
		t.Fatal("closed stdin session compilation failed")
		return
	}
	s := compiled.Session
	start := s.Start(0)
	if start.Handle == nil {
		t.Fatal("closed stdin loop not scheduled")
		return
	}
	h := start.Handle
	for i := 0; i < 20 && !h.Node.Current && h.Node.Diagnostic.Code == ""; i++ {
		s.Program.Tick(0)
	}
	r := h.Poll()
	if !r.Done || r.Result.Diagnostic.Code != "" || len(s.Program.RecordReaders) != 0 {
		t.Error("closed stdin opened a reader or failed rather than yielding zero items")
	}
	r.Result.Free(a)
	h.Free()
	s.Free()
	compiled.Free(a)
	registry.Free()
}
