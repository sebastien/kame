package wasmcheck

import (
	"kame/core"
	"kame/host/wasm"
	"kame/internal/testing"
)

func TestBinaryCaptureCompletionOwnsBytesAndRejectsAmbiguousMetadata(t *testing.T) {
	a := t.Allocator()
	fields := []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: 1}}}
	value := core.NewRecord(a, fields)
	var data = [3]byte{0, 255, 10}
	if !wasm.AppendCapturedBytes(a, &value, data[:], nil) {
		t.Fatal("attach binary streams")
		return
	}
	data[1] = 0
	stdout, stderr := core.Value{}, core.Value{}
	for i := range value.Record {
		if value.Record[i].Key == "stdout" {
			stdout = value.Record[i].Value
		}
		if value.Record[i].Key == "stderr" {
			stderr = value.Record[i].Value
		}
	}
	if stdout.Kind != core.Bytes || len(stdout.Bytes) != 3 || stdout.Bytes[1] != 255 || stderr.Kind != core.Bytes || len(stderr.Bytes) != 0 {
		t.Error("capture lost exact bytes, ownership or empty-stream identity")
	}
	if wasm.AppendCapturedBytes(a, &value, nil, nil) {
		t.Error("duplicate payload fields accepted")
	}
	value.Free(a)
	value = core.Value{}
	if wasm.AppendCapturedBytes(a, &value, nil, nil) {
		t.Error("nonrecord metadata accepted")
	}
}
