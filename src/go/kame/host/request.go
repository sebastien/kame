// Package host defines portable requests issued by evaluators and runtimes.
package host

import (
	"kame/core"
	"solod.dev/so/mem"
)

type RequestKind int

const (
	RequestCustom RequestKind = iota
	RequestReadFile
	RequestWriteFile
	RequestProcess
	RequestEnvironment
	// RequestStatPath and RequestExpandGlob remain distinct from reads so hosts
	// can grant, schedule, and cache them without interpreting an operation
	// field in a generic read request.
	RequestStatPath
	RequestExpandGlob
	RequestWallTime
	RequestMonotonicTime
	// Cache operations transport opaque, canonically encoded records. The
	// portable runtime owns validation and key construction; hosts only persist
	// and retrieve bytes.
	RequestCacheGet
	RequestCachePut
	RequestCacheDelete
	// PrepareOutputs creates implicit parent directories for structured recipes.
	RequestPrepareOutputs
	RequestTimer
	RequestProcessCancel
	RequestCacheLock
	RequestCacheUnlock
	// RequestPlugin invokes one explicitly registered, versioned operation.
	RequestPlugin
)

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
