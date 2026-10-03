package wasm_test

import (
	"unsafe"

	"kame/host/wasm"
	"solod.dev/so/c"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestHeapReusesUnorderedFreesAndCoalesces(t *testing.T) {
	a := t.Allocator()
	buf := slices.Make[byte](a, 4097)
	defer slices.Free(a, buf)
	h := wasm.NewHeap(buf[1:])
	for cycle := 0; cycle < 1000; cycle++ {
		one := mem.AllocSlice[byte](&h, 300, 300)
		two := mem.AllocSlice[byte](&h, 700, 700)
		three := mem.AllocSlice[byte](&h, 500, 500)
		mem.FreeSlice(&h, two)
		mem.FreeSlice(&h, one)
		mem.FreeSlice(&h, three)
		large, err := h.Alloc(4000, 32)
		if err != nil {
			t.Error("freed blocks did not coalesce")
			return
		}
		if uintptr(unsafe.Pointer(c.PtrAs[byte](large)))%32 != 0 {
			t.Error("unaligned backing storage broke requested alignment")
		}
		h.Free(large, 4000, 32)
	}
}

func TestHeapReallocPreservesDataAndFailedAllocation(t *testing.T) {
	a := t.Allocator()
	buf := slices.Make[byte](a, 2048)
	defer slices.Free(a, buf)
	h := wasm.NewHeap(buf)
	one := mem.AllocSlice[byte](&h, 128, 128)
	for i := range one {
		one[i] = byte(i + 1)
	}
	blocker := mem.AllocSlice[byte](&h, 128, 128)
	one = mem.ReallocSlice(&h, one, 512, 512)
	for i := 0; i < 128; i++ {
		if one[i] != byte(i+1) {
			t.Error("relocated allocation lost data")
			break
		}
	}
	for i := 128; i < len(one); i++ {
		if one[i] != 0 {
			t.Error("grown allocation was not zeroed")
			break
		}
	}
	_, err := h.Realloc(&one[0], 512, 4096, 1)
	if err != mem.ErrOutOfMemory || one[0] != 1 {
		t.Error("failed reallocation damaged its original block")
	}
	mem.FreeSlice(&h, blocker)
	one = mem.ReallocSlice(&h, one, 64, 64)
	one = mem.ReallocSlice(&h, one, 1024, 1024)
	if one[0] != 1 || one[63] != 64 || one[64] != 0 {
		t.Error("in-place resizing lost data or zeroing")
	}
	mem.FreeSlice(&h, one)
	large, err := h.Alloc(2000, 1)
	if err != nil {
		t.Error("reallocation left unreclaimable blocks")
		return
	}
	h.Free(large, 2000, 1)
}
