package wasm

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
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

// Common authored-source fields are optional here. Each descriptor boundary
// separately decides which fields are required and whether unknowns are allowed.
func validDescriptorSource(value core.Value) bool {
	if value.Kind != core.Record {
		return false
	}
	for i := range value.Record {
		field := value.Record[i]
		switch field.Key {
		case "name", "text":
			if field.Value.Kind != core.String {
				return false
			}
		case "offset":
			if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 2147483647 {
				return false
			}
		case "includes":
			if !validDescriptorIncludes(field.Value) {
				return false
			}
		}
	}
	return true
}

func validDescriptorSourceSpan(value core.Value) bool {
	return int64(len(host.PayloadText(value, "text"))) <= 2147483647-host.PayloadInt(value, "offset")
}

func validSessionFragment(value core.Value) bool {
	if !validDescriptorSource(value) || !validDescriptorSourceSpan(value) {
		return false
	}
	for i := range value.Record {
		field := value.Record[i]
		switch field.Key {
		case "lang", "comment":
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
		}
	}
	return true
}

func validDescriptorIncludes(value core.Value) bool {
	if value.Kind != core.List {
		return false
	}
	for i := range value.List {
		frame := value.List[i]
		if frame.Kind != core.Record {
			return false
		}
		fields := 0
		for j := range frame.Record {
			field := frame.Record[j]
			if field.Key == "source" || field.Key == "label" {
				if field.Value.Kind != core.String || field.Value.Text == "" {
					return false
				}
				if field.Key == "source" {
					fields |= 1
				} else {
					fields |= 2
				}
			}
			if field.Key == "start" || field.Key == "end" {
				if field.Value.Kind != core.Int || field.Value.Int < 0 || field.Value.Int > 2147483647 {
					return false
				}
				if field.Key == "start" {
					fields |= 4
				} else {
					fields |= 8
				}
			}
		}
		if fields != 15 || host.PayloadInt(frame, "end") < host.PayloadInt(frame, "start") {
			return false
		}
	}
	return true
}

func descriptorIncludes(a mem.Allocator, value core.Value) []diagnostic.Frame {
	var frames []diagnostic.Frame
	items := host.PayloadList(value, "includes")
	for i := range items {
		frames = slices.Append(a, frames, diagnostic.Frame{Kind: "include", Label: pureText(a, host.PayloadText(items[i], "label")), Source: pureText(a, host.PayloadText(items[i], "source")), Span: diagnostic.Span{Start: int(host.PayloadInt(items[i], "start")), End: int(host.PayloadInt(items[i], "end"))}})
	}
	return frames
}

func freeDescriptorIncludes(a mem.Allocator, frames []diagnostic.Frame) {
	for i := range frames {
		mem.FreeString(a, frames[i].Label)
		mem.FreeString(a, frames[i].Source)
	}
	slices.Free(a, frames)
}
