package main

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host/wasm"
	"kame/internal/mem"
)

//go:wasmexport kame_wasm_process_started
func processStarted(handle uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending == nil {
		return 4
	}
	i.runtime.ProcessStarted(i.pending.value)
	return 0
}

//go:wasmexport kame_wasm_process_started_request
func startedRequest(handle, req uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	r := i.requests[req]
	if r == nil {
		return 1
	}
	i.runtime.ProcessStarted(r.value)
	return 0
}

//go:wasmexport kame_wasm_process_exited_request
func exitedRequest(handle, req uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	r := i.requests[req]
	if r == nil {
		return 1
	}
	i.runtime.ProcessExited(r.value)
	return 0
}

//go:wasmexport kame_wasm_process_retain_limit
func processRetain(handle, req uint64) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return ^uint32(0)
	}
	r := i.requests[req]
	if req == 0 {
		r = i.pending
	}
	if r == nil {
		return ^uint32(0)
	}
	return uint32(i.runtime.ProcessRetainLimit(r.value))
}

//go:wasmexport kame_wasm_process_stream
func processStream(handle uint64, stderr, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending == nil {
		return 4
	}
	return processStreamRequest(handle, i.pending.handle, stderr, src, length)
}

//go:wasmexport kame_wasm_process_stream_request
func processStreamRequest(handle, req uint64, stderr, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	r := i.requests[req]
	if r == nil {
		return 1
	}
	b, ok := region(src, length)
	if !ok {
		return 4
	}
	i.runtime.ProcessStream(r.value, stderr != 0, b)
	return 0
}

//go:wasmexport kame_wasm_stream_item
func streamItem(handle, req uint64, node int32, src, length uint32) int32 {
	i, code := runtimeInstance(handle)
	if code != 0 || length > 65536 {
		return -124
	}
	r := i.requests[req]
	if r == nil {
		return -124
	}
	b, ok := region(src, length)
	if !ok {
		return -124
	}
	return int32(i.runtime.StreamItem(r.value, int(node), b))
}

//go:wasmexport kame_wasm_process_snapshot
func processSnapshot(handle, req uint64, src, length uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	r := i.requests[req]
	if r == nil {
		return 1
	}
	b, ok := region(src, length)
	if !ok {
		return 4
	}
	var v core.Value
	if !wasm.CompletionValueFromJSON(mem.System, b, &v) {
		return 4
	}
	defer v.Free(mem.System)
	if !i.runtime.ProcessGraphSnapshot(r.value, v) {
		return 4
	}
	return 0
}

//go:wasmexport kame_wasm_process_terminal
func processTerminal(handle uint64, status, signal int32, outcome, op, ol, ep, el, cp, cl, mp, ml uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending == nil {
		return 4
	}
	stdout, ok := region(op, ol)
	if !ok {
		return 4
	}
	stderr, ok := region(ep, el)
	if !ok {
		return 4
	}
	c, ok := text(cp, cl)
	if !ok {
		return 4
	}
	m, ok := text(mp, ml)
	if !ok {
		return 4
	}
	r := i.pending
	i.runtime.ProcessTerminal(r.value, stdout, stderr, int(status), int(signal), int(outcome), c, m)
	delete(i.requests, r.handle)
	i.last = r.handle
	i.pending = nil
	return 0
}

//go:wasmexport kame_wasm_complete_capture
func completeCapture(handle, req uint64, mp, ml, op, ol, ep, el uint32) uint32 {
	metadata, ok := region(mp, ml)
	if !ok {
		return 4
	}
	stdout, ok := region(op, ol)
	if !ok {
		return 4
	}
	stderr, ok := region(ep, el)
	if !ok {
		return 4
	}
	var v core.Value
	if !wasm.CompletionValueFromJSON(mem.System, metadata, &v) || !wasm.AppendCapturedBytes(mem.System, &v, stdout, stderr) {
		return 4
	}
	return complete(handle, req, v, diagnostic.Diagnostic{})
}
