// Package host defines portable requests issued by evaluators and runtimes.
package host

import (
	"kame/core"
	"solod.dev/so/mem"
)

// Payload record fields shared by library operations and runtime hosts.
const (
	FieldOp     = "op"
	FieldPath   = "path"
	FieldData   = "data"
	FieldScript = "script"
	OpRead      = "read"
	OpExists    = "exists"
	OpStat      = "stat"
	OpWildcard  = "wildcard"
)

// FilePayload creates a filesystem request payload. A string payload remains a
// read request for callers that predate the record protocol.
func FilePayload(a mem.Allocator, op string, name string) core.Value {
	fields := []core.RecordField{{Key: FieldOp, Value: core.NewString(a, op)}, {Key: FieldPath, Value: core.NewString(a, name)}}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	return payload
}

func ProcessPayload(a mem.Allocator, script string) core.Value {
	fields := []core.RecordField{{Key: FieldScript, Value: core.NewString(a, script)}}
	payload := core.NewRecord(a, fields)
	fields[0].Value.Free(a)
	return payload
}

func WritePayload(a mem.Allocator, name string, data []byte) core.Value {
	fields := []core.RecordField{{Key: FieldPath, Value: core.NewString(a, name)}, {Key: FieldData, Value: core.NewBytes(a, data)}}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	return payload
}

// PayloadPath returns the path or environment name carried by a request.
func PayloadPath(payload core.Value) string {
	if payload.Kind == core.String {
		return payload.Text
	}
	if payload.Kind != core.Record {
		return ""
	}
	for i := range payload.Record {
		if payload.Record[i].Key == FieldPath && payload.Record[i].Value.Kind == core.String {
			return payload.Record[i].Value.Text
		}
	}
	return ""
}

func PayloadText(payload core.Value, field string) string {
	if payload.Kind == core.String && field == FieldScript {
		return payload.Text
	}
	if payload.Kind != core.Record {
		return ""
	}
	for i := range payload.Record {
		if payload.Record[i].Key == field && payload.Record[i].Value.Kind == core.String {
			return payload.Record[i].Value.Text
		}
	}
	return ""
}

func PayloadBytes(payload core.Value, field string) []byte {
	if payload.Kind != core.Record {
		return nil
	}
	for i := range payload.Record {
		if payload.Record[i].Key == field && payload.Record[i].Value.Kind == core.Bytes {
			return payload.Record[i].Value.Bytes
		}
	}
	return nil
}
