package host

import (
	"encoding/base64"
	"kame/core"
	json "kame/internal/jsonstream"
	"kame/internal/mem"
	"strings"
)

const StageChunkLimit = 32 * 1024

const (
	StageRead   = "read"
	StageWatch  = "watch"
	StageStdout = "stdout"
	StageStderr = "stderr"
	StageFinish = "finish"
)

// StageIO carries one bounded exchange with an evaluator-owned process stage.
// Only one data exchange and one output watch may be outstanding per stage.
type StageIO struct {
	Graph     int64
	Index     int
	Operation string
	Bytes     []byte
	Status    int
	Signal    int
	Cancelled bool
	Frame     string // optional :lines or :nul, for reads ending at a delimiter
}

// StageIOCompletion identifies the waiting evaluator node. Closed is input
// EOF on a read, consumer closure on a watch, and a stopped endpoint on a
// write or finish.
type StageIOCompletion struct {
	NodeID     int64
	Generation int64
	Attempt    int64
	Operation  string
	Closed     bool
}

type ProcessStageIOHost interface {
	StartStageIO(request Request, exchange StageIO) bool
}

func (s StageIO) Valid() bool {
	if s.Graph <= 0 || s.Index < 0 {
		return false
	}
	if s.Frame != "" && (s.Operation != StageRead || (s.Frame != ":lines" && s.Frame != ":nul")) {
		return false
	}
	switch s.Operation {
	case StageRead, StageWatch:
		return len(s.Bytes) == 0 && s.Status == 0 && s.Signal == 0 && !s.Cancelled
	case StageStdout, StageStderr:
		return len(s.Bytes) > 0 && len(s.Bytes) <= StageChunkLimit && s.Status == 0 && s.Signal == 0 && !s.Cancelled
	case StageFinish:
		return len(s.Bytes) == 0 && s.Status >= 0 && s.Status <= 255 &&
			s.Signal >= 0 && s.Signal < 128 && (s.Signal == 0 || s.Status == 128+s.Signal) &&
			(!s.Cancelled || s.Status == 0)
	default:
		return false
	}
}

func StageIOPayload(a mem.Allocator, s StageIO) core.Value {
	var wire strings.Builder
	e := json.NewEncoder(&wire)
	e.BeginObject()
	e.Str("process")
	e.Int(s.Graph)
	e.Str("stage")
	e.Int(int64(s.Index))
	e.Str(FieldOp)
	e.Str(s.Operation)
	if s.Frame != "" {
		e.Str("frame")
		e.Str(s.Frame)
	}
	e.Str("bytes")
	e.Str(base64.StdEncoding.EncodeToString(s.Bytes))
	e.Str("status")
	e.Int(int64(s.Status))
	e.Str("signal")
	e.Int(int64(s.Signal))
	e.Str("cancelled")
	e.Bool(s.Cancelled)
	e.EndObject()
	e.Flush()
	fields := []core.RecordField{
		{Key: FieldData, Value: core.NewString(a, wire.String())},
		{Key: FieldOp, Value: core.NewString(a, s.Operation)},
		{Key: "process", Value: core.Value{Kind: core.Int, Int: s.Graph}},
		{Key: "stage", Value: core.Value{Kind: core.Int, Int: int64(s.Index)}},
		{Key: "bytes", Value: core.NewBytes(a, s.Bytes)},
		{Key: "status", Value: core.Value{Kind: core.Int, Int: int64(s.Status)}},
		{Key: "signal", Value: core.Value{Kind: core.Int, Int: int64(s.Signal)}},
		{Key: "cancelled", Value: core.Value{Kind: core.Bool, Bool: s.Cancelled}},
	}
	if s.Frame != "" {
		fields = append(fields, core.RecordField{Key: "frame", Value: core.NewString(a, s.Frame)})
	}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	return payload
}

func ParseStageIO(payload core.Value) (StageIO, bool) {
	required := map[string]core.Kind{
		"process": core.Int, "stage": core.Int, FieldOp: core.String, "bytes": core.Bytes,
		"status": core.Int, "signal": core.Int, "cancelled": core.Bool,
	}
	if payload.Kind != core.Record {
		return StageIO{}, false
	}
	var frame string
	framed := false
	for _, field := range payload.Record {
		if field.Key == FieldData {
			continue
		}
		if field.Key == "frame" {
			if framed || field.Value.Kind != core.String || field.Value.Text == "" {
				return StageIO{}, false
			}
			frame, framed = field.Value.Text, true
			continue
		}
		kind, present := required[field.Key]
		if !present || field.Value.Kind != kind {
			return StageIO{}, false
		}
		delete(required, field.Key)
	}
	if len(required) != 0 {
		return StageIO{}, false
	}
	index := PayloadInt(payload, "stage")
	status := PayloadInt(payload, "status")
	signal := PayloadInt(payload, "signal")
	if int64(int(index)) != index || status < 0 || status > 255 || signal < 0 || signal >= 128 {
		return StageIO{}, false
	}
	exchange := StageIO{
		Graph: PayloadInt(payload, "process"), Index: int(index), Operation: PayloadText(payload, FieldOp),
		Bytes: PayloadBytes(payload, "bytes"), Status: int(status), Signal: int(signal),
		Cancelled: PayloadBool(payload, "cancelled"),
		Frame:     frame,
	}
	return exchange, exchange.Valid()
}
