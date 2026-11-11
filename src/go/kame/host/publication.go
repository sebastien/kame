package host

import (
	"kame/core"
	json "kame/internal/jsonstream"
	"kame/internal/mem"
	"strings"
)

func (r *PublishResult) Value(a mem.Allocator) core.Value {
	return core.NewRecord(a, []core.RecordField{
		{Key: "code", Value: core.Value{Kind: core.String, Text: r.Code}},
		{Key: "published", Value: core.Value{Kind: core.Bool, Bool: r.Published}},
		{Key: "stagedRemoved", Value: core.Value{Kind: core.Bool, Bool: r.StagedRemoved}},
	})
}

func PublishPayload(a mem.Allocator, staged string, destination string) core.Value {
	b := strings.Builder{}
	e := json.NewEncoder(&b)
	e.BeginObject()
	e.Str("staged")
	e.Str(staged)
	e.Str("destination")
	e.Str(destination)
	e.EndObject()
	e.Flush()
	value := core.NewRecord(a, []core.RecordField{
		{Key: "staged", Value: core.Value{Kind: core.String, Text: staged}},
		{Key: "destination", Value: core.Value{Kind: core.String, Text: destination}},
		{Key: FieldData, Value: core.Value{Kind: core.String, Text: b.String()}},
	})
	b.Reset()
	return value
}
