package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/slices"
)

// recordField borrows the first matching field, or returns nil when absent.
// The caller must not free the returned value independently of the record.
func recordField(record core.Value, key string) core.Value {
	for i := range record.Record {
		if record.Record[i].Key == key {
			return record.Record[i].Value
		}
	}
	return core.Value{}
}

func validRecord(value core.Value) bool {
	if value.Kind != core.Record {
		return false
	}
	for i := range value.Record {
		for j := 0; j < i; j++ {
			if value.Record[i].Key == value.Record[j].Key {
				return false
			}
		}
	}
	return true
}

func opKindPredicate(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	matched := c.OperationName == core.KindName(v[0].Kind)+"?"
	if c.OperationName == "number?" {
		matched = v[0].Kind == core.Int || v[0].Kind == core.Float
	}
	c.FreeCallable(&v[0])
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: matched}}
}

func opEmpty(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	value := v[0]
	empty := (value.Kind == core.String && len(value.Text) == 0) ||
		(value.Kind == core.Bytes && len(value.Bytes) == 0) ||
		(value.Kind == core.List && len(value.List) == 0) ||
		(value.Kind == core.Record && len(value.Record) == 0)
	c.FreeCallable(&v[0])
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: empty}}
}

func opHasKey(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.String {
		return invalidArgument(c, v, 0, "string")
	}
	if !validRecord(v[1]) {
		return invalidArgument(c, v, 1, "record with unique keys")
	}
	found := false
	for i := range v[1].Record {
		if v[1].Record[i].Key == v[0].Text {
			found = true
			break
		}
	}
	discardArgCallables(c, v)
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: found}}
}

func opSuccess(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if !validRecord(v[0]) {
		return invalidArgument(c, v, 0, "Result record with unique keys")
	}
	status, signal := recordField(v[0], "status"), recordField(v[0], "signal")
	if status.Kind != core.Int || signal.Kind != core.Int {
		discardArgCallables(c, v)
		return c.InvalidOperation("requires integer status and signal fields")
	}
	discardArgCallables(c, v)
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: status.Int == 0 && signal.Int == 0}}
}

// Record traversal preserves stored field order and transfers nested callables.
func opItems(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if !validRecord(v[0]) {
		return invalidArgument(c, v, 0, "record with unique keys")
	}
	var values []core.Value
	for i := range v[0].Record {
		field := v[0].Record[i]
		key := core.NewString(c.Run, field.Key)
		fields := []core.RecordField{{Key: "key", Value: key}, {Key: "value", Value: field.Value}}
		values = slices.Append(c.Run, values, core.NewRecord(c.Run, fields))
		key.Free(c.Run)
	}
	result := core.NewList(c.Run, values)
	for i := range values {
		values[i].Free(c.Run)
	}
	slices.Free(c.Run, values)
	return eval.Result{Value: result}
}

// Replacing a field preserves its position; inserting a new field appends it.
func opSet(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if !validRecord(v[2]) {
		return invalidArgument(c, v, 2, "record with unique keys")
	}
	if v[0].Kind != core.String {
		return invalidArgument(c, v, 0, "string")
	}
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
	if !found {
		fields = slices.Append(c.Run, fields, core.RecordField{Key: v[0].Text, Value: v[1]})
	}
	result := core.NewRecord(c.Run, fields)
	slices.Free(c.Run, fields)
	return eval.Result{Value: result}
}

// Merge is shallow and right-biased. Keys retain their first-seen positions.
func opMerge(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	for i := range v {
		if !validRecord(v[i]) {
			return invalidArgument(c, v, i, "record with unique keys")
		}
	}
	var fields []core.RecordField
	for i := range v {
		for j := range v[i].Record {
			field := v[i].Record[j]
			found := -1
			for k := range fields {
				if fields[k].Key == field.Key {
					found = k
					break
				}
			}
			if found < 0 {
				fields = slices.Append(c.Run, fields, field)
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
