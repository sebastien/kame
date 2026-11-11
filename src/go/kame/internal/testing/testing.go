package testing

import (
	"kame/internal/mem"
	"testing"
)

type T struct {
	*testing.T
	alloc mem.Tracker
}

func (t *T) Allocator() mem.Allocator {
	if t.alloc.Allocator == nil {
		t.alloc.Allocator = mem.System
	}
	return &t.alloc
}
func Wrap(t *testing.T) *T { return &T{T: t} }
