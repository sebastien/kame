package core_test

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestCatValueJSONEncodesNativeValues(t *testing.T) {
	a := t.Allocator()
	cases := slices.Make[string](a, 22)
	cases[0], cases[1] = `null`, `null`
	cases[2], cases[3] = `true`, `true`
	cases[4], cases[5] = `false`, `false`
	cases[6], cases[7] = `-42`, `-42`
	cases[8], cases[9] = `1.5`, `1.5`
	cases[10], cases[11] = `"hello"`, `"hello"`
	cases[12], cases[13] = `["alpha","beta","gamma"]`, `["alpha","beta","gamma"]`
	cases[14], cases[15] = `{"name":"value","count":2}`, `{"name":"value","count":2}`
	cases[16], cases[17] = `{"status":0,"stdout":"hi","stderr":"","signal":0}`, `{"status":0,"stdout":"hi","stderr":"","signal":0}`
	cases[18], cases[19] = `[]`, `[]`
	cases[20], cases[21] = `{}`, `{}`
	for i := 0; i < len(cases); i += 2 {
		var value core.Value
		if !core.ParseJSON(a, []byte(cases[i]), &value) {
			t.Error("valid JSON rejected: " + cases[i])
			continue
		}
		var data []byte
		ok := core.CatValueJSON(a, value, &data)
		value.Free(a)
		if !ok {
			t.Error("JSON-native value rejected: " + cases[i])
			continue
		}
		if string(data) != cases[i+1] {
			t.Error("cat JSON differs for " + cases[i] + ": got " + string(data))
		}
		mem.FreeSlice(a, data)
	}
	slices.Free(a, cases)
}

func TestCatValueJSONRejectsNonNativeValues(t *testing.T) {
	a := t.Allocator()
	pattern := core.NewPattern(a, "{name:*}")
	var data []byte
	if core.CatValueJSON(a, pattern, &data) {
		t.Error("pattern value accepted as JSON")
		mem.FreeSlice(a, data)
	}
	pattern.Free(a)
	resource := core.NewResource(a, core.ResourceFile, "./input")
	if core.CatValueJSON(a, resource, &data) {
		t.Error("resource value accepted as JSON")
		mem.FreeSlice(a, data)
	}
	resource.Free(a)
	if core.CatValueJSON(a, core.Value{Kind: core.Callable}, &data) {
		t.Error("callable value accepted as JSON")
		mem.FreeSlice(a, data)
	}
	if core.CatValueJSON(a, core.Value{Kind: core.Process}, &data) {
		t.Error("process value accepted as JSON")
		mem.FreeSlice(a, data)
	}
	binary := core.NewBytes(a, []byte{0xff, 0xfe})
	if core.CatValueJSON(a, binary, &data) {
		t.Error("binary bytes accepted as JSON")
		mem.FreeSlice(a, data)
	}
	binary.Free(a)
	items := slices.Make[core.Value](a, 1)
	items[0] = core.NewPattern(a, "{name:*}")
	nested := core.NewList(a, items)
	items[0].Free(a)
	slices.Free(a, items)
	if core.CatValueJSON(a, nested, &data) {
		t.Error("nested pattern accepted as JSON")
		mem.FreeSlice(a, data)
	}
	nested.Free(a)
}
