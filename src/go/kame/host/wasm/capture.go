package wasm

import (
	"kame/core"
	"kame/internal/mem"
)

// AppendCapturedBytes keeps binary payloads out of the JSON metadata parser.
func AppendCapturedBytes(a mem.Allocator, value *core.Value, stdout []byte, stderr []byte) bool {
	if value.Kind != core.Record {
		return false
	}
	for i := range value.Record {
		if value.Record[i].Key == "stdout" || value.Record[i].Key == "stderr" {
			return false
		}
	}
	value.Record = append(value.Record, core.RecordField{Key: pureText(a, "stdout"), Value: core.NewBytes(a, stdout)})
	value.Record = append(value.Record, core.RecordField{Key: pureText(a, "stderr"), Value: core.NewBytes(a, stderr)})
	return true
}
