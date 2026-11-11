package wasmcheck

import (
	"bytes"
	"kame/cli"
	"kame/host/wasm"
	"kame/internal/mem"
	"kame/internal/testing"
)

func TestWasmPresentationUsesNativeLayout(t *testing.T) {
	a := t.Allocator()
	snapshot := `{"op":"frame","command":"build","subject":"dist","width":80,"height":12,"elapsedMS":1200,"counts":{"completed":8},"workers":[{"target":"./build/main.o","program":"cc","elapsedMS":1200}]}`
	result := wasm.RenderPresentation(a, snapshot)
	defer result.Free(a)
	var direct = bytes.Buffer{}
	defer direct.Reset()
	workers := []cli.ProgressWorkerView{{Target: "./build/main.o", Program: "cc", ElapsedMS: 1200}}
	cli.WriteDashboard(&direct, cli.ProgressView{Command: "build", Subject: "dist", Width: 80, Height: 12, ElapsedMS: 1200, Counts: cli.Progress{Completed: 8}, Workers: workers})
	if result.Code != "" || result.Text != direct.String() {
		t.Error("WASM snapshot does not use the native presenter")
	}
}

func TestWasmPresentationOwnsInvocationState(t *testing.T) {
	_ = t.Allocator()
	buffer := make([]byte, 64*1024)
	defer mem.FreeSlice(nil, buffer)
	heap := wasm.NewHeap(buffer)
	p := wasm.NewPresentation(&heap)
	started := `{"type":"target-started","node":7,"generation":1,"target":"root","resource":{"kind":"task","name":"root"}}`
	done := `{"type":"target-completed","node":7,"generation":1,"target":"root","resource":{"kind":"task","name":"root"}}`
	first := p.Observe(started, 5, 1000000000)
	first.Free(mem.System)
	history := p.Observe(`{"type":"target-estimate","node":7,"generation":1,"estimateMS":1000,"resource":{"kind":"task","name":"root"}}`, 5, 1000000000)
	history.Free(mem.System)
	estimate := p.Render(`{"op":"estimate","nowNS":1500000000}`)
	if estimate.Code != "" || estimate.Text != `{"known":true,"remainingMS":500,"percent":50}` || p.State.Counts.Active != 1 {
		t.Error("advisory estimate query changed lifecycle state or lost historical wall time")
	}
	estimate.Free(mem.System)
	second := p.Observe(done, 5, 1123000000)
	second.Free(mem.System)
	counts := p.Render(`{"op":"counts"}`)
	if counts.Text != `{"active":0,"completed":1,"failed":0,"cancelled":0}` {
		t.Error("counts query did not publish established state")
	}
	counts.Free(mem.System)
	used := heap.UsedBytes
	query := `{"op":"outcome","target":"root","outcome":"complete","ansi":true,"tracked":true,"scope":"5","event":{"type":"target-completed","node":7,"generation":1,"resource":{"kind":"task","name":"root"}}}`
	for i := 0; i < 5; i++ {
		result := p.Render(query)
		if result.Code != "" || result.Text != "[root] 0.1s - ✓\n" {
			t.Error("tracked outcome lost its established duration")
		}
		result.Free(mem.System)
	}
	if used != heap.UsedBytes || p.State.Counts.Completed != 1 {
		t.Error("read-only presentation query consumed state")
	}
	invalid := p.Observe(`{"type":"target-started","node":"7"}`, 5, 0)
	if invalid.Code != "OPT_VALUE_INVALID" || p.State.Counts.Active != 0 {
		t.Error("malformed observation mutated invocation state")
	}
	invalid.Free(mem.System)
	other := p.Observe(done, 6, 0)
	other.Free(mem.System)
	if p.State.Counts.Completed != 2 {
		t.Error("separate execution instances collided")
	}
	reset := p.Observe(`{"type":"reset"}`, 0, 0)
	reset.Free(mem.System)
	if p.State.Counts.Completed != 0 || len(p.State.Outcomes) != 0 {
		t.Error("reset retained previous invocation outcomes")
	}
	p.State.Free(&heap)
	mem.Free(&heap, p)
	if heap.UsedBytes != 0 {
		t.Error("presentation state leaked its invocation heap")
	}
}

func TestWasmPreparedWorkPrecedesSynchronousDispatch(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "task root :\n")
	if start.Runtime == nil {
		start.Result.Free(a)
		t.Error("runtime failed to compile")
		return
	}
	r := start.Runtime
	defer r.Free()
	begin := r.RequestTarget("root")
	defer begin.Free(a)
	if begin.Code != "" {
		t.Error("target request failed")
		return
	}
	observed := false
	for i := 0; i < 100; i++ {
		work := r.PrepareWorkJSON()
		again := r.PrepareWorkJSON()
		if work.Text != again.Text {
			t.Error("query/copy changed the pending dispatch")
		}
		again.Free(a)
		if work.Text != "" {
			observed = true
			if r.Handle.Node.Current {
				t.Error("work was reported only after the evaluator completed")
			}
		}
		work.Free(a)
		next := r.Step()
		if next.OK {
			next.Request.Free(a)
			t.Error("empty task invented a host request")
			return
		}
		result := r.Result()
		done := result.Done
		result.Free(a)
		if done {
			break
		}
	}
	if !observed {
		t.Error("synchronous work had no pre-dispatch observation")
	}
}

func TestWasmPresentationRejectsInvalidSnapshots(t *testing.T) {
	a := t.Allocator()
	inputs := []string{`[]`, `{`, `{"op":"unknown"}`, `{"op":"frame","width":-1}`, `{"op":"frame","width":9999999999}`, `{"op":"frame","width":"80","workers":[]}`, `{"op":"frame","workers":[{"elapsedMS":"1"}]}`, `{"op":"summary","counts":{"completed":9999999999}}`, `{"op":"outcome","outcome":"invented"}`}
	for i := range inputs {
		result := wasm.RenderPresentation(a, inputs[i])
		if result.Code != "OPT_VALUE_INVALID" || result.Text != "" {
			t.Error("invalid presentation snapshot was accepted")
		}
		result.Free(a)
	}
}
