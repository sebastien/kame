package wasm

import (
	"kame/core"
	"kame/host"
)

// Validate known option fields before Payload helpers apply omitted defaults.
// Unknown fields remain compatible with newer embedding hosts.
func validDescriptorOptions(value core.Value, session bool) bool {
	if value.Kind != core.Record {
		return false
	}
	for i := range value.Record {
		field := value.Record[i]
		switch field.Key {
		case "sources":
			if !session && field.Value.Kind != core.List {
				return false
			}
		case "fragments", "args":
			if session && field.Value.Kind != core.List {
				return false
			}
		case "defines":
			if !session && !descriptorStrings(field.Value) {
				return false
			}
		case "buildDefines":
			if session && !descriptorStrings(field.Value) {
				return false
			}
		case "environment", "parameters", "toolOverrides":
			if !descriptorStrings(field.Value) {
				return false
			}
		case "timeoutMS":
			if field.Value.Kind != core.Int || field.Value.Int < 0 {
				return false
			}
		case "retryCount", "retainBytes":
			if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 2147483647 {
				return false
			}
		case "captureLimit":
			if session && (field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 2147483647) {
				return false
			}
		case "force":
			if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 1 {
				return false
			}
		case "dryRun", "printResult", "json":
			if session && (field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 1) {
				return false
			}
		}
	}
	return true
}

func descriptorStrings(value core.Value) bool {
	if value.Kind != core.List {
		return false
	}
	for i := range value.List {
		if value.List[i].Kind != core.String {
			return false
		}
	}
	return true
}

func validSessionFragment(value core.Value) bool {
	if value.Kind != core.Record {
		return false
	}
	for i := range value.Record {
		field := value.Record[i]
		switch field.Key {
		case "name", "text", "lang", "comment":
			if field.Value.Kind != core.String {
				return false
			}
		case "entries", "defines":
			if !descriptorStrings(field.Value) {
				return false
			}
		case "inline", "skipStatements", "check":
			if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 1 {
				return false
			}
		case "offset":
			if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 2147483647 {
				return false
			}
		}
	}
	return int64(len(host.PayloadText(value, "text"))) <= 2147483647-host.PayloadInt(value, "offset")
}
