package wasm_test

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/wasm"
	"solod.dev/so/testing"
)

func TestHandlesRejectStaleAndForeignOwners(t *testing.T) {
	table := wasm.NewTable(t.Allocator())
	handle := table.Add(1, 42)
	if value, ok := table.Get(1, handle); !ok || value != 42 {
		t.Error("live handle was not resolved")
	}
	if _, ok := table.Get(2, handle); ok {
		t.Error("foreign owner resolved handle")
	}
	if !table.Free(1, handle) {
		t.Error("live handle was not freed")
	}
	if _, ok := table.Get(1, handle); ok {
		t.Error("stale handle resolved")
	}
	if table.Free(1, handle) {
		t.Error("stale handle was freed twice")
	}
	next := table.Add(1, 7)
	if next == handle {
		t.Error("reused slot retained its generation")
	}
	table.FreeTable()
}

func TestOwnerResolvesHandlesWithoutPriorOwner(t *testing.T) {
	table := wasm.NewTable(t.Allocator())
	if table.Owner(0) != 0 {
		t.Error("zero handle reported an owner")
	}
	handle := table.Add(7, 99)
	if owner := table.Owner(handle); owner != 7 {
		t.Errorf("owner = %d, want 7", owner)
	}
	if value, ok := table.Get(table.Owner(handle), handle); !ok || value != 99 {
		t.Error("owner did not round-trip into a value lookup")
	}
	table.Free(7, handle)
	if table.Owner(handle) != 0 {
		t.Error("stale handle reported an owner")
	}
	table.FreeTable()
}

func TestEventHeaderHasStableLittleEndianLayout(t *testing.T) {
	var out [wasm.EventHeaderSize]byte
	header := wasm.EventHeader{Kind: 9, Root: 1, Node: 2, Request: 3, Generation: 4, Revision: 5, PayloadLen: 6}
	if !wasm.EncodeEventHeader(out[:], header) {
		t.Fatal("header was not encoded")
		return
	}
	if out[0] != 1 || out[2] != 9 || out[4] != 1 || out[12] != 2 || out[20] != 3 || out[28] != 4 || out[36] != 5 || out[44] != 6 {
		t.Error("header fields are not little-endian at stable offsets")
	}
	if wasm.EncodeEventHeader(out[:47], header) {
		t.Error("undersized output was accepted")
	}
}

func TestQueriedEventStaysPinnedUntilCopiedOrDiscarded(t *testing.T) {
	q := wasm.NewEventQueue(t.Allocator())
	q.Emit(wasm.EventHeader{Kind: 1}, []byte("first"))
	q.Emit(wasm.EventHeader{Kind: 2}, []byte("second"))
	first := q.Next()
	if !first.OK || first.Header.Kind != 1 || first.Header.PayloadLen != 5 {
		t.Fatal("first event was not exposed")
		return
	}
	again := q.Next()
	if !again.OK || again.Header.Kind != 1 {
		t.Error("queried event was replaced before copy")
	}
	short := make([]byte, 4)
	if q.Copy(short) {
		t.Error("undersized copy was accepted")
	}
	if q.Next().Header.Kind != 1 {
		t.Error("undersized copy released pinned event")
	}
	payload := make([]byte, 5)
	if !q.Copy(payload) || string(payload) != "first" {
		t.Error("event payload was not copied")
	}
	second := q.Next()
	if !second.OK || second.Header.Kind != 2 {
		t.Error("second event did not follow copied event")
	}
	if !q.Discard() {
		t.Error("pinned event was not discarded")
	}
	q.Free()
}

func TestPureExpressionUsesPortableEvaluator(t *testing.T) {
	a := t.Allocator()
	result := wasm.EvaluatePure(a, "(join [\"left\" \"right\"] \":\")")
	if result.Code != "" || result.Text != "left:right" || result.HostNeeded {
		t.Errorf("pure evaluation = %#v", result)
	}
	result.Free(a)
	fromSource := wasm.EvaluateSourcePure(a, "name = \"Kame\"\n", "(uppercase name)")
	if fromSource.Code != "" || fromSource.Text != "KAME" {
		t.Errorf("source evaluation = %#v", fromSource)
	}
	fromSource.Free(a)
}

func TestCompletionValueFromJSONParsesCanonicalValues(t *testing.T) {
	a := t.Allocator()
	var boolValue core.Value
	if !wasm.CompletionValueFromJSON(a, []byte("true"), &boolValue) || boolValue.Kind != core.Bool || !boolValue.Bool {
		t.Errorf("true = %#v", boolValue)
	}
	boolValue.Free(a)
	var intValue core.Value
	if !wasm.CompletionValueFromJSON(a, []byte("-42"), &intValue) || intValue.Kind != core.Int || intValue.Int != -42 {
		t.Errorf("-42 = %#v", intValue)
	}
	intValue.Free(a)
	var floatValue core.Value
	if !wasm.CompletionValueFromJSON(a, []byte("1.5"), &floatValue) || floatValue.Kind != core.Float || floatValue.Float != 1.5 {
		t.Errorf("1.5 = %#v", floatValue)
	}
	floatValue.Free(a)
	var stringValue core.Value
	if !wasm.CompletionValueFromJSON(a, []byte(`"a\nb\u0041"`), &stringValue) || stringValue.Kind != core.String || stringValue.Text != "a\nbA" {
		t.Errorf("escaped string = %#v", stringValue)
	}
	stringValue.Free(a)
	var listValue core.Value
	if !wasm.CompletionValueFromJSON(a, []byte(`["x","y"]`), &listValue) || listValue.Kind != core.List || len(listValue.List) != 2 || listValue.List[1].Text != "y" {
		t.Errorf("list = %#v", listValue)
	}
	listValue.Free(a)
	var recordValue core.Value
	if !wasm.CompletionValueFromJSON(a, []byte(`{"name":"x","size":3,"dir":true}`), &recordValue) || recordValue.Kind != core.Record || len(recordValue.Record) != 3 || recordValue.Record[1].Value.Int != 3 {
		t.Errorf("record = %#v", recordValue)
	}
	recordValue.Free(a)
	malformed := []string{`{"a":}`, `true false`, `[1,]`, `"unterminated`, `nul`}
	for i := range malformed {
		var value core.Value
		if wasm.CompletionValueFromJSON(a, []byte(malformed[i]), &value) {
			value.Free(a)
			t.Errorf("malformed JSON accepted: %s", malformed[i])
		}
	}
	var empty core.Value
	if !wasm.CompletionValueFromJSON(a, []byte("null"), &empty) || empty.Kind != core.Nil {
		t.Errorf("null = %#v", empty)
	}
	empty.Free(a)
}

func TestRuntimeMaterializesDefinitionTargetWithMemoryHost(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "result = (read \"input.txt\")\n")
	if started.Runtime == nil {
		t.Fatalf("runtime did not compile: %s", started.Result.Code)
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	if !runtime.SetFile("input.txt", []byte("wasm-target")) {
		t.Fatal("memory host rejected a file")
		return
	}
	if preflight := runtime.RequestTarget("result"); preflight.Code != "" {
		t.Fatalf("target request failed: %s", preflight.Code)
		return
	}
	done := false
	for i := 0; i < 32 && !done; i++ {
		runtime.Step()
		probe := runtime.Result()
		done = probe.Done
		probe.Free(a)
	}
	result := runtime.Result()
	if !result.Done {
		t.Error("target did not complete")
	} else if result.Diagnostic.Code != "" {
		t.Error("target diagnostic " + result.Diagnostic.Code + ": " + result.Diagnostic.Message)
	} else if result.Value.Kind != core.Bytes {
		t.Error("target value was not bytes")
	} else if string(result.Value.Bytes) != "wasm-target" {
		t.Error("target bytes = " + string(result.Value.Bytes))
	}
	result.Free(a)
}

func TestRuntimeTargetEnvironmentComesFromConfiguration(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "result = (env \"KAME_TARGET_ENV\")\n")
	if started.Runtime == nil {
		t.Fatalf("runtime did not compile: %s", started.Result.Code)
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	if !runtime.SetEnvironment("KAME_TARGET_ENV", "target-value") {
		t.Fatal("memory host rejected an environment entry")
		return
	}
	if preflight := runtime.RequestTarget("result"); preflight.Code != "" {
		t.Fatalf("target request failed: %s", preflight.Code)
		return
	}
	done := false
	for i := 0; i < 32 && !done; i++ {
		runtime.Step()
		probe := runtime.Result()
		done = probe.Done
		probe.Free(a)
	}
	result := runtime.Result()
	if !result.Done {
		t.Error("target did not complete")
	} else if result.Diagnostic.Code != "" {
		t.Error("target diagnostic " + result.Diagnostic.Code + ": " + result.Diagnostic.Message)
	} else if result.Value.Kind != core.String {
		t.Error("target value was not a string")
	} else if result.Value.Text != "target-value" {
		t.Error("target text = " + result.Value.Text)
	}
	result.Free(a)
}

func TestRuntimeForwardsTargetHostRequests(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "result = (read \"input.txt\")\n")
	if started.Runtime == nil {
		t.Fatalf("runtime did not compile: %s", started.Result.Code)
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	if !runtime.SetForwarding(true) {
		t.Fatal("forwarding was rejected")
		return
	}
	if preflight := runtime.RequestTarget("result"); preflight.Code != "" {
		t.Fatalf("target request failed: %s", preflight.Code)
		return
	}
	serviced := false
	done := false
	for i := 0; i < 64 && !done; i++ {
		next := runtime.Step()
		if next.OK {
			if next.Request.Kind != host.RequestReadFile || host.PayloadPath(next.Request.Payload) != "input.txt" {
				t.Error("unexpected forwarded request")
				break
			}
			runtime.Complete(next.Request, core.NewBytes(a, []byte("forwarded")), diagnostic.Diagnostic{})
			next.Request.Free(a)
			serviced = true
		}
		probe := runtime.Result()
		done = probe.Done
		probe.Free(a)
	}
	if !serviced {
		t.Error("no host request was forwarded")
	}
	result := runtime.Result()
	if !result.Done {
		t.Error("forwarded target did not complete")
	} else if result.Diagnostic.Code != "" {
		t.Error("forwarded target diagnostic " + result.Diagnostic.Code + ": " + result.Diagnostic.Message)
	} else if result.Value.Kind != core.Bytes {
		t.Error("forwarded target value was not bytes")
	} else if string(result.Value.Bytes) != "forwarded" {
		t.Error("forwarded target bytes = " + string(result.Value.Bytes))
	}
	result.Free(a)
}

func TestRuntimeForwardsRuleRecipe(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "./out.txt :\n\tprintf written\n")
	if started.Runtime == nil {
		t.Fatalf("runtime did not compile: %s", started.Result.Code)
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	if !runtime.SetForwarding(true) {
		t.Fatal("forwarding was rejected")
		return
	}
	if preflight := runtime.RequestTarget("./out.txt"); preflight.Code != "" {
		t.Fatalf("target request failed: %s", preflight.Code)
		return
	}
	serviced := false
	done := false
	for i := 0; i < 64 && !done; i++ {
		next := runtime.Step()
		if next.OK {
			if next.Request.Kind != host.RequestProcess || host.PayloadText(next.Request.Payload, host.FieldScript) != "printf written" {
				t.Error("unexpected forwarded recipe request")
				break
			}
			runtime.Complete(next.Request, core.NewString(a, ""), diagnostic.Diagnostic{})
			next.Request.Free(a)
			serviced = true
		}
		probe := runtime.Result()
		done = probe.Done
		probe.Free(a)
	}
	if !serviced {
		t.Error("recipe was not forwarded")
	}
	result := runtime.Result()
	if !result.Done {
		t.Error("rule target did not complete")
	} else if result.Diagnostic.Code != "" {
		t.Error("rule target diagnostic " + result.Diagnostic.Code + ": " + result.Diagnostic.Message)
	} else if result.Value.Kind != core.String || result.Value.Text != "./out.txt" {
		t.Error("rule target path = " + result.Value.Text)
	}
	result.Free(a)
}

func TestRuntimeYieldsHostRequestAndResumes(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "")
	if started.Runtime == nil || started.Result.Code != "" {
		t.Fatal("runtime did not compile")
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	request := runtime.RequestExpression("(shell \"echo wasm\")")
	if request.Code != "" {
		t.Fatalf("request expression failed: %s", request.Code)
		return
	}
	next := runtime.Step()
	if !next.OK || next.Request.Kind != host.RequestProcess || next.Request.ID == 0 {
		t.Fatal("shell did not yield a host request")
		return
	}
	runtime.Complete(next.Request, core.NewString(a, "wasm"), diagnostic.Diagnostic{})
	next.Request.Free(a)
	_ = runtime.Step()
	_ = runtime.Step()
	result := runtime.Result()
	if !result.Done || result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "wasm" {
		t.Errorf("resumed runtime result = %#v", result)
	}
	result.Free(a)
}

func TestRuntimeYieldsDirectFileReadRequest(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "")
	if started.Runtime == nil {
		t.Fatal("runtime did not compile")
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	if request := runtime.RequestExpression("(read \"input.txt\")"); request.Code != "" {
		t.Fatalf("request expression failed: %s", request.Code)
		return
	}
	next := runtime.Step()
	if !next.OK || next.Request.Kind != host.RequestReadFile || host.PayloadPath(next.Request.Payload) != "input.txt" {
		t.Fatal("read did not yield a direct file request")
		return
	}
	runtime.Complete(next.Request, core.NewBytes(a, []byte("wasm")), diagnostic.Diagnostic{})
	next.Request.Free(a)
	_ = runtime.Step()
	_ = runtime.Step()
	result := runtime.Result()
	if !result.Done || result.Diagnostic.Code != "" || result.Value.Kind != core.Bytes || string(result.Value.Bytes) != "wasm" {
		t.Errorf("resumed file read result = %#v", result)
	}
	result.Free(a)
}

func TestRuntimeYieldsDirectEnvironmentRequest(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "")
	if started.Runtime == nil {
		t.Fatal("runtime did not compile")
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	if request := runtime.RequestExpression("(env \"KAME_WASM_TEST_VALUE\")"); request.Code != "" {
		t.Fatalf("request expression failed: %s", request.Code)
		return
	}
	next := runtime.Step()
	if !next.OK || next.Request.Kind != host.RequestEnvironment || host.PayloadPath(next.Request.Payload) != "KAME_WASM_TEST_VALUE" {
		t.Fatal("env did not yield a direct environment request")
		return
	}
	runtime.Complete(next.Request, core.NewString(a, "wasm"), diagnostic.Diagnostic{})
	next.Request.Free(a)
	_ = runtime.Step()
	_ = runtime.Step()
	result := runtime.Result()
	if !result.Done || result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "wasm" {
		t.Errorf("resumed environment result = %#v", result)
	}
	result.Free(a)
}

func TestRuntimeCancellationIgnoresLateCompletion(t *testing.T) {
	a := t.Allocator()
	started := wasm.NewRuntime(a, "")
	if started.Runtime == nil {
		t.Fatal("runtime did not compile")
		return
	}
	runtime := started.Runtime
	defer runtime.Free()
	if request := runtime.RequestExpression("(shell \"echo wasm\")"); request.Code != "" {
		t.Fatalf("request expression failed: %s", request.Code)
		return
	}
	next := runtime.Step()
	if !next.OK || !runtime.Cancel() {
		t.Fatal("runtime did not yield and cancel a host request")
		return
	}
	// A callback may arrive after cancellation. Engine correlation discards it.
	runtime.Complete(next.Request, core.NewString(a, "late"), diagnostic.Diagnostic{})
	next.Request.Free(a)
	_ = runtime.Step()
	result := runtime.Result()
	if !result.Done || result.Diagnostic.Code != "EXEC_CANCELLED" {
		t.Errorf("cancelled runtime result = %#v", result)
	}
	result.Free(a)
}
