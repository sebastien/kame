package main

import (
	"littlemake/diagnostic"
	"littlemake/program"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

func writeJSONEvent(out io.Writer, event program.Event) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema"); e.Int(1)
	e.Str("type"); e.Str(eventType(event.Kind))
	e.Str("target"); e.Str(event.Target)
	e.Str("node"); e.Int(event.NodeID)
	e.Str("generation"); e.Int(event.Generation)
	e.Str("attempt"); e.Int(event.Attempt)
	if event.RequestID != 0 { e.Str("request"); e.Int(event.RequestID) }
	if len(event.Data) != 0 {
		e.Str("data")
		if utf8.Valid(event.Data) { e.Str(string(event.Data)); e.Str("encoding"); e.Str("utf-8")
		} else { encoded := base64Text(event.Data); e.Str(encoded); mem.FreeString(mem.System, encoded); e.Str("encoding"); e.Str("base64") }
	}
	if event.Cached { e.Str("cached"); e.Bool(true) }
	if event.Truncated { e.Str("truncated"); e.Bool(true) }
	if event.Diagnostic.Code != "" { e.Str("diagnostic"); encodeDiagnostic(&e, event.Diagnostic) }
	e.EndObject(); e.Flush(); io.WriteString(out, "\n")
}

func writeJSONDiagnostic(out io.Writer, d diagnostic.Diagnostic) {
	e := json.NewEncoder(out)
	e.BeginObject(); e.Str("schema"); e.Int(1); e.Str("type"); e.Str("diagnostic"); e.Str("diagnostic"); encodeDiagnostic(&e, d); e.EndObject(); e.Flush(); io.WriteString(out, "\n")
}

func encodeDiagnostic(e *json.Encoder, d diagnostic.Diagnostic) {
	e.BeginObject(); e.Str("code"); e.Str(d.Code); e.Str("severity")
	if d.Severity == diagnostic.Warning { e.Str("warning") } else if d.Severity == diagnostic.Fatal { e.Str("fatal") } else { e.Str("error") }
	e.Str("message"); e.Str(d.Message)
	if d.Source != "" { e.Str("source"); e.Str(d.Source) }
	e.Str("span"); e.BeginObject(); e.Str("start"); e.Int(int64(d.Span.Start)); e.Str("end"); e.Int(int64(d.Span.End)); e.EndObject()
	e.EndObject()
}

func eventType(kind program.EventKind) string {
	if kind == program.TargetStarted { return "target-started" }
	if kind == program.ProcessStarted { return "process-started" }
	if kind == program.ProcessExited { return "process-exited" }
	if kind == program.Stdout { return "stdout" }
	if kind == program.Stderr { return "stderr" }
	if kind == program.DependencyDiscovered { return "dependency" }
	if kind == program.Effect { return "effect" }
	if kind == program.TargetValue { return "target-value" }
	if kind == program.TargetCompleted { return "target-completed" }
	if kind == program.TargetFailed { return "target-failed" }
	if kind == program.TargetCancelled { return "target-cancelled" }
	return "cache-warning"
}

func base64Text(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := strings.NewBuilder(mem.System)
	defer b.Free()
	for i := 0; i < len(data); i += 3 {
		value := int(data[i]) << 16
		if i+1 < len(data) { value |= int(data[i+1]) << 8 }
		if i+2 < len(data) { value |= int(data[i+2]) }
		b.WriteByte(alphabet[(value>>18)&63]); b.WriteByte(alphabet[(value>>12)&63])
		if i+1 < len(data) { b.WriteByte(alphabet[(value>>6)&63]) } else { b.WriteByte('=') }
		if i+2 < len(data) { b.WriteByte(alphabet[value&63]) } else { b.WriteByte('=') }
	}
	return strings.Clone(mem.System, b.String())
}
