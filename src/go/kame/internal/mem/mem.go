// Package mem bridges explicit object lifetimes while Go owns the memory.
// Typed allocations always use Go's heap so pointers remain visible to the GC.
package mem

import (
	"errors"
	"reflect"
	"runtime"
	"unsafe"
)

type Allocator interface {
	Alloc(int, int) (any, error)
	Realloc(any, int, int, int) (any, error)
	Free(any, int, int)
}

var ErrOutOfMemory = errors.New("out of memory")

type system struct{}

var System Allocator = system{}

func (system) Alloc(n, a int) (any, error) {
	b := make([]byte, n+a)
	p := unsafe.Pointer(&b[0])
	off := (-uintptr(p)) & uintptr(a-1)
	return (*byte)(unsafe.Add(p, off)), nil
}
func (s system) Realloc(p any, o, n, a int) (any, error) {
	q, e := s.Alloc(n, a)
	if e == nil {
		Copy(q, p, min(o, n))
	}
	return q, e
}
func (system) Free(any, int, int) {}
func Alloc[T any](a Allocator) *T { return new(T) }
func Free[T any](a Allocator, p *T) {
	if p != nil {
		var zero T
		*p = zero
	}
}
func rawBytes[T any](a Allocator) bool {
	_, raw := a.(interface{ RawBuffers() })
	_, isByte := any(*new(T)).(byte)
	return raw && isByte
}
func AllocSlice[T any](a Allocator, n, c int) []T {
	if rawBytes[T](a) && c > 0 {
		p, e := a.Alloc(c, 1)
		if e != nil {
			panic(e)
		}
		return unsafe.Slice((*T)(pointer(p)), c)[:n]
	}
	return make([]T, n, c)
}
func ReallocSlice[T any](a Allocator, s []T, n, c int) []T {
	r, e := TryReallocSlice(a, s, n, c)
	if e != nil {
		panic(e)
	}
	return r
}
func TryReallocSlice[T any](a Allocator, s []T, n, c int) ([]T, error) {
	if rawBytes[T](a) {
		if c == 0 {
			FreeSlice(a, s)
			return nil, nil
		}
		var p any
		var e error
		if cap(s) == 0 {
			p, e = a.Alloc(c, 1)
		} else {
			p, e = a.Realloc(unsafe.SliceData(s), cap(s), c, 1)
		}
		if e != nil {
			return nil, e
		}
		return unsafe.Slice((*T)(pointer(p)), c)[:n], nil
	}
	r := make([]T, n, c)
	copy(r[:cap(r)], s[:cap(s)])
	return r, nil
}
func FreeSlice[T any](a Allocator, s []T) {
	if rawBytes[T](a) && cap(s) > 0 {
		a.Free(unsafe.SliceData(s), cap(s), 1)
	}
}
func FreeString(a Allocator, s string) {}
func pointer(p any) unsafe.Pointer {
	if p == nil {
		return nil
	}
	return unsafe.Pointer(reflect.ValueOf(p).Pointer())
}
func Copy(d, s any, n int) any {
	copy(unsafe.Slice((*byte)(pointer(d)), n), unsafe.Slice((*byte)(pointer(s)), n))
	return d
}

// Tracker reports process-wide Go heap statistics. Its counters include every
// Go allocation, including allocations outside this adapter.
type Tracker struct{ Allocator Allocator }
type Stats struct{ Alloc, TotalAlloc, Mallocs, Frees uint64 }

func (t *Tracker) Alloc(n, a int) (any, error)             { return t.Allocator.Alloc(n, a) }
func (t *Tracker) Realloc(p any, o, n, a int) (any, error) { return t.Allocator.Realloc(p, o, n, a) }
func (t *Tracker) Free(p any, n, a int)                    { t.Allocator.Free(p, n, a) }
func (t *Tracker) Stats() Stats {
	var s runtime.MemStats
	runtime.ReadMemStats(&s)
	return Stats{Alloc: s.HeapAlloc, TotalAlloc: s.TotalAlloc, Mallocs: s.Mallocs, Frees: s.Frees}
}
