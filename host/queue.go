// Package host defines portable requests issued by evaluators and runtimes.
package host

import (
	"littlemake/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type Queue struct {
	Alloc mem.Allocator
	next  int64
	items []Request
}

func NewQueue(a mem.Allocator) *Queue {
	q := mem.Alloc[Queue](a)
	q.Alloc = a
	return q
}

func (q *Queue) Submit(nodeID int64, generation int64, attempt int64, kind RequestKind, payload core.Value) int64 {
	if q == nil || nodeID == 0 {
		return 0
	}
	q.next++
	q.items = slices.Append(q.Alloc, q.items, Request{ID: q.next, NodeID: nodeID, Generation: generation, Attempt: attempt, Kind: kind, Payload: payload.Clone(q.Alloc)})
	return q.next
}

type NextResult struct {
	Request Request
	OK      bool
}

// Next transfers the oldest request payload to its caller.
func (q *Queue) Next() NextResult {
	if q == nil || len(q.items) == 0 {
		return NextResult{}
	}
	request := q.items[0]
	copy(q.items, q.items[1:])
	q.items = q.items[:len(q.items)-1]
	return NextResult{Request: request, OK: true}
}

func (q *Queue) Free() {
	if q == nil {
		return
	}
	for i := range q.items {
		q.items[i].Free(q.Alloc)
	}
	slices.Free(q.Alloc, q.items)
	mem.Free(q.Alloc, q)
}
