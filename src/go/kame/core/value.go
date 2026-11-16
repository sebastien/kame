package core

import (
	"kame/internal/mem"
	"slices"
	"strings"
)

type Kind int

// KindName is the user-facing name of a value's kind, never its contents.
func KindName(kind Kind) string {
	switch kind {
	case Nil:
		return "nil"
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Float:
		return "float"
	case String:
		return "string"
	case Bytes:
		return "bytes"
	case List:
		return "list"
	case Record:
		return "record"
	case Callable:
		return "callable"
	case Module:
		return "kash-module"
	case Resource:
		return "resource"
	case Pattern:
		return "pattern"
	case Process:
		return "process"
	}
	return "unknown"
}

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
	Pattern
	Process
	Module
)

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
	// ToolName retains executable lookup provenance on resolved string values.
	ToolName string
	// GlobPattern preserves literal interpolation bytes when used by wildcard.
	GlobPattern string
	Bytes       []byte
	List        []Value
	Record      []RecordField
	Callable    any
	// CallableOwner marks immutable invocation-owned callables. Their owner
	// outlives engine snapshots; ordinary lexical closures remain unpublished.
	CallableOwner any
	// CallableSignature is present only for callables with a stable semantic identity.
	// Its storage is owned by CallableOwner and outlives published values.
	CallableSignature *Signature
	// Process is opaque and borrowed from its invocation owner, never serialized.
	Process any
	// Module is an opaque invocation-owned library; its owner outlives snapshots.
	Module          any
	ModuleOwner     any
	ModuleSignature *Signature
	Resource        ResourceKey
	// Shared counts the owners of an immutable list or record backing. Clone
	// adds an owner in O(1); Free releases the elements with the last owner.
	// Shared values never contain callables, which need per-owner wrappers.
	Shared *SharedStorage
}

// SharedStorage is the owner count of one shared list or record backing.
type SharedStorage struct{ References int }

func NewString(a mem.Allocator, text string) Value {
	if len(text) == 0 {
		return Value{Kind: String}
	}
	return Value{Kind: String, Text: strings.Clone(text)}
}

func NewBytes(a mem.Allocator, data []byte) Value {
	b := slices.Clone(data)
	return Value{Kind: Bytes, Bytes: b}
}

func NewList(a mem.Allocator, values []Value) Value {
	list := make([]Value, len(values))
	for i := range values {
		list[i] = values[i].Clone(a)
	}
	return Value{Kind: List, List: list}
}

func NewRecord(a mem.Allocator, fields []RecordField) Value {
	record := make([]RecordField, len(fields))
	for i := range fields {
		record[i].Key = NewString(a, fields[i].Key).Text
		record[i].Value = fields[i].Value.Clone(a)
	}
	return Value{Kind: Record, Record: record}
}

func NewResource(a mem.Allocator, kind ResourceKind, name string) Value {
	return Value{Kind: Resource, Resource: NewResourceKey(a, kind, name)}
}

// NewPattern returns a pattern value carrying its canonical text.
func NewPattern(a mem.Allocator, text string) Value {
	if len(text) == 0 {
		return Value{Kind: Pattern}
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return Value{Kind: Pattern, Text: string(b)}
}

func (v *Value) Clone(a mem.Allocator) Value {
	if v.Shared != nil {
		v.Shared.References++
		return *v
	}
	copy := *v
	switch v.Kind {
	case String:
		copy = NewString(a, v.Text)
		copy.ToolName = NewString(a, v.ToolName).Text
		copy.GlobPattern = NewString(a, v.GlobPattern).Text
	case Bytes:
		copy = NewBytes(a, v.Bytes)
	case List:
		copy = NewList(a, v.List)
	case Record:
		copy = NewRecord(a, v.Record)
	case Resource:
		copy = Value{Kind: Resource, Resource: v.Resource.Clone(a)}
	case Pattern:
		copy = NewPattern(a, v.Text)
	}
	return copy
}

// Share makes an owned list or record backing shareable, so later clones are
// O(1). Values containing callables stay private. The backing is clipped so an
// append by any owner reallocates rather than writing past a shared length.
func (v *Value) Share(a mem.Allocator) {
	if v.Shared != nil || (v.Kind != List && v.Kind != Record) || v.HasCallable() {
		return
	}
	v.List, v.Record = slices.Clip(v.List), slices.Clip(v.Record)
	v.Shared = mem.Alloc[SharedStorage](a)
	v.Shared.References = 1
}

// CloneOwned returns a deep copy with no shared backing at any depth, for
// consumers outside the evaluator that own and may mutate what they receive.
func (v *Value) CloneOwned(a mem.Allocator) Value {
	switch v.Kind {
	case List:
		list := make([]Value, len(v.List))
		for i := range v.List {
			list[i] = v.List[i].CloneOwned(a)
		}
		return Value{Kind: List, List: list}
	case Record:
		record := make([]RecordField, len(v.Record))
		for i := range v.Record {
			record[i].Key = NewString(a, v.Record[i].Key).Text
			record[i].Value = v.Record[i].Value.CloneOwned(a)
		}
		return Value{Kind: Record, Record: record}
	}
	return v.Clone(a)
}

// Unshare gives v a private backing that its owner may mutate in place.
func (v *Value) Unshare(a mem.Allocator) {
	if v.Shared == nil {
		return
	}
	private := *v
	private.Shared = nil
	private = private.Clone(a)
	v.Free(a)
	*v = private
}

func (v *Value) HasCallable() bool {
	if v.Shared != nil {
		return false
	}
	if v.Kind == Callable {
		return true
	}
	if v.Kind == List {
		for i := range v.List {
			if v.List[i].HasCallable() {
				return true
			}
		}
	}
	if v.Kind == Record {
		for i := range v.Record {
			if v.Record[i].Value.HasCallable() {
				return true
			}
		}
	}
	return false
}

func (v *Value) HasTransientCallable() bool {
	if v.Shared != nil {
		return false
	}
	if v.Kind == Callable {
		return v.CallableOwner == nil
	}
	for i := range v.List {
		if v.List[i].HasTransientCallable() {
			return true
		}
	}
	for i := range v.Record {
		if v.Record[i].Value.HasTransientCallable() {
			return true
		}
	}
	return false
}

func (v *Value) Free(a mem.Allocator) {
	if v.Shared != nil {
		v.Shared.References--
		if v.Shared.References > 0 {
			*v = Value{}
			return
		}
		mem.Free(a, v.Shared)
	}
	switch v.Kind {
	case String:
		mem.FreeString(a, v.Text)
		mem.FreeString(a, v.ToolName)
		mem.FreeString(a, v.GlobPattern)
	case Bytes:
		mem.FreeSlice(nil, v.Bytes)
	case List:
		for i := range v.List {
			v.List[i].Free(a)
		}
		mem.FreeSlice(nil, v.List)
	case Record:
		for i := range v.Record {
			mem.FreeString(a, v.Record[i].Key)
			v.Record[i].Value.Free(a)
		}
		mem.FreeSlice(nil, v.Record)
	case Resource:
		v.Resource.Free(a)
	case Pattern:
		mem.FreeString(a, v.Text)
	}
	*v = Value{}
}
