package wasm_test

import (
	"kame/core"
	"kame/host/wasm"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestWatchRetainsSharedRootsAndInvalidatesObservedFiles(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "A = (text (read ./input))\nB = (uppercase A)\n")
	if started.Runtime == nil {
		t.Fatal("compile watched definitions")
		return
	}
	r := started.Runtime
	defer r.Free()
	r.SetFile("input", []byte("first"))
	filesystem := r.Host
	begin := r.RequestWatch([]byte("[\"A\",\"B\"]"))
	if begin.Code != "" {
		t.Error(begin.Message)
		begin.Free(a)
		return
	}
	begin.Free(a)
	for i := 0; i < 64; i++ {
		next := r.Step()
		if next.OK {
			next.Request.Free(a)
			t.Error("memory watch submitted host request")
			return
		}
	}
	snapshot := r.WatchStateJSON()
	if !strings.Contains(snapshot.Text, "FIRST") || !strings.Contains(snapshot.Text, "first") || !strings.Contains(snapshot.Text, "\"busy\":false") {
		t.Error(snapshot.Text)
	}
	snapshot.Free(a)
	keys := r.Program.FilesystemResources()
	if len(keys) != 1 || keys[0].Kind != core.ResourceFile {
		t.Error("watch lost or duplicated shared input interest")
	}
	for i := range keys {
		keys[i].Free(a)
	}
	slices.Free(a, keys)
	for i := 0; i < 32; i++ {
		queried := r.WatchStateJSON()
		queried.Free(a)
	}
	filesystem.SetFile("input", []byte("second"))
	invalidated := r.InvalidateWatch([]byte("[{\"kind\":\"file\",\"name\":\"input\"}]"))
	if invalidated.Code != "" {
		t.Error(invalidated.Message)
	}
	invalidated.Free(a)
	for i := 0; i < 64; i++ {
		next := r.Step()
		if next.OK {
			next.Request.Free(a)
			t.Error("unexpected forwarded request")
			return
		}
	}
	snapshot = r.WatchStateJSON()
	if !strings.Contains(snapshot.Text, "SECOND") || !strings.Contains(snapshot.Text, "second") || strings.Contains(snapshot.Text, "FIRST") {
		t.Error(snapshot.Text)
	}
	snapshot.Free(a)
	result := r.Result()
	if result.Done {
		t.Error("watch session dropped retained root at completion")
	}
	result.Free(a)
}

func TestWatchInvalidationBatchRejectsPartialMutation(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "VALUE = (text (read ./input))\n")
	if started.Runtime == nil {
		t.Fatal("compile watched value")
		return
	}
	r := started.Runtime
	defer r.Free()
	r.SetFile("input", []byte("before"))
	begin := r.RequestWatch([]byte("[\"VALUE\"]"))
	begin.Free(a)
	for i := 0; i < 32; i++ {
		next := r.Step()
		if next.OK {
			next.Request.Free(a)
		}
	}
	before := r.WatchStateJSON()
	invalid := r.InvalidateWatch([]byte("[{\"kind\":\"file\",\"name\":\"input\"},{\"kind\":\"process\",\"name\":\"bad\"}]"))
	if invalid.Code != "PARSE_ERR" {
		t.Error("invalid batch accepted")
	}
	invalid.Free(a)
	after := r.WatchStateJSON()
	if before.Text != after.Text {
		t.Error("invalid batch partially changed graph")
	}
	before.Free(a)
	after.Free(a)
}

func TestWatchRejectsInvalidTargetsAndFreesFailedBatch(t *testing.T) {
	a := t.Allocator()
	invalid := []string{"[]", "{}", "[1]", "[\"\"]", "[\"A\",false]"}
	for i := range invalid {
		started := wasm.NewRuntime(a, "A = 1\n")
		if started.Runtime == nil {
			t.Fatal("compile watch batch")
			return
		}
		r := started.Runtime
		begin := r.RequestWatch([]byte(invalid[i]))
		if begin.Code != "PARSE_ERR" || r.Program != nil {
			t.Error("invalid targets started a program")
		}
		begin.Free(a)
		r.Free()
	}
	started := wasm.NewRuntime(a, "A = 1\n")
	if started.Runtime == nil {
		t.Fatal("compile failed batch")
		return
	}
	r := started.Runtime
	begin := r.RequestWatch([]byte("[\"A\",\"missing\"]"))
	if begin.Code != "TGT_NO_RULE" || r.Handle != nil {
		t.Error("failed batch retained active roots")
	}
	begin.Free(a)
	r.Free()
}
