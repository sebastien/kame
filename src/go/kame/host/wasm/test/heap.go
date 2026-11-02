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

func TestHeapFragmentationPreservesLiveBlocks(t *testing.T) {
	a := t.Allocator()
	buf := slices.Make[byte](a, 262145)
	defer slices.Free(a, buf)
	h := wasm.NewHeap(buf[1:])
	var pointers [128]any
	var sizes [128]int
	seed := uint32(7)
	for step := 0; step < 8192; step++ {
		seed = seed*1664525 + 1013904223
		i := int((seed >> 16) % 128)
		if pointers[i] != nil {
			data := unsafe.Slice(c.PtrAs[byte](pointers[i]), sizes[i])
			for j := range data {
				if data[j] != byte(i+1) {
					t.Fatal("fragmentation corrupted a live allocation")
					return
				}
			}
			if seed&1 == 0 {
				h.Free(pointers[i], sizes[i], 32)
				pointers[i], sizes[i] = nil, 0
				continue
			}
		}
		size := int((seed>>8)%1024) + 1
		var pointer any
		var err error
		if pointers[i] == nil {
			pointer, err = h.Alloc(size, 32)
		} else {
			pointer, err = h.Realloc(pointers[i], sizes[i], size, 32)
		}
		if err != nil {
			t.Fatal("fragmented heap could not satisfy a bounded allocation")
			return
		}
		if uintptr(unsafe.Pointer(c.PtrAs[byte](pointer)))%32 != 0 {
			t.Error("fragmented allocation lost alignment")
		}
		data := unsafe.Slice(c.PtrAs[byte](pointer), size)
		for j := range data {
			if j < sizes[i] && data[j] != byte(i+1) {
				t.Error("reallocation lost existing bytes")
			}
			if j >= sizes[i] && data[j] != 0 {
				t.Error("new allocation bytes were not zeroed")
			}
			data[j] = byte(i + 1)
		}
		pointers[i], sizes[i] = pointer, size
	}
	// A mostly empty heap must retain a useful large range despite small churn.
	reserve, reserveErr := h.Alloc(65536, 32)
	if reserveErr != nil {
		t.Error("small allocation churn fragmented the large free ranges")
	} else {
		h.Free(reserve, 65536, 32)
	}
	for i := range pointers {
		h.Free(pointers[i], sizes[i], 32)
	}
	if h.UsedBytes != 0 {
		t.Error("fragmented heap retained freed allocations")
	}
	large, err := h.Alloc(262000, 32)
	if err != nil {
		t.Error("fragmented heap did not fully coalesce")
		return
	}
	h.Free(large, 262000, 32)
}
