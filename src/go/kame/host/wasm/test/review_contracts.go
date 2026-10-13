package wasm_test

import (
	"kame/host/wasm"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestReviewRuntimeOwnershipAndReversePhase(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		start.Result.Free(a)
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	r.SetDirectory("work")
	r.SetDirectory(r.Directory) // Replacement must clone before releasing an alias.
	r.SetDirectory("other")
	prepared := r.Prepare()
	if prepared.Code != "" {
		prepared.Free(a)
		t.Fatal("prepare")
		return
	}
	prepared.Free(a)
	requested := r.RequestExpression("42")
	if requested.Code != "PHASE_INVALID" || r.Node != nil {
		t.Error("expression accepted in prepared build mode")
	}
	requested.Free(a)
}

func TestReviewPureDiscardsEffectsOnEveryExit(t *testing.T) {
	a := t.Allocator()
	inputs := []string{`(out! "hello")`, `(list (err! "hello") missing)`, `(list (yield! "hello") (read "missing"))`}
	for i := range inputs {
		result := wasm.EvaluatePure(a, inputs[i])
		if i == 0 && result.Text != `"hello"` {
			t.Error("pure effect changed returned value")
		}
		if i == 1 && result.Code == "" {
			t.Error("expected failure after emission")
		}
		if i == 2 && !result.HostNeeded {
			t.Error("expected suspension after emission")
		}
		result.Free(a)
	}
}

func TestReviewConvenienceHeapOwnsDescriptorAndFailure(t *testing.T) {
	a := t.Allocator()
	buf := slices.Make[byte](a, 1024*1024)
	defer slices.Free(a, buf)
	// Route the documented system-owned storage through the test tracker.
	system := mem.System
	mem.System = a
	start := wasm.NewRuntimeIn(buf, "test.km", "")
	ok := start.Runtime != nil
	if ok {
		start.Runtime.Free()
	} else {
		start.Result.Free(mem.System)
	}
	failed := wasm.NewRuntimeIn(buf, "bad.km", "(")
	for i := range buf {
		buf[i] = 0
	}
	validFailure := failed.Runtime == nil && failed.Result.Code == "PARSE_ERR" && failed.Result.Message != ""
	if failed.Runtime != nil {
		failed.Runtime.Free()
	}
	failed.Result.Free(mem.System)
	mem.System = system
	if !ok || !validFailure {
		t.Error("convenience construction or external diagnostic lifetime failed")
	}
	heap := wasm.NewHeap(buf)
	borrowed := wasm.NewRuntimeWithHeap(&heap, "test.km", "")
	if borrowed.Runtime == nil {
		borrowed.Result.Free(&heap)
		t.Fatal("borrowed construction")
		return
	}
	borrowed.Runtime.Free()
	block, err := heap.Alloc(len(buf)-64, 1)
	if err != nil {
		t.Error("borrowed descriptor did not survive reset")
		return
	}
	heap.Free(block, len(buf)-64, 1)
}

func TestReviewDescriptorRejectsKnownMalformedFields(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		start.Result.Free(a)
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	sessions := []string{
		`{"fragments":[{"lang":"km","text":42}]}`,
		`{"fragments":[{"lang":"km","text":"x","offset":-1}]}`,
		`{"fragments":[{"lang":"km","text":"x","offset":2147483647}]}`,
		`{"fragments":[{"lang":"km","offset":2147483648}]}`,
		`{"fragments":[{"lang":"km","entries":[42]}]}`,
		`{"fragments":[{"lang":"km","defines":42}]}`,
		`{"fragments":[{"lang":"km","name":42}]}`,
		`{"fragments":[{"lang":"km","inline":"yes"}]}`,
		`{"fragments":42}`,
		`{"fragments":[{"lang":"km"}],"environment":42}`,
		`{"fragments":[{"lang":"km"}],"retryCount":2147483648}`,
		`{"fragments":[{"lang":"km"}],"timeoutMS":-1}`,
	}
	for i := range sessions {
		result := r.PrepareSession([]byte(sessions[i]))
		if result.Code != "PARSE_ERR" || r.Program != nil {
			t.Errorf("malformed session accepted: %d", i)
		}
		result.Free(a)
	}
	builds := []string{
		`{"sources":[{"name":"x","text":""}],"defines":42}`,
		`{"sources":[{"name":"x","text":""}],"parameters":[42]}`,
		`{"sources":[{"name":"x","text":""}],"retainBytes":-1}`,
		`{"sources":[{"name":"x","text":""}],"retryCount":2147483648}`,
		`{"sources":[{"name":"x","text":""}],"force":"yes"}`,
	}
	for i := range builds {
		result := r.SetBuildSources([]byte(builds[i]))
		if result.Code != "PARSE_ERR" || len(r.BuildSources) != 0 {
			t.Errorf("malformed build accepted: %d", i)
		}
		result.Free(a)
	}
	result := r.SetBuildSources([]byte(`{"sources":[{"name":"x","text":"","offset":2147483647}],"future":42,"json":42}`))
	if result.Code != "" {
		t.Error("valid boundary or unknown field rejected")
	}
	result.Free(a)
}

func TestReviewHeapTargetFailureOwnedByRuntime(t *testing.T) {
	a := t.Allocator()
	buf := slices.Make[byte](a, 1024*1024)
	defer slices.Free(a, buf)
	heap := wasm.NewHeap(buf)
	start := wasm.NewRuntimeWithHeap(&heap, "target.kmk", "")
	if start.Runtime == nil {
		start.Result.Free(&heap)
		t.Fatal("create heap runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	begin := r.RequestTarget("missing")
	if begin.Code == "" {
		t.Error("expected target preflight failure")
	}
	begin.Free(r.Alloc)
}

func TestReviewSessionPreservesOptionalAndUnknownFields(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		start.Result.Free(a)
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	result := r.PrepareSession([]byte(`{"fragments":[{"lang":"expr","text":"42","future":42}],"future":"compatible","defines":42}`))
	if result.Code != "" || r.Session == nil {
		t.Error("optional defaults or unknown fields rejected")
	}
	result.Free(a)
	requested := r.RequestExpression("42")
	if requested.Code != "PHASE_INVALID" {
		t.Error("expression accepted in session mode")
	}
	requested.Free(a)
}

func TestReviewMemoryHostRecordedDirectories(t *testing.T) {
	a := t.Allocator()
	h := wasm.NewMemoryHost(a)
	defer h.Free()
	h.Mkdir("parent/empty", 0o755)
	if !h.Stat("parent").Info.IsDir {
		t.Error("recorded child did not establish ancestor")
	}
	entries, err := h.ReadDir(a, "parent")
	if err != nil || len(entries) != 1 || entries[0].Name != "empty" || !entries[0].IsDir {
		t.Error("recorded directory listed as file or missing")
	}
	for i := range entries {
		mem.FreeString(a, entries[i].Name)
	}
	slices.Free(a, entries)
	entries, err = h.ReadDir(a, "parent/empty")
	if err != nil || len(entries) != 0 {
		t.Error("empty directory is not readable")
	}
	for i := range entries {
		mem.FreeString(a, entries[i].Name)
	}
	slices.Free(a, entries)
	h.Mkdir("parent", 0o755)
	if h.Remove("parent") == nil {
		t.Error("nonempty ancestor removed")
	}
	if h.Remove("parent/empty") != nil || h.Stat("parent/empty").Exists {
		t.Error("empty directory removal failed")
	}
	h.SetFile("file", []byte("data"))
	if h.Mkdir("file", 0o755) == nil {
		t.Error("mkdir accepted file")
	}
	entries, err = h.ReadDir(a, "file")
	if err == nil {
		t.Error("ReadDir accepted file")
	}
	for i := range entries {
		mem.FreeString(a, entries[i].Name)
	}
	slices.Free(a, entries)
	h.Mkdir("dir", 0o755)
	if h.WriteFileAtomic("dir", nil, 0o644, false) == nil {
		t.Error("write replaced directory")
	}
}
