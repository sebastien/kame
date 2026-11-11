package wasm

import (
	"bytes"
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/program"
	"slices"
)

// PrepareSession consumes a host-loaded descriptor. Parsing/registration and
// sequencing are portable; the wrapper only loads source bytes and services I/O.
func (r *Runtime) PrepareSession(data []byte) PureResult {
	if r.Program != nil || r.Node != nil {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "runtime already has active work")}
	}
	var value core.Value
	if !CompletionValueFromJSON(r.Alloc, data, &value) {
		return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid session descriptor")}
	}
	defer value.Free(r.Alloc)
	if !validDescriptorOptions(value, true) {
		return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid session options")}
	}
	inputs := host.PayloadList(value, "fragments")
	for i := range inputs {
		if !validSessionFragment(inputs[i]) {
			return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid session fragment")}
		}
	}
	var fragments []program.Fragment
	for i := range inputs {
		var entries []string
		var defines []string
		fields := host.PayloadList(inputs[i], "defines")
		for j := range fields {
			defines = append(defines, fields[j].Text)
		}
		items := host.PayloadList(inputs[i], "entries")
		for j := range items {
			entries = append(entries, items[j].Text)
		}
		f := program.Fragment{Name: host.PayloadText(inputs[i], "name"), Text: host.PayloadText(inputs[i], "text"), SourceDirectory: host.PayloadText(inputs[i], "sourceDirectory"), Signature: descriptorSourceSignature(inputs[i]), Lang: host.PayloadText(inputs[i], "lang"), Entries: entries, Inline: host.PayloadInt(inputs[i], "inline") != 0, Offset: int(host.PayloadInt(inputs[i], "offset")), Includes: descriptorIncludes(r.Alloc, inputs[i]), SkipStatements: host.PayloadInt(inputs[i], "skipStatements") != 0, Comment: host.PayloadText(inputs[i], "comment"), Defines: defines, Check: host.PayloadInt(inputs[i], "check") != 0}
		if f.Lang != "km" && f.Lang != "kmk" && f.Lang != "kash" && f.Lang != "expr" && f.Lang != "template" {
			for j := range fragments {
				freeDescriptorIncludes(r.Alloc, fragments[j].Includes)
				mem.FreeSlice(nil, fragments[j].Entries)
				mem.FreeSlice(nil, fragments[j].Defines)
			}
			mem.FreeSlice(nil, defines)
			freeDescriptorIncludes(r.Alloc, f.Includes)
			mem.FreeSlice(nil, entries)
			mem.FreeSlice(nil, fragments)
			return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid session language")}
		}
		fragments = append(fragments, f)
	}
	if len(fragments) == 0 {
		mem.FreeSlice(nil, fragments)
		return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "empty session descriptor")}
	}
	grants := r.InspectionGrants
	var overrides, environment []string
	fields := host.PayloadList(value, "buildDefines")
	for i := range fields {
		overrides = append(overrides, fields[i].Text)
	}
	for i := range r.Environment {
		environment = append(environment, r.Environment[i])
	}
	fields = host.PayloadList(value, "environment")
	for i := range fields {
		environment = append(environment, fields[i].Text)
	}
	var tools []string
	fields = host.PayloadList(value, "toolOverrides")
	for i := range fields {
		tools = append(tools, fields[i].Text)
	}
	options := program.Options{TimeoutMS: host.PayloadInt(value, "timeoutMS"), RetryCount: int(host.PayloadInt(value, "retryCount")), ToolOverrides: tools, Host: r.Host, Directory: r.Directory, Jobs: 1, Environment: environment, Defines: overrides, Grants: grants, ForwardRequests: true, CaptureLimit: int(host.PayloadInt(value, "captureLimit")), DryRun: host.PayloadInt(value, "dryRun") != 0, Force: host.PayloadInt(value, "force") != 0}
	options.HasInput = host.PayloadInt(value, "dataInput") == 1
	options.TrackTimings = host.PayloadInt(value, "trackTimings") != 0
	options.TimingNow, options.TimingClock = runtimeFinalizationNow, r
	options.RetainBytes = int(host.PayloadInt(value, "retainBytes"))
	options.PrintResult = host.PayloadInt(value, "printResult") != 0
	var parameters []string
	fields = host.PayloadList(value, "parameters")
	for i := range fields {
		parameters = append(parameters, fields[i].Text)
	}
	options.Parameters = parameters
	compiled := program.CompileSession(r.Alloc, fragments, r.Registry, options)
	mem.FreeSlice(nil, parameters)
	mem.FreeSlice(nil, tools)
	mem.FreeSlice(nil, overrides)
	mem.FreeSlice(nil, environment)
	r.Host = nil
	for i := range fragments {
		freeDescriptorIncludes(r.Alloc, fragments[i].Includes)
		mem.FreeSlice(nil, fragments[i].Entries)
		mem.FreeSlice(nil, fragments[i].Defines)
	}
	mem.FreeSlice(nil, fragments)
	if compiled.Session == nil {
		buffer := bytes.Buffer{}
		for i := range compiled.Diagnostics {
			program.WriteJSONDiagnostic(&buffer, compiled.Diagnostics[i])
		}
		r.EventJSONClear()
		r.EventJSON = slices.Clone([]byte(buffer.String()))
		buffer.Reset()
		out := PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "cannot compile session")}
		if len(compiled.Diagnostics) != 0 {
			out.Free(r.Alloc)
			out = PureResult{Code: pureText(r.Alloc, compiled.Diagnostics[0].Code), Message: pureText(r.Alloc, compiled.Diagnostics[0].Message), SpanStart: compiled.Diagnostics[0].Span.Start, SpanEnd: compiled.Diagnostics[0].Span.End}
		}
		compiled.Free(r.Alloc)
		return out
	}
	r.Session, r.Program = compiled.Session, compiled.Session.Program
	r.Session.SetJSON(host.PayloadInt(value, "json") != 0)
	compiled.Free(r.Alloc)
	args := host.PayloadList(value, "args")
	r.Program.Eval.SetDefinitionArgs(args)
	if !r.Session.JSON {
		r.Program.Eval.SetDefinitionEffectSink(sessionEffect, r)
	}
	return PureResult{}
}

func sessionEffect(value any, effect eval.Effect) {
	r := value.(*Runtime)
	r.Effects = append(r.Effects, eval.Effect{Kind: effect.Kind, Data: slices.Clone(effect.Data)})
}

func (r *Runtime) SessionWorkCount() int {
	if r.Session == nil {
		return 0
	}
	return len(r.Session.Work) + 1 // Final invocation-owned join, never a displayed value.
}

// SessionExitStatus is separate from result values and diagnostics. A negative
// status means the ordered session has not requested termination.
func (r *Runtime) SessionExitStatus() int {
	if r.Session == nil || !r.Session.Exit.Requested {
		return -1
	}
	return r.Session.Exit.Status
}

// CaptureLimit reports the effective invocation capture bound. Zero means no
// session or build program has been compiled yet.
func (r *Runtime) CaptureLimit() int {
	if r == nil {
		return 0
	}
	if r.Session != nil && r.Session.Program != nil {
		return r.Session.Program.Options.CaptureLimit
	}
	if r.Program != nil {
		return r.Program.Options.CaptureLimit
	}
	return 0
}

func (r *Runtime) SessionWorkKind(index int) uint32 {
	if r.Session == nil || index < 0 || index >= len(r.Session.Work) {
		return 0
	}
	w := r.Session.Work[index]
	if w.Selected && w.Display {
		return 2
	}
	if w.Value && w.Display {
		return 1
	}
	return 3
}

func (r *Runtime) RequestSessionWork(index int) PureResult {
	if r.Session == nil || index < 0 || index > len(r.Session.Work) {
		return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "invalid session work index")}
	}
	if r.Handle != nil {
		r.Handle.Free()
		r.Handle = nil
	}
	eval.FreeEffects(r.Alloc, r.Effects)
	r.Effects, r.EffectIndex = nil, 0
	start := r.Session.Start(index)
	if start.Diagnostic.Code != "" {
		out := PureResult{Code: pureText(r.Alloc, start.Diagnostic.Code), Message: pureText(r.Alloc, start.Diagnostic.Message)}
		start.Diagnostic.Free(r.Alloc)
		return out
	}
	r.Handle = start.Handle
	return PureResult{}
}
