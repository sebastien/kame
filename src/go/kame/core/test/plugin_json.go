package core_test

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestPluginValueJSONRoundTripsSupportedKinds(t *testing.T) {
	a := t.Allocator()
	values := slices.Make[core.Value](a, 11)
	values[0] = core.Value{Kind: core.Nil}
	values[1] = core.Value{Kind: core.Bool, Bool: true}
	values[2] = core.Value{Kind: core.Int, Int: -42}
	values[3] = core.Value{Kind: core.Float, Float: 1.5}
	values[4] = core.NewString(a, "text")
	values[5] = core.NewBytes(a, []byte{0, 1, 255})
	values[6] = core.NewString(a, "{name:*}")
	values[6].Kind = core.Pattern
	values[7] = core.Value{Kind: core.Resource, Resource: core.NewResourceKey(a, core.ResourceFile, "file:///work/input")}
	items := slices.Make[core.Value](a, 2)
	items[0], items[1] = core.NewString(a, "nested"), core.Value{Kind: core.Bool, Bool: false}
	values[8] = core.NewList(a, items)
	items[0].Free(a); items[1].Free(a); slices.Free(a, items)
	fields := slices.Make[core.RecordField](a, 1)
	fields[0] = core.RecordField{Key: core.NewString(a, "field").Text, Value: core.NewBytes(a, []byte{255, 0})}
	values[9] = core.NewRecord(a, fields)
	mem.FreeString(a, fields[0].Key); fields[0].Value.Free(a); slices.Free(a, fields)
	values[10] = core.Value{Kind: core.Resource, Resource: core.NewResourceKey(a, core.ResourceTool, "compiler")}
	for i := range values {
		data := core.PluginValueJSON(a, values[i])
		if data == nil { t.Error("supported value was not encoded"); continue }
		var decoded core.Value
		if !core.ParsePluginValueJSON(a, data, &decoded) { t.Error("encoded value did not parse"); mem.FreeSlice(a, data); continue }
		encodedAgain := core.PluginValueJSON(a, decoded)
		if encodedAgain == nil || string(data) != string(encodedAgain) { t.Error("plugin value changed kind or content during round trip") }
		mem.FreeSlice(a, encodedAgain)
		decoded.Free(a)
		mem.FreeSlice(a, data)
		values[i].Free(a)
	}
	slices.Free(a, values)
}

func TestPluginValueJSONRejectsUnsupportedAndMalformedValues(t *testing.T) {
	a := t.Allocator()
	if data := core.PluginValueJSON(a, core.Value{Kind: core.Callable}); data != nil { t.Error("callable was serialized"); mem.FreeSlice(a, data) }
	invalid := slices.Make[string](a, 5)
	invalid[0] = `{"kind":"string","data":"x","extra":true}`
	invalid[1] = `{"kind":"bool","kind":"bool","data":true}`
	invalid[2] = `{"kind":"bytes","data":"%%%="}`
	invalid[3] = `{"kind":"resource","resourceKind":"unknown","name":"x"}`
	invalid[4] = `{"kind":"list","items":[{"kind":"callable"}]}`
	for i := range invalid {
		var decoded core.Value
		if core.ParsePluginValueJSON(a, []byte(invalid[i]), &decoded) { t.Error("malformed plugin value was accepted"); decoded.Free(a) }
	}
	slices.Free(a, invalid)
}
