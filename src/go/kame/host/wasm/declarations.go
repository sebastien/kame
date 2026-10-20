package wasm

import (
	"kame/core"
	"kame/host"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// DeclarationPredicate is a host-free configuration query used by source
// composition. The descriptor contains name/prefix/predicate strings and
// defines/environment string arrays, plus an optional directory string supplying
// the evaluation cwd. Borrowed fields live until the query ends.
func DeclarationPredicate(a mem.Allocator, data []byte) PureResult {
	return runConfigurationCondition(a, data, true)
}

// RunCompositionCondition accepts ordinary truth. Alongside the legacy predicate
// descriptor it accepts declaration-only sources and a condition, each with
// name/text/offset and optional includes. On failure Text carries diagnostic JSON.
func RunCompositionCondition(a mem.Allocator, data []byte) PureResult {
	return runConfigurationCondition(a, data, false)
}

func runConfigurationCondition(a mem.Allocator, data []byte, requireBoolean bool) PureResult {
	var value core.Value
	if !core.ParseJSON(a, data, &value) {
		return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "invalid declaration predicate descriptor")}
	}
	defer value.Free(a)
	if value.Kind != core.Record {
		return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate descriptor must be a record")}
	}
	name, prefix, predicate, directory := "<declaration>", "", "", ""
	var defines []string
	var environment []string
	var sources []program.CompileSource
	var condition program.CompileSource
	hasSources, hasCondition, hasPrefix := false, false, false
	defer slices.Free(a, defines)
	defer slices.Free(a, environment)
	defer freeConditionSources(a, sources)
	defer freeDescriptorIncludes(a, condition.Includes)
	hasPredicate := false
	for i := range value.Record {
		field := value.Record[i]
		if field.Key == "name" || field.Key == "prefix" || field.Key == "predicate" || field.Key == "directory" {
			if field.Value.Kind != core.String {
				return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate text fields must be strings")}
			}
			if field.Key == "name" {
				name = field.Value.Text
			} else if field.Key == "prefix" {
				prefix = field.Value.Text
				hasPrefix = true
			} else if field.Key == "directory" {
				directory = field.Value.Text
			} else {
				predicate, hasPredicate = field.Value.Text, true
			}
		} else if !requireBoolean && field.Key == "sources" {
			if field.Value.Kind != core.List || hasSources {
				return invalidConditionDescriptor(a)
			}
			hasSources = true
			for j := range field.Value.List {
				if !validConditionSource(field.Value.List[j]) {
					return invalidConditionDescriptor(a)
				}
				sources = slices.Append(a, sources, conditionSource(a, field.Value.List[j]))
			}
		} else if !requireBoolean && field.Key == "condition" {
			if hasCondition || !validConditionSource(field.Value) {
				return invalidConditionDescriptor(a)
			}
			condition, hasCondition = conditionSource(a, field.Value), true
		} else if field.Key == "defines" || field.Key == "environment" {
			if field.Value.Kind != core.List {
				return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate settings must be string arrays")}
			}
			for j := range field.Value.List {
				setting := field.Value.List[j]
				if setting.Kind != core.String {
					return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate settings must contain strings")}
				}
				if field.Key == "defines" {
					defines = slices.Append(a, defines, setting.Text)
				} else {
					environment = slices.Append(a, environment, setting.Text)
				}
			}
		} else {
			return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "unknown declaration predicate descriptor field")}
		}
	}
	if hasSources || hasCondition {
		if !hasSources || !hasCondition || hasPrefix || hasPredicate {
			return invalidConditionDescriptor(a)
		}
	} else if !hasPredicate {
		return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate is required")}
	}
	var result program.PredicateResult
	if requireBoolean {
		result = program.DeclarationPredicate(a, name, prefix, predicate, defines, environment, directory)
	} else if hasCondition {
		result = program.CompositionConditionSources(a, sources, condition, defines, environment, directory)
	} else {
		result = program.CompositionCondition(a, name, prefix, predicate, defines, environment, directory)
	}
	defer result.Diagnostic.Free(a)
	if result.Diagnostic.Code != "" {
		out := PureResult{Code: pureText(a, result.Diagnostic.Code), Message: pureText(a, result.Diagnostic.Message), SpanStart: result.Diagnostic.Span.Start, SpanEnd: result.Diagnostic.Span.End}
		if !requireBoolean {
			buffer := bytes.NewBuffer(a, nil)
			program.WriteJSONDiagnostic(&buffer, result.Diagnostic)
			out.Text = pureText(a, buffer.String())
			buffer.Free()
		}
		return out
	}
	if result.Selected {
		return PureResult{Text: pureText(a, "true")}
	}
	return PureResult{Text: pureText(a, "false")}
}

func invalidConditionDescriptor(a mem.Allocator) PureResult {
	return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "invalid authored condition source descriptor")}
}

func validConditionSource(value core.Value) bool {
	if !validDescriptorSource(value) || !validDescriptorSourceSpan(value) {
		return false
	}
	hasName, hasText := false, false
	for i := range value.Record {
		f := value.Record[i]
		if f.Key == "name" {
			hasName = true
		} else if f.Key == "text" {
			hasText = true
		} else if f.Key != "offset" && f.Key != "includes" {
			return false
		}
	}
	return hasName && hasText && host.PayloadText(value, "name") != ""
}

func conditionSource(a mem.Allocator, value core.Value) program.CompileSource {
	return program.CompileSource{Name: host.PayloadText(value, "name"), Text: host.PayloadText(value, "text"), Offset: int(host.PayloadInt(value, "offset")), Includes: descriptorIncludes(a, value)}
}

func freeConditionSources(a mem.Allocator, sources []program.CompileSource) {
	for i := range sources {
		freeDescriptorIncludes(a, sources[i].Includes)
	}
	slices.Free(a, sources)
}
