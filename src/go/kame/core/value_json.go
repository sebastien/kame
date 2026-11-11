package core

import (
	json "kame/internal/jsonstream"
	"kame/internal/mem"

	"strings"
	"unicode/utf8"
)

// PresentationValueJSON encodes values for human-facing output. Unlike ValueJSON,
// it preserves record order, accepts UTF-8 bytes as strings, and passes ordinary
// string/key bytes to the encoder without UTF-8 validation. It is not a canonical
// serialization or a strict JSON-value validator. Failure leaves out unchanged.
func PresentationValueJSON(a mem.Allocator, value Value, out *[]byte) bool {
	return valueJSON(a, value, out, true)
}

// ValueJSON encodes JSON-native values only, with lexical record key order.
func ValueJSON(a mem.Allocator, value Value, out *[]byte) bool {
	return valueJSON(a, value, out, false)
}

// EncodeValueJSON writes a JSON-native value into an existing encoder.
func EncodeValueJSON(a mem.Allocator, e *json.Encoder, value Value) bool {
	return encodeValueJSON(a, e, value, 0, false)
}

func valueJSON(a mem.Allocator, value Value, out *[]byte, presentation bool) bool {
	var builder strings.Builder
	e := json.NewEncoder(&builder)
	if !encodeValueJSON(a, &e, value, 0, presentation) {
		builder.Reset()
		return false
	}
	e.Flush()
	text := builder.String()
	data := mem.AllocSlice[byte](a, len(text), len(text))
	copy(data, text)
	builder.Reset()
	*out = data
	return true
}

func encodeValueJSON(a mem.Allocator, e *json.Encoder, value Value, depth int, presentation bool) bool {
	var order []int
	defer mem.FreeSlice(nil, order)
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
		if !presentation && !utf8.Valid([]byte(value.Text)) {
			return false
		}
		e.Str(value.Text)
	case Bytes:
		if !presentation || !utf8.Valid(value.Bytes) {
			return false
		}
		e.Str(string(value.Bytes))
	case List:
		e.BeginArray()
		for i := range value.List {
			if !encodeValueJSON(a, e, value.List[i], depth+1, presentation) {
				return false
			}
		}
		e.EndArray()
	case Record:
		order = make([]int, len(value.Record))
		for i := range order {
			order[i] = i
			if !presentation {
				for j := i; j > 0 && value.Record[order[j]].Key < value.Record[order[j-1]].Key; j-- {
					order[j], order[j-1] = order[j-1], order[j]
				}
			}
		}
		e.BeginObject()
		for _, i := range order {
			if !presentation && !utf8.Valid([]byte(value.Record[i].Key)) {
				return false
			}
			e.Str(value.Record[i].Key)
			if !encodeValueJSON(a, e, value.Record[i].Value, depth+1, presentation) {
				return false
			}
		}
		e.EndObject()
	default:
		return false
	}
	return true
}
