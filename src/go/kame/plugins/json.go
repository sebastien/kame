package plugins

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type jsonField struct { Value core.Value; Found bool; Duplicate bool }

// RegisterJSON parses the explicit, bounded-schema declaration form used by
// embedders, then applies the same all-or-nothing validation as Register.
func RegisterJSON(a mem.Allocator, registry *eval.Registry, data []byte) bool {
	var raw core.Value
	if !core.ParseJSON(a, data, &raw) { return false }
	defer raw.Free(a)
	if raw.Kind != core.List { return false }
	declarations := slices.Make[Plugin](a, len(raw.List))
	defer freePluginDeclarations(a, declarations)
	for i := range raw.List {
		value := raw.List[i]
		if value.Kind != core.Record { return false }
		name := jsonFieldValue(value, "name")
		version := jsonFieldValue(value, "version")
		if name.Duplicate || version.Duplicate || !name.Found || !version.Found || name.Value.Kind != core.String || version.Value.Kind != core.String { return false }
		declarations[i].Name, declarations[i].Version = name.Value.Text, version.Value.Text
		requestLimit := jsonFieldValue(value, "maxRequestBytes")
		responseLimit := jsonFieldValue(value, "maxResponseBytes")
		timeout := jsonFieldValue(value, "timeoutMS")
		if !readOptionalLimit(requestLimit, &declarations[i].MaxRequestBytes) || !readOptionalLimit(responseLimit, &declarations[i].MaxResponseBytes) || !readOptionalTimeout(timeout, &declarations[i].TimeoutMS) { return false }
		operations := jsonFieldValue(value, "operations")
		if operations.Duplicate || !operations.Found || operations.Value.Kind != core.List { return false }
		declarations[i].Operations = slices.Make[Operation](a, len(operations.Value.List))
		for j := range operations.Value.List {
			op := operations.Value.List[j]
			if op.Kind != core.Record { return false }
			operationName := jsonFieldValue(op, "name")
			operationVersion := jsonFieldValue(op, "version")
			minArity := jsonFieldValue(op, "minArity")
			maxArity := jsonFieldValue(op, "maxArity")
			if operationName.Duplicate || operationVersion.Duplicate || minArity.Duplicate || maxArity.Duplicate || !operationName.Found || !operationVersion.Found || !minArity.Found || !maxArity.Found || operationName.Value.Kind != core.String || operationVersion.Value.Kind != core.String || minArity.Value.Kind != core.Int || maxArity.Value.Kind != core.Int { return false }
			declaration := &declarations[i].Operations[j]
			declaration.Name, declaration.Version = operationName.Value.Text, operationVersion.Value.Text
			declaration.MinArity, declaration.MaxArity = int(minArity.Value.Int), int(maxArity.Value.Int)
			requestLimit = jsonFieldValue(op, "maxRequestBytes")
			responseLimit = jsonFieldValue(op, "maxResponseBytes")
			timeout = jsonFieldValue(op, "timeoutMS")
			if !readOptionalLimit(requestLimit, &declaration.MaxRequestBytes) || !readOptionalLimit(responseLimit, &declaration.MaxResponseBytes) || !readOptionalTimeout(timeout, &declaration.TimeoutMS) { return false }
			capabilities := jsonFieldValue(op, "capabilities")
			if capabilities.Duplicate { return false }
			if capabilities.Found {
				if capabilities.Value.Kind != core.List { return false }
				for k := range capabilities.Value.List {
					capability := capabilities.Value.List[k]
					if capability.Kind != core.String { return false }
					switch capability.Text {
					case "read": declaration.Capabilities = slices.Append(a, declaration.Capabilities, eval.Read)
					case "write": declaration.Capabilities = slices.Append(a, declaration.Capabilities, eval.Write)
					case "run": declaration.Capabilities = slices.Append(a, declaration.Capabilities, eval.Run)
					case "env": declaration.Capabilities = slices.Append(a, declaration.Capabilities, eval.Env)
					default: return false
					}
				}
			}
		}
	}
	registered := Register(a, registry, declarations)
	return registered
}

func freePluginDeclarations(a mem.Allocator, declarations []Plugin) {
	for i := range declarations { for j := range declarations[i].Operations { slices.Free(a, declarations[i].Operations[j].Capabilities) }; slices.Free(a, declarations[i].Operations) }
	slices.Free(a, declarations)
}

func jsonFieldValue(value core.Value, key string) jsonField {
	result := jsonField{}
	for i := range value.Record {
		if value.Record[i].Key != key { continue }
		if result.Found { result.Duplicate = true; return result }
		result.Value, result.Found = value.Record[i].Value, true
	}
	return result
}

func readOptionalLimit(field jsonField, target *int) bool {
	if !field.Found { return true }
	if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > int64(DefaultMaxRequestBytes) { return false }
	*target = int(field.Value.Int)
	return true
}

func readOptionalTimeout(field jsonField, target *int64) bool {
	if !field.Found { return true }
	if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > DefaultTimeoutMS { return false }
	*target = field.Value.Int
	return true
}
