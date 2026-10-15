package core_test

import (
	"kame/core"
	"solod.dev/so/math"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestPluginValueJSONRoundTripsSupportedKinds(t *testing.T) {
	a := t.Allocator()
	values := slices.Make[core.Value](a, 14)
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
	items[0].Free(a)
	items[1].Free(a)
	slices.Free(a, items)
	fields := slices.Make[core.RecordField](a, 1)
	fields[0] = core.RecordField{Key: core.NewString(a, "field").Text, Value: core.NewBytes(a, []byte{255, 0})}
	values[9] = core.NewRecord(a, fields)
	mem.FreeString(a, fields[0].Key)
	fields[0].Value.Free(a)
	slices.Free(a, fields)
	values[10] = core.Value{Kind: core.Resource, Resource: core.NewResourceKey(a, core.ResourceTool, "compiler")}
	values[11] = core.Value{Kind: core.Float, Float: 0.0}
	values[12] = core.Value{Kind: core.Float, Float: 1.0}
	values[13] = core.Value{Kind: core.Float, Float: -42.0}
	for i := range values {
		data := core.PluginValueJSON(a, values[i])
		if data == nil {
			t.Error("supported value was not encoded")
			continue
		}
		var decoded core.Value
		if !core.ParsePluginValueJSON(a, data, &decoded) {
			t.Error("encoded value did not parse")
			mem.FreeSlice(a, data)
			continue
		}
		encodedAgain := core.PluginValueJSON(a, decoded)
		if encodedAgain == nil || string(data) != string(encodedAgain) {
			t.Error("plugin value changed kind or content during round trip")
		}
		mem.FreeSlice(a, encodedAgain)
		decoded.Free(a)
		mem.FreeSlice(a, data)
		values[i].Free(a)
	}
	slices.Free(a, values)
}

func TestPluginBytesBase64Padding(t *testing.T) {
	a := t.Allocator()
	data := []byte{255, 255, 255, 255}
	expected := []string{
		`{"kind":"bytes","data":""}`,
		`{"kind":"bytes","data":"/w=="}`,
		`{"kind":"bytes","data":"//8="}`,
		`{"kind":"bytes","data":"////"}`,
		`{"kind":"bytes","data":"/////w=="}`,
	}
	for i := range expected {
		encoded := core.PluginValueJSON(a, core.Value{Kind: core.Bytes, Bytes: data[:i]})
		if string(encoded) != expected[i] {
			t.Error("plugin bytes lost canonical Base64 padding")
		}
		mem.FreeSlice(a, encoded)
	}
}

func TestPluginValueJSONRejectsEncoderFailures(t *testing.T) {
	a := t.Allocator()
	numbers := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
	for i := range numbers {
		data := core.PluginValueJSON(a, core.Value{Kind: core.Float, Float: numbers[i]})
		if data != nil {
			t.Error("nonfinite float returned partial plugin JSON")
			mem.FreeSlice(a, data)
		}
	}
	invalidBytes := []byte{255}
	invalidText := core.NewString(a, string(invalidBytes))
	data := core.PluginValueJSON(a, invalidText)
	invalidText.Free(a)
	if data != nil {
		t.Error("invalid UTF-8 returned partial plugin JSON")
		mem.FreeSlice(a, data)
	}
	// Borrowed links into one fixture allocation, not individually owned lists.
	values := slices.Make[core.Value](a, 300)
	for i := 0; i < len(values)-1; i++ {
		values[i] = core.Value{Kind: core.List, List: values[i+1 : i+2]}
	}
	data = core.PluginValueJSON(a, values[0])
	slices.Free(a, values)
	if data != nil {
		t.Error("excessive nesting returned partial plugin JSON")
		mem.FreeSlice(a, data)
	}
}

func TestPluginValueJSONRejectsUnsupportedAndMalformedValues(t *testing.T) {
	a := t.Allocator()
	if data := core.PluginValueJSON(a, core.Value{Kind: core.Callable}); data != nil {
		t.Error("callable was serialized")
		mem.FreeSlice(a, data)
	}
	invalid := slices.Make[string](a, 5)
	invalid[0] = `{"kind":"string","data":"x","extra":true}`
	invalid[1] = `{"kind":"bool","kind":"bool","data":true}`
	invalid[2] = `{"kind":"bytes","data":"%%%="}`
	invalid[3] = `{"kind":"resource","resourceKind":"unknown","name":"x"}`
	invalid[4] = `{"kind":"list","items":[{"kind":"callable"}]}`
	for i := range invalid {
		var decoded core.Value
		if core.ParsePluginValueJSON(a, []byte(invalid[i]), &decoded) {
			t.Error("malformed plugin value was accepted")
			decoded.Free(a)
		}
	}
	slices.Free(a, invalid)
}
