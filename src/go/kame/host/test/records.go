package host_test

import (
	"kame/host"
	"solod.dev/so/testing"
)

func TestRecordPayloadPreservesPortableWireFields(t *testing.T) {
	payload := host.RecordPayload(t.Allocator(), 3, "./input", 10, false)
	defer payload.Free(t.Allocator())
	if host.PayloadText(payload, "data") != `{"reader":3,"path":"./input","delimiter":10}` {
		t.Error("record payload missing JSON wire data: " + host.PayloadText(payload, "data"))
	}
}

func TestStdinRecordPayloadMarksInputWithoutAFileGrant(t *testing.T) {
	payload := host.RecordPayload(t.Allocator(), 4, "", 0, true)
	defer payload.Free(t.Allocator())
	if !host.PayloadBool(payload, "stdin") || host.PayloadPath(payload) != "" || host.PayloadText(payload, "data") != `{"reader":4,"path":"","delimiter":0,"stdin":true}` {
		t.Error("stdin reader payload lost its explicit input mode")
	}
}
