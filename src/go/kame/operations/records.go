package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/slices"
)

func validRecord(value core.Value) bool {
	if value.Kind != core.Record { return false }
	for i := range value.Record {
		for j := 0; j < i; j++ { if value.Record[i].Key == value.Record[j].Key { return false } }
	}
	return true
}

func opKindPredicate(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	kind := core.Record
	if c.OperationName == "list?" { kind = core.List }
	if c.OperationName == "string?" { kind = core.String }
	matched := v[0].Kind == kind
	c.FreeCallable(&v[0])
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: matched}}
}

// Record traversal preserves stored field order and transfers nested callables.
func opItems(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if !validRecord(v[0]) { return invalidArgument(c, v, 0, "record with unique keys") }
	var values []core.Value
	for i := range v[0].Record {
		field := v[0].Record[i]
		key := core.NewString(c.Run, field.Key)
		fields := []core.RecordField{{Key: "key", Value: key}, {Key: "value", Value: field.Value}}
		values = slices.Append(c.Run, values, core.NewRecord(c.Run, fields))
		key.Free(c.Run)
	}
	result := core.NewList(c.Run, values)
	for i := range values { values[i].Free(c.Run) }
	slices.Free(c.Run, values)
	return eval.Result{Value: result}
}

// Replacing a field preserves its position; inserting a new field appends it.
func opSet(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if !validRecord(v[2]) { return invalidArgument(c, v, 2, "record with unique keys") }
	if v[0].Kind != core.String { return invalidArgument(c, v, 0, "string") }
	var fields []core.RecordField
	found := false
	for i := range v[2].Record {
		field := v[2].Record[i]
		if field.Key == v[0].Text {
			c.FreeCallable(&v[2].Record[i].Value)
			field.Value, found = v[1], true
		}
		fields = slices.Append(c.Run, fields, field)
	}
	if !found { fields = slices.Append(c.Run, fields, core.RecordField{Key: v[0].Text, Value: v[1]}) }
	result := core.NewRecord(c.Run, fields)
	slices.Free(c.Run, fields)
	return eval.Result{Value: result}
}

// Merge is shallow and right-biased. Keys retain their first-seen positions.
func opMerge(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	for i := range v { if !validRecord(v[i]) { return invalidArgument(c, v, i, "record with unique keys") } }
	var fields []core.RecordField
	for i := range v {
		for j := range v[i].Record {
			field := v[i].Record[j]
			found := -1
			for k := range fields { if fields[k].Key == field.Key { found = k; break } }
			if found < 0 { fields = slices.Append(c.Run, fields, field)
			} else {
				c.FreeCallable(&fields[found].Value)
				fields[found].Value = field.Value
			}
		}
	}
	result := core.NewRecord(c.Run, fields)
	slices.Free(c.Run, fields)
	return eval.Result{Value: result}
}
