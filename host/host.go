// Package host defines portable requests issued by evaluators and runtimes.
package host

import (
	"littlemake/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type RequestKind int

const (
	RequestCustom RequestKind = iota
	RequestReadFile
	RequestWriteFile
	RequestProcess
	RequestEnvironment
)

// Payload record fields shared by library operations and runtime hosts.
const (
	FieldOp     = "op"
	FieldPath   = "path"
	FieldData   = "data"
	FieldScript = "script"
	OpRead      = "read"
	OpExists    = "exists"
	OpStat      = "stat"
	OpWildcard  = "wildcard"
)

// FilePayload creates a filesystem request payload. A string payload remains a
// read request for callers that predate the record protocol.
func FilePayload(a mem.Allocator, op string, name string) core.Value {
	fields := []core.RecordField{{Key: FieldOp, Value: core.NewString(a, op)}, {Key: FieldPath, Value: core.NewString(a, name)}}
	payload := core.NewRecord(a, fields)
	for i := range fields { fields[i].Value.Free(a) }
	return payload
}

func ProcessPayload(a mem.Allocator, script string) core.Value {
	fields := []core.RecordField{{Key: FieldScript, Value: core.NewString(a, script)}}
	payload := core.NewRecord(a, fields)
	fields[0].Value.Free(a)
	return payload
}

func WritePayload(a mem.Allocator, name string, data []byte) core.Value {
	fields := []core.RecordField{{Key: FieldPath, Value: core.NewString(a, name)}, {Key: FieldData, Value: core.NewBytes(a, data)}}
	payload := core.NewRecord(a, fields)
	for i := range fields { fields[i].Value.Free(a) }
	return payload
}

// PayloadPath returns the path or environment name carried by a request.
func PayloadPath(payload core.Value) string {
	if payload.Kind == core.String { return payload.Text }
	if payload.Kind != core.Record { return "" }
	for i := range payload.Record { if payload.Record[i].Key == FieldPath && payload.Record[i].Value.Kind == core.String { return payload.Record[i].Value.Text } }
	return ""
}

func PayloadText(payload core.Value, field string) string {
	if payload.Kind == core.String && field == FieldScript { return payload.Text }
	if payload.Kind != core.Record { return "" }
	for i := range payload.Record { if payload.Record[i].Key == field && payload.Record[i].Value.Kind == core.String { return payload.Record[i].Value.Text } }
	return ""
}

func PayloadBytes(payload core.Value, field string) []byte {
	if payload.Kind != core.Record { return nil }
	for i := range payload.Record { if payload.Record[i].Key == field && payload.Record[i].Value.Kind == core.Bytes { return payload.Record[i].Value.Bytes } }
	return nil
}

// Request owns Payload until it is popped or the queue is freed.
type Request struct {
	ID         int64
	NodeID     int64
	Generation int64
	Attempt    int64
	Kind       RequestKind
	Payload    core.Value
}

func (r *Request) Free(a mem.Allocator) { r.Payload.Free(a); *r = Request{} }

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
	if q == nil || nodeID == 0 { return 0 }
	q.next++
	q.items = slices.Append(q.Alloc, q.items, Request{ID: q.next, NodeID: nodeID, Generation: generation, Attempt: attempt, Kind: kind, Payload: payload.Clone(q.Alloc)})
	return q.next
}

type NextResult struct { Request Request; OK bool }

// Next transfers the oldest request payload to its caller.
func (q *Queue) Next() NextResult {
	if q == nil || len(q.items) == 0 { return NextResult{} }
	request := q.items[0]
	copy(q.items, q.items[1:])
	q.items = q.items[:len(q.items)-1]
	return NextResult{Request: request, OK: true}
}

func (q *Queue) Free() {
	if q == nil { return }
	for i := range q.items { q.items[i].Free(q.Alloc) }
	slices.Free(q.Alloc, q.items)
	mem.Free(q.Alloc, q)
}
