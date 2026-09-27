// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type Capability int

const (
	Read Capability = iota
	Write
	Run
	Env
)

type Grant struct {
	Capability Capability
	Names      []string
}

type OperationCall func(*Context, any, []core.Value) Result
type ContextFree func(mem.Allocator, any)

type Operation struct {
	Name         string
	Call         OperationCall
	Context      any
	FreeContext  ContextFree
	MinArity     int
	MaxArity     int // -1 accepts any remaining arguments.
	Capabilities []Capability
	Version      string
}

type Registry struct {
	Alloc mem.Allocator
	Items []Operation
}

// TaskKeyResult identifies one operation implementation for a cached task.
// Callers own Key when OK is true.
type TaskKeyResult struct {
	Key core.ResourceKey
	OK  bool
}

// TaskKey includes the stable operation version so a new implementation cannot
// reuse a cached result produced by an older one.
func (r *Registry) TaskKey(a mem.Allocator, task string, operation string) TaskKeyResult {
	o := r.lookup(operation)
	if o == nil {
		return TaskKeyResult{}
	}
	b := strings.NewBuilder(a)
	defer b.Free()
	b.WriteString(task)
	b.WriteByte('\x00')
	b.WriteString(o.Name)
	b.WriteByte('\x00')
	b.WriteString(o.Version)
	return TaskKeyResult{Key: core.NewResourceKey(a, core.ResourceTask, b.String()), OK: true}
}

func NewRegistry(a mem.Allocator) *Registry {
	r := mem.Alloc[Registry](a)
	r.Alloc = a
	return r
}

func (r *Registry) Add(operation Operation) bool {
	if r == nil || operation.Name == "" || operation.Call == nil || r.lookup(operation.Name) != nil {
		return false
	}
	operation.Name = owned(r.Alloc, operation.Name)
	operation.Version = owned(r.Alloc, operation.Version)
	operation.Capabilities = slices.Clone(r.Alloc, operation.Capabilities)
	r.Items = slices.Append(r.Alloc, r.Items, operation)
	return true
}

func (r *Registry) lookup(name string) *Operation {
	if r == nil {
		return nil
	}
	for i := range r.Items {
		if r.Items[i].Name == name {
			return &r.Items[i]
		}
	}
	return nil
}

func (r *Registry) Free() {
	if r == nil {
		return
	}
	for i := range r.Items {
		o := &r.Items[i]
		if o.FreeContext != nil {
			o.FreeContext(r.Alloc, o.Context)
		}
		mem.FreeString(r.Alloc, o.Name)
		mem.FreeString(r.Alloc, o.Version)
		slices.Free(r.Alloc, o.Capabilities)
	}
	slices.Free(r.Alloc, r.Items)
	mem.Free(r.Alloc, r)
}
