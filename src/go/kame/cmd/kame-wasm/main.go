//go:build wasip1 && wasm

// The WASI reactor exposes the same primitive buffer protocol as the JS host.
// Buffers are pinned by the allocation table until the caller explicitly frees
// them; Go never stores pointer-bearing objects in a raw byte arena.
package main

import (
	"encoding/binary"
	"kame/host/wasm"
	"kame/internal/mem"
	"math"
	"unsafe"
)

var buffers = map[uint32][]byte{}
var instances = map[uint64]*instance{}
var nextInstance uint64
var globalDiagnostic string

type instance struct {
	presentation       *wasm.Presentation
	root, node, last   uint64
	pending            *request
	requests           map[uint64]*request
	spanStart, spanEnd int

	runtime    *wasm.Runtime
	diagnostic string
	name       string
	source     string
}

func main() {}
func region(p, n uint32) ([]byte, bool) {
	if n == 0 {
		return nil, true
	}
	for base, b := range buffers {
		if p >= base && uint64(p)+uint64(n) <= uint64(base)+uint64(len(b)) {
			return b[p-base : p-base+n], true
		}
	}
	return nil, false
}
func text(p, n uint32) (string, bool) { b, ok := region(p, n); return string(b), ok }
func finish(result wasm.PureResult, dst, size, out uint32, diag *string) uint32 {
	*diag = ""
	length, ok := region(out, 4)
	if !ok {
		return 4
	}
	binary.LittleEndian.PutUint32(length, uint32(len(result.Text)))
	if result.Code != "" {
		*diag = result.Code + ": " + result.Message
		return 5
	}
	if uint32(len(result.Text)) > size {
		return 3
	}
	b, ok := region(dst, uint32(len(result.Text)))
	if !ok {
		return 4
	}
	copy(b, result.Text)
	return 0
}

//go:wasmexport kame_wasm_abi_version
func abiVersion() uint32 { return 1 }

//go:wasmexport kame_wasm_alloc
func alloc(size, alignment uint32) uint32 {
	if size == 0 || alignment == 0 || alignment&(alignment-1) != 0 || alignment > 65536 || uint64(size)+uint64(alignment) > math.MaxInt32 {
		return 0
	}
	b := make([]byte, int(size)+int(alignment)-1)
	base := uintptr(unsafe.Pointer(&b[0]))
	offset := (-base) & uintptr(alignment-1)
	p := uint32(base + offset)
	buffers[p] = b[offset : offset+uintptr(size)]
	hostBufferPeak = max(hostBufferPeak, hostHeapUsed())
	return p
}

//go:wasmexport kame_wasm_free
func free(p, size uint32) uint32 {
	if p == 0 && size == 0 {
		return 0
	}
	b, ok := buffers[p]
	if !ok || uint32(len(b)) != size {
		return 4
	}
	delete(buffers, p)
	return 0
}

//go:wasmexport kame_wasm_copy
func copyBuffer(dst, size, src, length uint32) uint32 {
	if size < length {
		return 3
	}
	d, ok := region(dst, length)
	if !ok {
		return 4
	}
	s, ok := region(src, length)
	if !ok {
		return 4
	}
	copy(d, s)
	return 0
}

//go:wasmexport kame_wasm_eval_pure
func evalPure(src, length, dst, size, out uint32) uint32 {
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	return finish(wasm.EvaluatePure(mem.System, s), dst, size, out, &globalDiagnostic)
}

//go:wasmexport kame_wasm_eval_source_pure
func evalSource(program, programLen, src, length, dst, size, out uint32) uint32 {
	p, ok := text(program, programLen)
	if !ok {
		return 4
	}
	s, ok := text(src, length)
	if !ok {
		return 4
	}
	return finish(wasm.EvaluateSourcePure(mem.System, p, s), dst, size, out, &globalDiagnostic)
}

//go:wasmexport kame_wasm_instance_create
func createInstance() uint64 {
	nextInstance++
	instances[nextInstance] = &instance{name: "<wasm>", requests: map[uint64]*request{}}
	return nextInstance
}

//go:wasmexport kame_wasm_instance_free
func freeInstance(handle uint64) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	if i.runtime != nil {
		if i.runtime.FinalizationPending() {
			return 4
		}
		i.runtime.Free()
	}
	if i.presentation != nil {
		i.presentation.Free()
	}
	delete(instances, handle)
	return 0
}

//go:wasmexport kame_wasm_cli
func cli(handle uint64, command, commandLen, args, argsLen, dst, size, out uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	c, ok := text(command, commandLen)
	if !ok {
		return 4
	}
	a, ok := text(args, argsLen)
	if !ok {
		return 4
	}
	return finish(wasm.ParseCLI(mem.System, c, a), dst, size, out, &i.diagnostic)
}

//go:wasmexport kame_wasm_parse
func parse(handle uint64, lang, langLen, name, nameLen, source, sourceLen, dst, size, out uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	l, ok := text(lang, langLen)
	if !ok {
		return 4
	}
	n, ok := text(name, nameLen)
	if !ok {
		return 4
	}
	s, ok := text(source, sourceLen)
	if !ok {
		return 4
	}
	return finish(wasm.ParseLanguage(mem.System, l, n, s), dst, size, out, &i.diagnostic)
}

//go:wasmexport kame_wasm_format
func format(handle uint64, lang, langLen, name, nameLen, source, sourceLen, indent, indentLen, width, dst, size, out uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	l, ok := text(lang, langLen)
	if !ok {
		return 4
	}
	n, ok := text(name, nameLen)
	if !ok {
		return 4
	}
	s, ok := text(source, sourceLen)
	if !ok {
		return 4
	}
	ind, ok := text(indent, indentLen)
	if !ok {
		return 4
	}
	return finish(wasm.FormatLanguage(mem.System, l, n, s, ind, int(width)), dst, size, out, &i.diagnostic)
}

//go:wasmexport kame_wasm_diagnostic_length
func diagnosticLength() uint32 { return uint32(len(globalDiagnostic)) }

//go:wasmexport kame_wasm_diagnostic_copy
func diagnosticCopy(dst, size uint32) uint32 {
	if size < uint32(len(globalDiagnostic)) {
		return 3
	}
	b, ok := region(dst, uint32(len(globalDiagnostic)))
	if !ok {
		return 4
	}
	copy(b, globalDiagnostic)
	return 0
}

//go:wasmexport kame_wasm_instance_diagnostic_length
func instanceDiagnosticLength(handle uint64) uint32 {
	i := instances[handle]
	if i == nil {
		return 0
	}
	return uint32(len(i.diagnostic))
}

//go:wasmexport kame_wasm_instance_diagnostic_copy
func instanceDiagnosticCopy(handle uint64, dst, size uint32) uint32 {
	i := instances[handle]
	if i == nil {
		return 1
	}
	if size < uint32(len(i.diagnostic)) {
		return 3
	}
	b, ok := region(dst, uint32(len(i.diagnostic)))
	if !ok {
		return 4
	}
	copy(b, i.diagnostic)
	return 0
}
