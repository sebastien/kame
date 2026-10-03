package wasm

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/slices"
)

// PrepareSession consumes a host-loaded descriptor. Parsing/registration and
// sequencing are portable; the wrapper only loads source bytes and services I/O.
func (r *Runtime) PrepareSession(data []byte) PureResult {
	if r.Program != nil || r.Node != nil { return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "runtime already has active work")} }
	var value core.Value
	if !CompletionValueFromJSON(r.Alloc, data, &value) { return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid session descriptor")} }
	defer value.Free(r.Alloc)
	var fragments []program.Fragment
	inputs := host.PayloadList(value, "fragments")
	for i := range inputs {
		var entries []string
  var defines []string
  fields := host.PayloadList(inputs[i], "defines")
  for j := range fields { defines = slices.Append(r.Alloc, defines, fields[j].Text) }
		items := host.PayloadList(inputs[i], "entries")
		for j := range items { entries = slices.Append(r.Alloc, entries, items[j].Text) }
		f := program.Fragment{Name: host.PayloadText(inputs[i], "name"), Text: host.PayloadText(inputs[i], "text"), Lang: host.PayloadText(inputs[i], "lang"), Entries: entries, Inline: host.PayloadInt(inputs[i], "inline") != 0, Offset: int(host.PayloadInt(inputs[i], "offset")), SkipStatements: host.PayloadInt(inputs[i], "skipStatements") != 0, Comment: host.PayloadText(inputs[i], "comment"), Defines: defines, Check: host.PayloadInt(inputs[i], "check") != 0}
		if f.Lang != "km" && f.Lang != "kmk" && f.Lang != "kash" && f.Lang != "expr" && f.Lang != "template" {
			for j := range fragments { slices.Free(r.Alloc, fragments[j].Entries); slices.Free(r.Alloc, fragments[j].Defines) }
			slices.Free(r.Alloc, defines); slices.Free(r.Alloc, entries); slices.Free(r.Alloc, fragments)
			return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "invalid session language")}
		}
		fragments = slices.Append(r.Alloc, fragments, f)
	}
	if len(fragments) == 0 { slices.Free(r.Alloc, fragments); return PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "empty session descriptor")} }
	grants := r.InspectionGrants
	options := program.Options{Host: r.Host, Directory: r.Directory, Jobs: 1, Environment: r.Environment, Grants: grants, ForwardRequests: true, CaptureLimit: int(host.PayloadInt(value, "captureLimit")), DryRun: host.PayloadInt(value, "dryRun") != 0}
	compiled := program.CompileSession(r.Alloc, fragments, r.Registry, options)
	r.Host = nil
	for i := range fragments { slices.Free(r.Alloc, fragments[i].Entries); slices.Free(r.Alloc, fragments[i].Defines) }
	slices.Free(r.Alloc, fragments)
	if compiled.Session == nil {
		buffer := bytes.NewBuffer(r.Alloc, nil)
			for i := range compiled.Diagnostics { program.WriteJSONDiagnostic(&buffer, compiled.Diagnostics[i]) }
			r.EventJSONClear()
			r.EventJSON = slices.Clone(r.Alloc, []byte(buffer.String()))
			buffer.Free()
		out := PureResult{Code: pureText(r.Alloc, "PARSE_ERR"), Message: pureText(r.Alloc, "cannot compile session")}
		if len(compiled.Diagnostics) != 0 { out.Free(r.Alloc); out = PureResult{Code: pureText(r.Alloc, compiled.Diagnostics[0].Code), Message: pureText(r.Alloc, compiled.Diagnostics[0].Message), SpanStart: compiled.Diagnostics[0].Span.Start, SpanEnd: compiled.Diagnostics[0].Span.End} }
		compiled.Free(r.Alloc)
		return out
	}
	r.Session, r.Program = compiled.Session, compiled.Session.Program
	r.Session.SetJSON(host.PayloadInt(value, "json") != 0)
	compiled.Free(r.Alloc)
	args := host.PayloadList(value, "args")
	r.Program.Eval.SetDefinitionArgs(args)
	if !r.Session.JSON { r.Program.Eval.SetDefinitionEffectSink(sessionEffect, r) }
	return PureResult{}
}

func sessionEffect(value any, effect eval.Effect) {
	r := value.(*Runtime)
	r.Effects = slices.Append(r.Alloc, r.Effects, eval.Effect{Kind: effect.Kind, Data: slices.Clone(r.Alloc, effect.Data)})
}

func (r *Runtime) SessionWorkCount() int {
	if r.Session == nil { return 0 }
	return len(r.Session.Work)+1 // Final invocation-owned join, never a displayed value.
}

func (r *Runtime) SessionWorkKind(index int) uint32 {
	if r.Session == nil || index < 0 || index >= len(r.Session.Work) { return 0 }
	w := r.Session.Work[index]
	if w.Selected { return 2 }
	if w.Value { return 1 }
	return 3
}

func (r *Runtime) RequestSessionWork(index int) PureResult {
	if r.Session == nil || index < 0 || index > len(r.Session.Work) { return PureResult{Code: pureText(r.Alloc, "PHASE_INVALID"), Message: pureText(r.Alloc, "invalid session work index")} }
	if r.Handle != nil { r.Handle.Free(); r.Handle = nil }
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
