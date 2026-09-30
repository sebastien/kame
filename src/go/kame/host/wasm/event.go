package wasm

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

const EventSchemaVersion uint16 = 1
const EventHeaderSize = 48

// EventHeader is an in-memory description only. ABI consumers receive its
// fixed-width little-endian encoding, never this Go layout.
type EventHeader struct {
	Schema     uint16
	Kind       uint16
	Root       Handle
	Node       Handle
	Request    Handle
	Generation int64
	Revision   int64
	PayloadLen uint32
}

type QueuedEvent struct {
	Header  EventHeader
	Payload []byte
}

// EventQueue enforces the ABI's event pinning rule. Next makes one event
// current but does not expose its payload allocation. Copy or Discard releases
// that pin; a later emitted event cannot replace it beforehand.
type EventQueue struct {
	alloc   mem.Allocator
	items   []QueuedEvent
	current QueuedEvent
	pinned  bool
}

type NextEvent struct {
	Header EventHeader
	OK     bool
}

func NewEventQueue(a mem.Allocator) *EventQueue {
	q := mem.Alloc[EventQueue](a)
	q.alloc = a
	return q
}

func (q *EventQueue) Emit(header EventHeader, payload []byte) bool {
	if q == nil {
		return false
	}
	header.Schema = EventSchemaVersion
	header.PayloadLen = uint32(len(payload))
	q.items = slices.Append(q.alloc, q.items, QueuedEvent{Header: header, Payload: slices.Clone(q.alloc, payload)})
	return true
}

// Next pins the oldest unconsumed event. Repeated calls return its unchanged
// metadata until Copy or Discard resolves it.
func (q *EventQueue) Next() NextEvent {
	if q == nil {
		return NextEvent{}
	}
	if !q.pinned {
		if len(q.items) == 0 {
			return NextEvent{}
		}
		q.current = q.items[0]
		copy(q.items, q.items[1:])
		q.items = q.items[:len(q.items)-1]
		q.pinned = true
	}
	return NextEvent{Header: q.current.Header, OK: true}
}

// Copy releases the pinned event only after a complete caller-owned copy.
func (q *EventQueue) Copy(dst []byte) bool {
	if q == nil || !q.pinned || !CopyPayload(dst, q.current.Payload) {
		return false
	}
	q.discardCurrent()
	return true
}

func (q *EventQueue) Discard() bool {
	if q == nil || !q.pinned {
		return false
	}
	q.discardCurrent()
	return true
}

func (q *EventQueue) discardCurrent() {
	slices.Free(q.alloc, q.current.Payload)
	q.current, q.pinned = QueuedEvent{}, false
}

func (q *EventQueue) Free() {
	if q == nil {
		return
	}
	if q.pinned {
		q.discardCurrent()
	}
	for i := range q.items {
		slices.Free(q.alloc, q.items[i].Payload)
	}
	slices.Free(q.alloc, q.items)
	mem.Free(q.alloc, q)
}

func putU16(dst []byte, at int, value uint16) { dst[at], dst[at+1] = byte(value), byte(value>>8) }
func putU32(dst []byte, at int, value uint32) {
	for i := 0; i < 4; i++ {
		dst[at+i] = byte(value >> uint(i*8))
	}
}
func putU64(dst []byte, at int, value uint64) {
	for i := 0; i < 8; i++ {
		dst[at+i] = byte(value >> uint(i*8))
	}
}

// EncodeEventHeader writes the stable ABI envelope. It returns false without
// writing when dst cannot hold the complete header.
func EncodeEventHeader(dst []byte, header EventHeader) bool {
	if len(dst) < EventHeaderSize {
		return false
	}
	if header.Schema == 0 {
		header.Schema = EventSchemaVersion
	}
	putU16(dst, 0, header.Schema)
	putU16(dst, 2, header.Kind)
	putU64(dst, 4, uint64(header.Root))
	putU64(dst, 12, uint64(header.Node))
	putU64(dst, 20, uint64(header.Request))
	putU64(dst, 28, uint64(header.Generation))
	putU64(dst, 36, uint64(header.Revision))
	putU32(dst, 44, header.PayloadLen)
	return true
}

// CopyPayload copies a pinned event payload into caller-owned output storage.
// It never returns a pointer into a WebAssembly allocation.
func CopyPayload(dst []byte, payload []byte) bool {
	if len(dst) < len(payload) {
		return false
	}
	copy(dst, payload)
	return true
}

// ClonePayload is kept in this package to make the ABI ownership boundary
// explicit at the call site.
func ClonePayload(a mem.Allocator, payload []byte) []byte {
	return slices.Clone(a, payload)
}
