package main

import "runtime"

// Host buffers have explicit ownership even though their backing storage is
// reclaimed by Go. Runtime heap measurements include all logical instances.
var hostBufferPeak uint32

//go:wasmexport kame_wasm_host_heap_used
func hostHeapUsed() uint32 {
	var bytes uint64
	for _, b := range buffers {
		bytes += uint64(len(b))
	}
	return uint32(min(bytes, uint64(^uint32(0))))
}

//go:wasmexport kame_wasm_host_heap_peak
func hostHeapPeak() uint32 { return hostBufferPeak }

//go:wasmexport kame_wasm_system_heap_used
func systemHeapUsed() uint32 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return uint32(min(stats.HeapAlloc, uint64(^uint32(0))))
}

//go:wasmexport kame_wasm_collect
func collect() { runtime.GC() }
