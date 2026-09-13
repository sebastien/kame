// Package core provides LittleMake's portable reactive values and streams.
package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type Kind int

const (
	Nil Kind = iota
	Bool
	Int
	Float
	String
	Bytes
	List
)

// Value is immutable after publication. Its referenced storage is owned by the
// allocator passed to its constructor or Clone and must be released with Free.
type Value struct {
	Kind  Kind
	Bool  bool
	Int   int64
	Float float64
	Text  string
	Bytes []byte
	List  []Value
}

func NewString(a mem.Allocator, text string) Value {
	if len(text) == 0 {
		return Value{Kind: String}
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return Value{Kind: String, Text: string(b)}
}

func NewBytes(a mem.Allocator, data []byte) Value {
	b := slices.Clone(a, data)
	return Value{Kind: Bytes, Bytes: b}
}

func NewList(a mem.Allocator, values []Value) Value {
	list := slices.Make[Value](a, len(values))
	for i := range values {
		list[i] = values[i].Clone(a)
	}
	return Value{Kind: List, List: list}
}

func (v *Value) Clone(a mem.Allocator) Value {
	copy := *v
	switch v.Kind {
	case String:
		copy = NewString(a, v.Text)
	case Bytes:
		copy = NewBytes(a, v.Bytes)
	case List:
		copy = NewList(a, v.List)
	}
	return copy
}

func (v *Value) Free(a mem.Allocator) {
	switch v.Kind {
	case String:
		mem.FreeString(a, v.Text)
	case Bytes:
		slices.Free(a, v.Bytes)
	case List:
		for i := range v.List {
			v.List[i].Free(a)
		}
		slices.Free(a, v.List)
	}
	*v = Value{}
}
