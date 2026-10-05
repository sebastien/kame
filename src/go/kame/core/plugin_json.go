package core

import (
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type pluginFieldResult struct { Value Value; Found bool }

// PluginValueJSON returns an owned, tagged JSON encoding for values that may
// cross a plugin boundary. Callable and process values have no wire form.
func PluginValueJSON(a mem.Allocator, value Value) []byte {
	var builder strings.Builder
	e := json.NewEncoder(&builder)
	if !encodePluginValue(a, &e, value) { builder.Free(); return nil }
	e.Flush()
	text := builder.String()
	data := mem.AllocSlice[byte](a, len(text), len(text))
	copy(data, text)
	builder.Free()
	return data
}

func encodePluginValue(a mem.Allocator, e *json.Encoder, value Value) bool {
	e.BeginObject()
	e.Str("kind")
	switch value.Kind {
	case Nil:
		e.Str("nil")
	case Bool:
		e.Str("bool"); e.Str("data"); e.Bool(value.Bool)
	case Int:
		e.Str("int"); e.Str("data"); e.Int(value.Int)
	case Float:
		e.Str("float"); e.Str("data"); e.Float(value.Float)
	case String:
		e.Str("string"); e.Str("data"); e.Str(value.Text)
	case Pattern:
		e.Str("pattern"); e.Str("data"); e.Str(value.Text)
	case Bytes:
		e.Str("bytes"); e.Str("data"); encoded := pluginBase64(a, value.Bytes); e.Str(encoded); mem.FreeString(a, encoded)
	case Resource:
		e.Str("resource"); e.Str("resourceKind"); e.Str(pluginResourceKind(value.Resource.Kind)); e.Str("name"); e.Str(value.Resource.Name)
	case List:
		e.Str("list"); e.Str("items"); e.BeginArray()
		for i := range value.List { if !encodePluginValue(a, e, value.List[i]) { return false } }
		e.EndArray()
	case Record:
		e.Str("record"); e.Str("fields"); e.BeginArray()
		for i := range value.Record {
			e.BeginArray(); e.Str(value.Record[i].Key)
			if !encodePluginValue(a, e, value.Record[i].Value) { return false }
			e.EndArray()
		}
		e.EndArray()
	default:
		return false
	}
	e.EndObject()
	return true
}

// ParsePluginValueJSON decodes one tagged value into allocator-owned storage.
// It rejects callables, process values, duplicate tag fields, and unknown data.
func ParsePluginValueJSON(a mem.Allocator, data []byte, out *Value) bool {
	var raw Value
	if !ParseJSON(a, data, &raw) { return false }
	valid := decodePluginValue(a, raw, out, 0)
	raw.Free(a)
	return valid
}

func decodePluginValue(a mem.Allocator, raw Value, out *Value, depth int) bool {
	if depth >= 256 || raw.Kind != Record { return false }
	kindField := pluginField(raw, "kind")
	if !kindField.Found || kindField.Value.Kind != String { return false }
	kind := kindField.Value.Text
	if kind == "nil" {
		if len(raw.Record) != 1 { return false }
		*out = Value{Kind: Nil}; return true
	}
	if kind == "bool" || kind == "int" || kind == "float" || kind == "string" || kind == "pattern" || kind == "bytes" {
		if len(raw.Record) != 2 { return false }
		dataField := pluginField(raw, "data")
		if !dataField.Found { return false }
		value := dataField.Value
		if kind == "bool" && value.Kind == Bool { *out = Value{Kind: Bool, Bool: value.Bool}; return true }
		if kind == "int" && value.Kind == Int { *out = Value{Kind: Int, Int: value.Int}; return true }
		if kind == "float" && value.Kind == Float { *out = Value{Kind: Float, Float: value.Float}; return true }
		if kind == "string" && value.Kind == String { *out = NewString(a, value.Text); return true }
		if kind == "pattern" && value.Kind == String { *out = NewString(a, value.Text); out.Kind = Pattern; return true }
		if kind == "bytes" && value.Kind == String { var decoded []byte; if !parsePluginBase64(a, value.Text, &decoded) { return false }; *out = Value{Kind: Bytes, Bytes: decoded}; return true }
		return false
	}
	if kind == "resource" {
		if len(raw.Record) != 3 { return false }
		resourceKind := pluginField(raw, "resourceKind")
		name := pluginField(raw, "name")
		if !resourceKind.Found || !name.Found || resourceKind.Value.Kind != String || name.Value.Kind != String { return false }
		parsedKind := parsePluginResourceKind(resourceKind.Value.Text)
		if parsedKind < 0 { return false }
		*out = Value{Kind: Resource, Resource: NewResourceKey(a, parsedKind, name.Value.Text)}
		return true
	}
	if kind == "list" {
		if len(raw.Record) != 2 { return false }
		items := pluginField(raw, "items")
		if !items.Found || items.Value.Kind != List { return false }
		values := slices.Make[Value](a, len(items.Value.List))
		for i := range items.Value.List {
			if !decodePluginValue(a, items.Value.List[i], &values[i], depth+1) { freePluginValues(a, values); return false }
		}
		*out = NewList(a, values)
		freePluginValues(a, values)
		return true
	}
	if kind == "record" {
		if len(raw.Record) != 2 { return false }
		fieldsValue := pluginField(raw, "fields")
		if !fieldsValue.Found || fieldsValue.Value.Kind != List { return false }
		fields := slices.Make[RecordField](a, len(fieldsValue.Value.List))
		for i := range fieldsValue.Value.List {
			pair := fieldsValue.Value.List[i]
			if pair.Kind != List || len(pair.List) != 2 || pair.List[0].Kind != String { freePluginFields(a, fields); return false }
			fields[i].Key = NewString(a, pair.List[0].Text).Text
			if !decodePluginValue(a, pair.List[1], &fields[i].Value, depth+1) { freePluginFields(a, fields); return false }
		}
		*out = NewRecord(a, fields)
		freePluginFields(a, fields)
		return true
	}
	return false
}

func pluginField(value Value, name string) pluginFieldResult {
	result := pluginFieldResult{}
	for i := range value.Record {
		if value.Record[i].Key != name { continue }
		if result.Found { return pluginFieldResult{} }
		result.Value, result.Found = value.Record[i].Value, true
	}
	return result
}

func freePluginValues(a mem.Allocator, values []Value) {
	for i := range values { values[i].Free(a) }
	slices.Free(a, values)
}

func freePluginFields(a mem.Allocator, fields []RecordField) {
	for i := range fields { mem.FreeString(a, fields[i].Key); fields[i].Value.Free(a) }
	slices.Free(a, fields)
}

func pluginBase64(a mem.Allocator, data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := strings.NewBuilder(a)
	for i := 0; i < len(data); i += 3 {
		v := int(data[i]) << 16
		if i+1 < len(data) { v |= int(data[i+1]) << 8 }
		if i+2 < len(data) { v |= int(data[i+2]) }
		b.WriteByte(alphabet[(v>>18)&63]); b.WriteByte(alphabet[(v>>12)&63])
		if i+1 < len(data) { b.WriteByte(alphabet[(v>>6)&63]) } else { b.WriteByte('=') }
		if i+2 < len(data) { b.WriteByte(alphabet[v&63]) } else { b.WriteByte('=') }
	}
	text := strings.Clone(a, b.String()); b.Free(); return text
}

func parsePluginBase64(a mem.Allocator, text string, out *[]byte) bool {
	if len(text)%4 != 0 { return false }
	capacity := len(text)/4*3
	data := mem.AllocSlice[byte](a, capacity, capacity)
	length := 0
	for i := 0; i < len(text); i += 4 {
		v, pad, valid := 0, 0, true
		for j := 0; j < 4; j++ {
			ch := text[i+j]
			if ch == '=' { pad++; if j < 2 || i+4 != len(text) { valid = false }; v <<= 6; continue }
			if pad != 0 { valid = false }
			index := pluginBase64Index(ch)
			if index < 0 { valid = false } else { v = (v << 6) | index }
		}
		if !valid || pad > 2 { slices.Free(a, data); return false }
		data[length] = byte(v >> 16); length++
		if pad < 2 { data[length] = byte(v >> 8); length++ }
		if pad == 0 { data[length] = byte(v); length++ }
	}
	*out = data[:length]
	return true
}

func pluginBase64Index(ch byte) int {
	if ch >= 'A' && ch <= 'Z' { return int(ch-'A') }
	if ch >= 'a' && ch <= 'z' { return int(ch-'a')+26 }
	if ch >= '0' && ch <= '9' { return int(ch-'0')+52 }
	if ch == '+' { return 62 }
	if ch == '/' { return 63 }
	return -1
}

func pluginResourceKind(kind ResourceKind) string {
	switch kind {
	case ResourceDefinition: return "definition"
	case ResourceTarget: return "target"
	case ResourceFile: return "file"
	case ResourceTask: return "task"
	case ResourceService: return "service"
	case ResourceGlob: return "glob"
	case ResourceEnvironment: return "environment"
	case ResourceTool: return "tool"
	}
	return "unknown"
}

func parsePluginResourceKind(text string) ResourceKind {
	switch text {
	case "definition": return ResourceDefinition
	case "target": return ResourceTarget
	case "file": return ResourceFile
	case "task": return ResourceTask
	case "service": return ResourceService
	case "glob": return ResourceGlob
	case "environment": return ResourceEnvironment
	case "tool": return ResourceTool
	}
	return -1
}
