package core

import (
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
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
	var builder strings.Builder
	e := json.NewEncoder(&builder)
	if !encodeCatValue(&e, value, 0) {
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

func encodeCatValue(e *json.Encoder, value Value, depth int) bool {
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
		e.Str(value.Text)
	case Bytes:
		if !utf8.Valid(value.Bytes) {
			return false
		}
		e.Str(string(value.Bytes))
	case List:
		e.BeginArray()
		for i := range value.List {
			if !encodeCatValue(e, value.List[i], depth+1) {
				return false
			}
		}
		e.EndArray()
	case Record:
		e.BeginObject()
		for i := range value.Record {
			e.Str(value.Record[i].Key)
			if !encodeCatValue(e, value.Record[i].Value, depth+1) {
				return false
			}
		}
		e.EndObject()
	default:
		return false
	}
	return true
}
