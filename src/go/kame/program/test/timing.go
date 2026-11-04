package program_test

import (
	"kame/core"
	"kame/host/wasm"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type timingClock struct{ Now int64 }

func advanceTimingClock(value any) int64 {
	clock := value.(*timingClock)
	clock.Now += 100000000
	return clock.Now
}

func TestTimingHistoryDoesNotChangeFreshnessOrCountPastWatchWork(t *testing.T) {
	a := t.Allocator()
	memory := wasm.NewMemoryHost(a)
	memory.SetFile("seed", []byte("seed"))
	parsed := script.Parse(a, "timing.kmk", "default : ./out\n./out : ./seed ; [shell: kame]\n\t(read @<)\n")
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	defer registry.Free()
	clock := timingClock{}
	compiled := program.Compile(a, parsed, registry, program.Options{Host: memory, Directory: ".", TrackTimings: true, TimingNow: advanceTimingClock, TimingClock: &clock, Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil {
		compiled.Free(a)
		t.Fatal("timing compile failed")
		return
	}
	defer compiled.Free(a)
	defer compiled.Program.Free()
	first := compiled.Program.Materialize("default")
	if first.Diagnostic.Code != "" {
		t.Error("timing collection failed execution")
	}
	first.Free(a)
	entries, err := memory.ReadDir(a, ".kame/cache/timing")
	if err != nil || len(entries) != 2 {
		t.Error("file and aggregate timing history not saved")
	}
	var signatures []core.Signature
	for i := range entries {
		name := path.Join(a, ".kame/cache/timing", entries[i].Name)
		data, readErr := memory.ReadFile(a, name)
		if readErr != nil || len(data) != 12 || string(data[:4]) != "KTM1" {
			t.Error("invalid timing record")
		}
		signatures = slices.Append(a, signatures, core.ContentSignature(data))
		mem.FreeSlice(a, data)
		mem.FreeString(a, name)
	}
	second := compiled.Program.Materialize("default")
	if second.Diagnostic.Code != "" {
		t.Error("timing history changed freshness")
	}
	second.Free(a)
	for i := range entries {
		name := path.Join(a, ".kame/cache/timing", entries[i].Name)
		data, readErr := memory.ReadFile(a, name)
		if readErr != nil || !core.ContentSignature(data).Equal(signatures[i]) {
			t.Error("cache-only aggregate replaced history with old-generation work")
		}
		mem.FreeSlice(a, data)
		mem.FreeString(a, name)
		mem.FreeString(a, entries[i].Name)
	}
	mem.FreeSlice(a, entries)
	slices.Free(a, signatures)
}
