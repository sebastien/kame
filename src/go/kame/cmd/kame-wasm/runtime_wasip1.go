package main

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/wasm"
	"kame/internal/mem"
	"kame/lang/eval"
)

type request struct {
	handle uint64
	value  host.Request
	pinned bool
}

var nextToken uint64 = 1 << 32

func token() uint64 { nextToken++; return nextToken }
func (i *instance) status(r wasm.PureResult) uint32 {
	i.diagnostic = ""
	i.spanStart, i.spanEnd = r.SpanStart, r.SpanEnd
	if r.Code != "" {
		i.diagnostic = r.Code + ": " + r.Message
		return 5
	}
	if r.HostNeeded {
		return 6
	}
	return 0
}
func runtimeInstance(handle uint64) (*instance, uint32) {
	i := instances[handle]
	if i == nil {
		return nil, 1
	}
	if i.runtime == nil {
		return nil, 4
	}
	return i, 0
}
func writeBytes(dst, size uint32, b []byte) uint32 {
	if uint32(len(b)) > size {
		return 3
	}
	d, ok := region(dst, uint32(len(b)))
	if !ok {
		return 4
	}
	copy(d, b)
	return 0
}

//go:wasmexport kame_wasm_source_compile
func sourceCompile(handle uint64, src, length uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	if i.presentation != nil || len(i.requests) > 0 || (i.runtime != nil && i.runtime.FinalizationPending()) {
		return 4
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	started := wasm.NewRuntime(mem.System, s)
	if started.Runtime == nil {
		return i.status(started.Result)
	}
	if i.runtime != nil {
		i.runtime.Free()
	}
	i.runtime = started.Runtime
	i.source = s
	i.root, i.node, i.last = 0, 0, 0
	return i.status(started.Result)
}

//go:wasmexport kame_wasm_set_source_name
func setSourceName(handle uint64, src, length uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	i.name = s
	return 0
}

//go:wasmexport kame_wasm_instance_diagnostic_span
func diagnosticSpan(handle uint64, start, end uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	if i.spanStart == 0 && i.spanEnd == 0 {
		return 4
	}
	a, ok := region(start, 4)
	if !ok {
		return 4
	}
	b, ok := region(end, 4)
	if !ok {
		return 4
	}
	put32(a, 0, uint32(i.spanStart))
	put32(b, 0, uint32(i.spanEnd))
	return 0
}

func put32(b []byte, at int, v uint32) {
	for j := 0; j < 4; j++ {
		b[at+j] = byte(v >> uint(j*8))
	}
}

//go:wasmexport kame_wasm_expression_begin
func expressionBegin(handle uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	code = i.status(i.runtime.RequestExpression(s))
	if code == 0 {
		i.root, i.node = token(), token()
	}
	return code
}

//go:wasmexport kame_wasm_target_begin
func targetBegin(handle uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	code = i.status(i.runtime.RequestTarget(s))
	if code == 0 {
		i.root, i.node = token(), token()
	}
	return code
}

//go:wasmexport kame_wasm_expression_cancel
func cancelExpression(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	i.runtime.Cancel()
	if i.pending != nil {
		i.last = i.pending.handle
		delete(i.requests, i.pending.handle)
		i.pending = nil
	}
	return 0
}

//go:wasmexport kame_wasm_step
func step(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending != nil {
		return 1
	}
	n := i.runtime.Step()
	if n.OK {
		r := &request{handle: token(), value: n.Request, pinned: true}
		i.requests[r.handle] = r
		i.pending = r
		return 1
	}
	r := i.runtime.Result()
	defer r.Free(mem.System)
	if r.Done {
		return 2
	}
	return 0
}

//go:wasmexport kame_wasm_next_request_id
func nextRequestID(handle uint64) int64 {
	i := instances[handle]
	if i == nil || i.pending == nil {
		return 0
	}
	return i.pending.value.ID
}

//go:wasmexport kame_wasm_next_event_header
func eventHeader(handle uint64, dst, size uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending == nil || !i.pending.pinned {
		return 4
	}
	if size < wasm.EventHeaderSize {
		return 3
	}
	b, ok := region(dst, wasm.EventHeaderSize)
	if !ok {
		return 4
	}
	r := i.pending
	wasm.EncodeEventHeader(b, wasm.EventHeader{Kind: 1, Root: wasm.Handle(i.root), Node: wasm.Handle(i.node), Request: wasm.Handle(r.handle), Generation: r.value.Generation, PayloadLen: uint32(len(wasm.ABIRequestPayload(r.value)))})
	return 0
}

//go:wasmexport kame_wasm_event_payload_copy
func eventPayload(handle uint64, dst, size uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending == nil || !i.pending.pinned {
		return 4
	}
	code = writeBytes(dst, size, []byte(wasm.ABIRequestPayload(i.pending.value)))
	if code == 0 {
		i.pending.pinned = false
	}
	return code
}

//go:wasmexport kame_wasm_event_discard
func discardEvent(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending == nil || !i.pending.pinned {
		return 4
	}
	i.pending.pinned = false
	return 0
}

//go:wasmexport kame_wasm_request_detach
func detachRequest(handle, req uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending == nil || i.pending.handle != req {
		return 1
	}
	i.pending.pinned = false
	i.pending = nil
	return 0
}

//go:wasmexport kame_wasm_request_attach
func attachRequest(handle, req uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	r := i.requests[req]
	if r == nil {
		return 1
	}
	i.pending = r
	r.pinned = true
	return 0
}
func complete(handle, req uint64, value core.Value, d diagnostic.Diagnostic) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	r := i.requests[req]
	if r == nil {
		if req != 0 && req == i.last {
			return 0
		}
		return 1
	}
	i.runtime.Complete(r.value, value, d)
	delete(i.requests, req)
	i.last = req
	if i.pending == r {
		i.pending = nil
	}
	return 0
}

//go:wasmexport kame_wasm_complete_text
func completeText(handle, req uint64, src, length uint32) uint32 {
	b, ok := text(src, length)
	if !ok {
		return 4
	}
	return complete(handle, req, core.NewString(mem.System, b), diagnostic.Diagnostic{})
}

//go:wasmexport kame_wasm_complete_bytes
func completeBytes(handle, req uint64, src, length uint32) uint32 {
	b, ok := region(src, length)
	if !ok {
		return 4
	}
	return complete(handle, req, core.NewBytes(mem.System, b), diagnostic.Diagnostic{})
}

//go:wasmexport kame_wasm_complete_nil
func completeNil(handle, req uint64) uint32 {
	return complete(handle, req, core.Value{}, diagnostic.Diagnostic{})
}

//go:wasmexport kame_wasm_complete_json
func completeJSON(handle, req uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.requests[req] == nil {
		return complete(handle, req, core.Value{}, diagnostic.Diagnostic{})
	}
	b, ok := region(src, length)
	if !ok {
		return 4
	}
	var v core.Value
	if !wasm.CompletionValueFromJSON(mem.System, b, &v) {
		complete(handle, req, core.Value{}, diagnostic.Diagnostic{Code: "HOST_FAIL", Message: "host completion is not valid canonical JSON", Severity: diagnostic.Error})
		i.diagnostic = "HOST_FAIL: host completion is not valid canonical JSON"
		return 5
	}
	return complete(handle, req, v, diagnostic.Diagnostic{})
}

//go:wasmexport kame_wasm_complete_failure
func completeFailure(handle, req uint64, cp, cl, mp, ml uint32) uint32 {
	c, ok := text(cp, cl)
	if !ok {
		return 4
	}
	m, ok := text(mp, ml)
	if !ok {
		return 4
	}
	return complete(handle, req, core.Value{}, diagnostic.Diagnostic{Code: c, Message: m, Severity: diagnostic.Error})
}

//go:wasmexport kame_wasm_result_copy
func resultCopy(handle uint64, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	r := i.runtime.Result()
	defer r.Free(mem.System)
	if !r.Done {
		return 4
	}
	if r.Diagnostic.Code != "" {
		return finish(wasm.PureResult{Code: r.Diagnostic.Code, Message: r.Diagnostic.Message}, dst, size, out, &i.diagnostic)
	}
	text := r.Value.Text
	if i.runtime.TargetResultKind() != 2 {
		text = eval.Display(mem.System, r.Value)
	}
	return finish(wasm.PureResult{Text: text}, dst, size, out, &i.diagnostic)
}

//go:wasmexport kame_wasm_result_kind
func resultKind(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return 0
	}
	return i.runtime.TargetResultKind()
}

//go:wasmexport kame_wasm_set_directory
func setDirectory(handle uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	s, ok := text(src, length)
	if !ok || !i.runtime.SetDirectory(s) {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_set_forwarding
func setForwarding(handle uint64, on uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if !i.runtime.SetForwarding(on != 0) {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_next_request_kind
func requestKind(handle uint64) uint32 {
	i := instances[handle]
	if i == nil || i.pending == nil || !i.pending.pinned {
		return 0
	}
	return wasm.ABIRequestKind(i.pending.value)
}
func requestBuffer(handle uint64, kind int) []byte {
	i := instances[handle]
	if i == nil || i.pending == nil || !i.pending.pinned {
		return nil
	}
	r := i.pending.value
	switch kind {
	case 0:
		if r.Kind == host.RequestWriteFile {
			return host.PayloadBytes(r.Payload, "data")
		}
	case 1:
		switch r.Kind {
		case host.RequestCacheGet, host.RequestCachePut, host.RequestCacheDelete, host.RequestCacheLock, host.RequestCacheUnlock:
			return host.CacheKey(r.Payload)
		}
	case 2:
		if r.Kind == host.RequestCachePut {
			return host.CacheRecord(r.Payload)
		}
	}
	return nil
}

//go:wasmexport kame_wasm_next_request_data_length
func requestDataLength(handle uint64) uint32 { return uint32(len(requestBuffer(handle, 0))) }

//go:wasmexport kame_wasm_request_data_copy
func requestDataCopy(handle uint64, dst, size uint32) uint32 {
	return writeBytes(dst, size, requestBuffer(handle, 0))
}

//go:wasmexport kame_wasm_next_request_key_length
func requestKeyLength(handle uint64) uint32 { return uint32(len(requestBuffer(handle, 1))) }

//go:wasmexport kame_wasm_request_key_copy
func requestKeyCopy(handle uint64, dst, size uint32) uint32 {
	return writeBytes(dst, size, requestBuffer(handle, 1))
}

//go:wasmexport kame_wasm_next_request_record_length
func requestRecordLength(handle uint64) uint32 { return uint32(len(requestBuffer(handle, 2))) }

//go:wasmexport kame_wasm_request_record_copy
func requestRecordCopy(handle uint64, dst, size uint32) uint32 {
	return writeBytes(dst, size, requestBuffer(handle, 2))
}
