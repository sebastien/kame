// Package wasm contains ABI-stable primitives for a freestanding WebAssembly
// host. It deliberately has no dependency on program or a native host.
package wasm

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Handle is a module-global, opaque identifier. Its low 32 bits are a one-based
// table index and its high 32 bits are a generation. Zero is never valid.
type Handle uint64

const invalidHandle Handle = 0

type handleSlot struct {
	Owner      uint64
	Value      uint64
	Generation uint32
	Live       bool
	Retired    bool
}

// Table owns a module-global handle namespace. Owner is an opaque instance
// token, allowing ABI entrypoints to reject a live handle from another instance.
type Table struct {
	Alloc mem.Allocator
	slots []handleSlot
}

func NewTable(a mem.Allocator) *Table {
	t := mem.Alloc[Table](a)
	t.Alloc = a
	return t
}

func makeHandle(index int, generation uint32) Handle {
	return Handle(uint64(generation)<<32 | uint64(index+1))
}

type handleParts struct {
	Index      int
	Generation uint32
	OK         bool
}

func splitHandle(handle Handle) handleParts {
	if handle == invalidHandle {
		return handleParts{}
	}
	index := int(uint64(handle)&0xffffffff) - 1
	generation := uint32(uint64(handle) >> 32)
	return handleParts{Index: index, Generation: generation, OK: index >= 0 && generation != 0}
}

// Add allocates a handle for owner and its opaque value. It returns zero only
// when no reusable slot remains or allocation fails in the supplied allocator.
func (t *Table) Add(owner uint64, value uint64) Handle {
	if t == nil || owner == 0 {
		return invalidHandle
	}
	for i := range t.slots {
		slot := &t.slots[i]
		if !slot.Live && !slot.Retired {
			slot.Live, slot.Owner, slot.Value = true, owner, value
			if slot.Generation == 0 {
				slot.Generation = 1
			}
			return makeHandle(i, slot.Generation)
		}
	}
	t.slots = slices.Append(t.Alloc, t.slots, handleSlot{Owner: owner, Value: value, Generation: 1, Live: true})
	return makeHandle(len(t.slots)-1, 1)
}

// Get resolves handle only when it remains live and belongs to owner.
func (t *Table) Get(owner uint64, handle Handle) (uint64, bool) {
	parts := splitHandle(handle)
	if !parts.OK || t == nil || parts.Index >= len(t.slots) {
		return 0, false
	}
	slot := t.slots[parts.Index]
	if !slot.Live || slot.Owner != owner || slot.Generation != parts.Generation {
		return 0, false
	}
	return slot.Value, true
}

// Free invalidates handle. Generation overflow retires the slot permanently,
// preventing a stale handle from ever becoming valid again.
func (t *Table) Free(owner uint64, handle Handle) bool {
	parts := splitHandle(handle)
	if !parts.OK || t == nil || parts.Index >= len(t.slots) {
		return false
	}
	slot := &t.slots[parts.Index]
	if !slot.Live || slot.Owner != owner || slot.Generation != parts.Generation {
		return false
	}
	slot.Live, slot.Owner, slot.Value = false, 0, 0
	if slot.Generation == ^uint32(0) {
		slot.Retired = true
	} else {
		slot.Generation++
	}
	return true
}

func (t *Table) FreeAll(owner uint64) {
	if t == nil || owner == 0 {
		return
	}
	for i := range t.slots {
		slot := &t.slots[i]
		if slot.Live && slot.Owner == owner {
			handle := makeHandle(i, slot.Generation)
			t.Free(owner, handle)
		}
	}
}

func (t *Table) FreeTable() {
	if t == nil {
		return
	}
	slices.Free(t.Alloc, t.slots)
	mem.Free(t.Alloc, t)
}
