package wasm

import (
	"unsafe"

	"solod.dev/so/c"
	"solod.dev/so/mem"
)

// Heap owns no memory outside its supplied buffer. Physical blocks keep their
// size, predecessor and payload offset in byte headers, allowing arbitrary
// frees to coalesce without allocating bookkeeping. Unlike a bump arena, it
// reclaims short-lived events while the compiled program remains alive.
type Heap struct {
	buf []byte
}

const heapHeader = 12
const heapNoPrevious = uint32(0xffffffff)

func NewHeap(buf []byte) Heap {
	h := Heap{buf: buf}
	h.Reset()
	return h
}

func (h *Heap) Reset() {
	if len(h.buf) >= heapHeader+4 && uint64(len(h.buf)) < uint64(heapNoPrevious) {
		h.put(0, uint32(len(h.buf)))
		h.put(4, heapNoPrevious)
		h.put(8, 0)
	}
}

func (h *Heap) get(offset int) uint32 {
	return uint32(h.buf[offset]) | uint32(h.buf[offset+1])<<8 | uint32(h.buf[offset+2])<<16 | uint32(h.buf[offset+3])<<24
}

func (h *Heap) put(offset int, value uint32) {
	for i := 0; i < 4; i++ {
		h.buf[offset+i] = byte(value >> (8 * i))
	}
}

func (h *Heap) Alloc(size int, align int) (any, error) {
	if size <= 0 || align <= 0 || align&(align-1) != 0 {
		panic("wasm heap: invalid size or alignment")
	}
	if len(h.buf) < heapHeader+4 || uint64(len(h.buf)) >= uint64(heapNoPrevious) || size > len(h.buf) || align > len(h.buf) {
		return nil, mem.ErrOutOfMemory
	}
	base := uintptr(unsafe.Pointer(&h.buf[0]))
	for block := 0; block < len(h.buf); {
		length := int(h.get(block))
		if h.get(block+8) == 0 {
			start := block + heapHeader + 4
			padding := int((0 - (base + uintptr(start))) & uintptr(align-1))
			data := start + padding
			if data <= block+length && size <= block+length-data {
				h.split(block, data+size)
				h.put(block+8, uint32(data))
				h.put(data-4, uint32(block))
				clear(h.buf[data : data+size])
				return &h.buf[data], nil
			}
		}
		block += length
	}
	return nil, mem.ErrOutOfMemory
}

// split keeps a usable remainder and updates its successor's predecessor.
func (h *Heap) split(block int, end int) {
	oldEnd := block + int(h.get(block))
	if oldEnd-end < heapHeader+4+1 {
		return
	}
	h.put(block, uint32(end-block))
	h.put(end, uint32(oldEnd-end))
	h.put(end+4, uint32(block))
	h.put(end+8, 0)
	if oldEnd < len(h.buf) {
		h.put(oldEnd+4, uint32(end))
	}
}

func (h *Heap) block(ptr any) int {
	_ = h
	p := (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(c.PtrAs[byte](ptr))) - 4))
	return int(uint32(*p) | uint32(*c.PtrAdd(p, 1))<<8 | uint32(*c.PtrAdd(p, 2))<<16 | uint32(*c.PtrAdd(p, 3))<<24)
}

func (h *Heap) mergeNext(block int) {
	end := block + int(h.get(block))
	if end < len(h.buf) && h.get(end+8) == 0 {
		end += int(h.get(end))
		h.put(block, uint32(end-block))
		if end < len(h.buf) {
			h.put(end+4, uint32(block))
		}
	}
}

func (h *Heap) Free(ptr any, size int, align int) {
	_ = size
	_ = align
	if ptr == nil {
		return
	}
	block := h.block(ptr)
	h.put(block+8, 0)
	h.mergeNext(block)
	previous := h.get(block + 4)
	if previous != heapNoPrevious && h.get(int(previous)+8) == 0 {
		h.mergeNext(int(previous))
	}
}

func (h *Heap) Realloc(ptr any, oldSize int, newSize int, align int) (any, error) {
	if oldSize <= 0 || newSize <= 0 || align <= 0 || align&(align-1) != 0 {
		panic("wasm heap: invalid reallocation")
	}
	block := h.block(ptr)
	data := int(h.get(block + 8))
	if newSize <= oldSize {
		h.split(block, data+newSize)
		end := block + int(h.get(block))
		if end < len(h.buf) && h.get(end+8) == 0 {
			h.mergeNext(end)
		}
		return ptr, nil
	}
	end := block + int(h.get(block))
	if end < len(h.buf) && h.get(end+8) == 0 && newSize <= end+int(h.get(end))-data {
		h.mergeNext(block)
		h.split(block, data+newSize)
		clear(h.buf[data+oldSize : data+newSize])
		return ptr, nil
	}
	if newSize <= end-data {
		clear(h.buf[data+oldSize : data+newSize])
		return ptr, nil
	}
	next, err := h.Alloc(newSize, align)
	if err != nil {
		return nil, err
	}
	mem.Copy(next, ptr, oldSize)
	h.Free(ptr, oldSize, align)
	return next, nil
}
