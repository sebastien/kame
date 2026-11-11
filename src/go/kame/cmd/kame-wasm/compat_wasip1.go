package main

import (
	"kame/host/wasm"
	"kame/internal/mem"
)

//go:wasmexport kame_wasm_event_header
func encodeEventHeader(dst, size, kind uint32, root, node, request uint64, generation, revision int64, payload uint32) uint32 {
	if size < wasm.EventHeaderSize {
		return 3
	}
	b, ok := region(dst, wasm.EventHeaderSize)
	if !ok {
		return 4
	}
	wasm.EncodeEventHeader(b, wasm.EventHeader{Kind: uint16(kind), Root: wasm.Handle(root), Node: wasm.Handle(node), Request: wasm.Handle(request), Generation: generation, Revision: revision, PayloadLen: payload})
	return 0
}

//go:wasmexport kame_wasm_expression_request
func expressionRequest(handle uint64, src, length, dst, size, out uint32) uint32 {
	i, code := runtimeInstance(handle)
	if code != 0 {
		return code
	}
	if i.pending != nil || i.presentation != nil {
		return 4
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	return finish(wasm.EvaluateSourcePure(mem.System, i.source, s), dst, size, out, &i.diagnostic)
}
