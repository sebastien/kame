package core

import (
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

// CatValueJSON encodes definition values that `do cat` prints in human mode
// as plain JSON. String and bytes values are written raw by the caller, so
// only JSON-native values are accepted here: nil, booleans, finite numbers,
// strings, lists, and records. Nested bytes are accepted when they hold valid
// UTF-8 text and encoded as JSON strings; every other non-JSON value
// (patterns, resources, callables, processes, binary bytes, and non-finite
// floats) reports false, leaving out untouched.
func CatValueJSON(a mem.Allocator, value Value, out *[]byte) bool {
	return valueJSON(a, value, out, true)
}

// ValueJSON encodes JSON-native values only, with lexical record key order.
func ValueJSON(a mem.Allocator, value Value, out *[]byte) bool {
	return valueJSON(a, value, out, false)
}

func valueJSON(a mem.Allocator, value Value, out *[]byte, allowBytes bool) bool {
	var builder strings.Builder
	e := json.NewEncoder(&builder)
	if !encodeCatValue(a, &e, value, 0, allowBytes) {
		builder.Free()
		return false
	}
	e.Flush()
	text := builder.String()
	data := mem.AllocSlice[byte](a, len(text), len(text))
	copy(data, text)
	builder.Free()
	*out = data
	return true
}

func encodeCatValue(a mem.Allocator, e *json.Encoder, value Value, depth int, allowBytes bool) bool {
	var order []int
	defer slices.Free(a, order)
	if depth >= 256 {
		return false
	}
	switch value.Kind {
	case Nil:
		e.Null()
	case Bool:
		e.Bool(value.Bool)
	case Int:
		e.Int(value.Int)
	case Float:
		if value.Float != value.Float {
			return false
		}
		if value.Float != 0 && value.Float/2 == value.Float {
			return false
		}
		if value.Float == 0 {
			e.Float(0)
		} else {
			e.Float(value.Float)
		}
	case String:
		if !allowBytes && !utf8.Valid([]byte(value.Text)) { return false }
		e.Str(value.Text)
	case Bytes:
		if !allowBytes || !utf8.Valid(value.Bytes) {
			return false
		}
		e.Str(string(value.Bytes))
	case List:
		e.BeginArray()
		for i := range value.List {
			if !encodeCatValue(a, e, value.List[i], depth+1, allowBytes) {
				return false
			}
		}
		e.EndArray()
	case Record:
		order = slices.Make[int](a, len(value.Record))
		for i := range order {
			order[i] = i
			if !allowBytes {
				for j := i; j > 0 && value.Record[order[j]].Key < value.Record[order[j-1]].Key; j-- { order[j], order[j-1] = order[j-1], order[j] }
			}
		}
		e.BeginObject()
		for _, i := range order {
			if !allowBytes && !utf8.Valid([]byte(value.Record[i].Key)) { return false }
			e.Str(value.Record[i].Key)
			if !encodeCatValue(a, e, value.Record[i].Value, depth+1, allowBytes) {
				return false
			}
		}
		e.EndObject()
	default:
		return false
	}
	return true
}
