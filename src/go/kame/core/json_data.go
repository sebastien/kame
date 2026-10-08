package core

import (
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

// WriteJSONData writes lossless data and encoding fields into an open object.
// Temporary binary encoding storage belongs to the caller's allocator.
func WriteJSONData(a mem.Allocator, e *json.Encoder, data []byte) {
	e.Str("data")
	if utf8.Valid(data) {
		e.Str(string(data))
		e.Str("encoding")
		e.Str("utf-8")
		return
	}
	encoded := base64Text(a, data)
	e.Str(encoded)
	mem.FreeString(a, encoded)
	e.Str("encoding")
	e.Str("base64")
}

func base64Text(a mem.Allocator, data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := strings.NewBuilder(a)
	defer b.Free()
	for i := 0; i < len(data); i += 3 {
		value := int(data[i]) << 16
		if i+1 < len(data) {
			value |= int(data[i+1]) << 8
		}
		if i+2 < len(data) {
			value |= int(data[i+2])
		}
		b.WriteByte(alphabet[(value>>18)&63])
		b.WriteByte(alphabet[(value>>12)&63])
		if i+1 < len(data) {
			b.WriteByte(alphabet[(value>>6)&63])
		} else {
			b.WriteByte('=')
		}
		if i+2 < len(data) {
			b.WriteByte(alphabet[value&63])
		} else {
			b.WriteByte('=')
		}
	}
	return strings.Clone(a, b.String())
}
