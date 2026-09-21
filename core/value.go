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
	Record
	Callable
	Resource
)

type ResourceKind int

const (
	ResourceDefinition ResourceKind = iota
	ResourceTarget
	ResourceFile
	ResourceTask
	ResourceService
	ResourceGlob
	ResourceEnvironment
)

// ResourceKey is owned by the allocator that created or cloned it.
type ResourceKey struct {
	Kind ResourceKind
	Name string
}

func NewResourceKey(a mem.Allocator, kind ResourceKind, name string) ResourceKey {
	if name == "" { return ResourceKey{Kind: kind} }
	b := mem.AllocSlice[byte](a, len(name), len(name))
	copy(b, []byte(name))
	return ResourceKey{Kind: kind, Name: string(b)}
}

func (k *ResourceKey) Clone(a mem.Allocator) ResourceKey {
	return NewResourceKey(a, k.Kind, k.Name)
}

func (k *ResourceKey) Free(a mem.Allocator) {
	mem.FreeString(a, k.Name)
	*k = ResourceKey{}
}

type RecordField struct {
	Key   string
	Value Value
}

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
	Record []RecordField
	Callable any
	Resource ResourceKey
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

func NewRecord(a mem.Allocator, fields []RecordField) Value {
	record := slices.Make[RecordField](a, len(fields))
	for i := range fields {
		record[i].Key = NewString(a, fields[i].Key).Text
		record[i].Value = fields[i].Value.Clone(a)
	}
	return Value{Kind: Record, Record: record}
}

func NewResource(a mem.Allocator, kind ResourceKind, name string) Value {
	return Value{Kind: Resource, Resource: NewResourceKey(a, kind, name)}
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
	case Record:
		copy = NewRecord(a, v.Record)
	case Resource:
		copy = Value{Kind: Resource, Resource: v.Resource.Clone(a)}
	}
	return copy
}

func (v *Value) HasCallable() bool {
	if v.Kind == Callable { return true }
	if v.Kind == List {
		for i := range v.List { if v.List[i].HasCallable() { return true } }
	}
	if v.Kind == Record {
		for i := range v.Record { if v.Record[i].Value.HasCallable() { return true } }
	}
	return false
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
	case Record:
		for i := range v.Record {
			mem.FreeString(a, v.Record[i].Key)
			v.Record[i].Value.Free(a)
		}
		slices.Free(a, v.Record)
	case Resource:
		v.Resource.Free(a)
	}
	*v = Value{}
}
