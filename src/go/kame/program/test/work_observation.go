package programcheck

import (
	"bytes"
	"io"
	"kame/core"
	"kame/host/wasm"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/program"
)

var workTrace *bytes.Buffer

func traceDispatch(node *core.Node, target string, started bool) {
	if started && node != nil {
		io.WriteString(workTrace, target+"\n")
	}
}

func freeWorkTrace(a mem.Allocator) {
	workTrace.Reset()
	mem.Free(a, workTrace)
	workTrace = nil
}

func dispatchTrace(t *testing.T, prepared bool) string {
	a := t.Allocator()
	workTrace = mem.Alloc[bytes.Buffer](a)
	*workTrace = bytes.Buffer{}
	defer freeWorkTrace(a)
	source := script.Parse(a, "work.kmk", "task root : a b\ntask a :\ntask b :\n")
	defer source.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	compiled := program.Compile(a, source, registry, program.Options{Host: wasm.NewMemoryHost(a), Directory: ".", Jobs: 3})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Error("compile failed")
		return ""
	}
	p := compiled.Program
	defer p.Free()
	p.ObserveWork = traceDispatch
	start := p.Start("root")
	if start.Diagnostic.Code != "" {
		start.Diagnostic.Free(a)
		t.Error("root failed to start")
		return ""
	}
	defer start.Handle.Free()
	done := false
	for i := 0; i < 1000; i++ {
		if prepared {
			before := workTrace.Len()
			first := p.PrepareWork()
			again := p.PrepareWork()
			if first.Node != again.Node || first.Target != again.Target || workTrace.Len() != before {
				t.Error("preparation dispatched work or changed identity")
			}
		}
		p.Tick(0)
		result := start.Handle.Poll()
		done = result.Done
		result.Result.Free(a)
		if done {
			break
		}
	}
	if !done {
		t.Error("dispatch did not finish")
	}
	return core.NewString(a, workTrace.String()).Text
}

func TestPreparedWorkRetainsBatchDispatchOrder(t *testing.T) {
	a := t.Allocator()
	ordinary := dispatchTrace(t, false)
	defer mem.FreeString(a, ordinary)
	prepared := dispatchTrace(t, true)
	defer mem.FreeString(a, prepared)
	if ordinary == "" || ordinary != prepared {
		t.Error("presentation boundary changed deterministic dispatch order")
	}
}
