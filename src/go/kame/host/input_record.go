package host

import (
	"kame/core"
	json "kame/internal/jsonstream"
	"kame/internal/mem"
	"strings"
)

const RecordLimit = 65536

// A string is one raw record including its delimiter, nil is EOF. Errors are
// never EOF. The evaluator validates framing/UTF-8 independently of the host.
type RecordResult struct {
	Value   core.Value
	Code    string
	Waiting bool // Nonblocking progress retained by the host until the next poll.
}

func RecordPayload(a mem.Allocator, id int64, path string, delimiter int, stdin bool) core.Value {
	b := strings.Builder{}
	e := json.NewEncoder(&b)
	e.BeginObject()
	e.Str("reader")
	e.Int(id)
	e.Str("path")
	e.Str(path)
	e.Str("delimiter")
	e.Int(int64(delimiter))
	if stdin {
		e.Str("stdin")
		e.Bool(true)
	}
	e.EndObject()
	e.Flush()
	fields := []core.RecordField{{Key: "reader", Value: core.Value{Kind: core.Int, Int: id}}, {Key: FieldPath, Value: core.NewString(a, path)}, {Key: "delimiter", Value: core.Value{Kind: core.Int, Int: int64(delimiter)}}, {Key: "stdin", Value: core.Value{Kind: core.Bool, Bool: stdin}}, {Key: FieldData, Value: core.NewString(a, b.String())}}
	r := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	b.Reset()
	return r
}
