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
