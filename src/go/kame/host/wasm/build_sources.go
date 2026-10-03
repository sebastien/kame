package wasm

import (
	"kame/core"
	"kame/host"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// SetBuildSources copies host-loaded include fragments. Compilation remains
// portable and effect-free, and CompileMany retains authored source mapping.
func (r *Runtime) SetBuildSources(data []byte) PureResult {
	if r.Program != nil || r.Node != nil || len(r.BuildSources) != 0 {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "build sources already configured or active")}
	}
	var descriptor core.Value
	if !CompletionValueFromJSON(r.Alloc, data, &descriptor) {
		return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid build source descriptor")}
	}
	defer descriptor.Free(r.Alloc)
	inputs := host.PayloadList(descriptor, "sources")
	if len(inputs) == 0 {
		return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "empty build source descriptor")}
	}
	total := 0
	for i := range inputs {
		valid, hasName, hasText := inputs[i].Kind == core.Record, false, false
		for j := range inputs[i].Record {
			field := inputs[i].Record[j]
			if field.Key == "name" { hasName = true; if field.Value.Kind != core.String { valid = false } }
			if field.Key == "text" { hasText = true; if field.Value.Kind != core.String { valid = false } }
			if field.Key == "offset" && field.Value.Kind != core.Int { valid = false }
		}
		name := host.PayloadText(inputs[i], "name")
		offset := host.PayloadInt(inputs[i], "offset")
		if !valid || !hasName || !hasText || name == "" || offset < 0 || offset > 2147483647 {
			r.freeBuildSources()
			return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid build source fragment")}
		}
		text := host.PayloadText(inputs[i], "text")
		if int64(len(text)) > 2147483647-offset {
			r.freeBuildSources()
			return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "build source span exceeds instance range")}
		}
		if len(text) > 65536-total {
			r.freeBuildSources()
			return PureResult{Code: pureText(r.Alloc, "NO_MEMORY"), Message: pureText(r.Alloc, "build sources exceed instance capacity")}
		}
		total += len(text)
		r.BuildSources = slices.Append(r.Alloc, r.BuildSources, program.CompileSource{Name: pureText(r.Alloc, name), Text: pureText(r.Alloc, text), Offset: int(offset)})
	}
	tools := host.PayloadList(descriptor, "toolOverrides")
	for i := range tools {
		if tools[i].Kind != core.String { r.freeBuildSources(); return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid tool override")} }
		r.BuildToolOverrides = slices.Append(r.Alloc, r.BuildToolOverrides, pureText(r.Alloc, tools[i].Text))
	}
	defines := host.PayloadList(descriptor, "defines")
	for i := range defines {
		if defines[i].Kind != core.String { r.freeBuildSources(); return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid definition override")} }
		r.BuildDefines = slices.Append(r.Alloc, r.BuildDefines, pureText(r.Alloc, defines[i].Text))
	}
	environment := host.PayloadList(descriptor, "environment")
	for i := range environment {
		if environment[i].Kind != core.String { r.freeBuildSources(); return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid build environment")} }
		r.BuildEnvironment = slices.Append(r.Alloc, r.BuildEnvironment, pureText(r.Alloc, environment[i].Text))
	}
	r.BuildForce = host.PayloadInt(descriptor, "force") != 0
	return PureResult{}
}

func (r *Runtime) freeBuildSources() {
 r.BuildForce = false
	for i := range r.BuildSources {
		mem.FreeString(r.Alloc, r.BuildSources[i].Name)
		mem.FreeString(r.Alloc, r.BuildSources[i].Text)
	}
	slices.Free(r.Alloc, r.BuildSources)
	r.BuildSources = nil
	for i := range r.BuildToolOverrides { mem.FreeString(r.Alloc, r.BuildToolOverrides[i]) }
	slices.Free(r.Alloc, r.BuildToolOverrides); r.BuildToolOverrides = nil
	for i := range r.BuildDefines { mem.FreeString(r.Alloc, r.BuildDefines[i]) }
	for i := range r.BuildEnvironment { mem.FreeString(r.Alloc, r.BuildEnvironment[i]) }
	slices.Free(r.Alloc, r.BuildDefines); r.BuildDefines = nil
	slices.Free(r.Alloc, r.BuildEnvironment); r.BuildEnvironment = nil
}

func (r *Runtime) compileBuild(options program.Options) program.CompileResult {
	options.Force = r.BuildForce
	options.Defines = r.BuildDefines
	options.ToolOverrides = r.BuildToolOverrides
	var environment []string
	for i := range options.Environment { environment = slices.Append(r.Alloc, environment, options.Environment[i]) }
	for i := range r.BuildEnvironment { environment = slices.Append(r.Alloc, environment, r.BuildEnvironment[i]) }
	options.Environment = environment
	defer slices.Free(r.Alloc, environment)
	if len(r.BuildSources) != 0 {
		return program.CompileMany(r.Alloc, r.BuildSources, r.Registry, options)
	}
	return program.Compile(r.Alloc, r.Parsed, r.Registry, options)
}

func (r *Runtime) buildCompileFailure(compiled *program.CompileResult) PureResult {
	buffer := bytes.NewBuffer(r.Alloc, nil)
	for i := range compiled.Diagnostics {
		program.WriteJSONDiagnostic(&buffer, compiled.Diagnostics[i])
	}
	r.EventJSONClear()
	r.EventJSON = slices.Clone(r.Alloc, []byte(buffer.String()))
	buffer.Free()
	if len(compiled.Diagnostics) != 0 {
		d := compiled.Diagnostics[0]
		return PureResult{Code: pureText(r.Alloc, d.Code), Message: pureText(r.Alloc, d.Message), SpanStart: d.Span.Start, SpanEnd: d.Span.End}
	}
	return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "source cannot be compiled")}
}
