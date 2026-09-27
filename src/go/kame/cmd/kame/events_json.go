package main

import (
	"kame/diagnostic"
	"kame/program"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

func writeJSONEvent(out io.Writer, event program.Event) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str(eventType(event.Kind))
	e.Str("target")
	e.Str(event.Target)
	e.Str("node")
	e.Int(event.NodeID)
	e.Str("generation")
	e.Int(event.Generation)
	e.Str("attempt")
	e.Int(event.Attempt)
	if event.RequestID != 0 {
		e.Str("request")
		e.Int(event.RequestID)
	}
	if len(event.Data) != 0 {
		e.Str("data")
		if utf8.Valid(event.Data) {
			e.Str(string(event.Data))
			e.Str("encoding")
			e.Str("utf-8")
		} else {
			encoded := base64Text(event.Data)
			e.Str(encoded)
			mem.FreeString(mem.System, encoded)
			e.Str("encoding")
			e.Str("base64")
		}
	}
	if event.Cached {
		e.Str("cached")
		e.Bool(true)
	}
	if event.Truncated {
		e.Str("truncated")
		e.Bool(true)
	}
	if event.Diagnostic.Code != "" {
		e.Str("diagnostic")
		encodeDiagnostic(&e, event.Diagnostic)
	}
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}

func writeJSONDiagnostic(out io.Writer, d diagnostic.Diagnostic) {
	e := json.NewEncoder(out)
	e.BeginObject()
	e.Str("schema")
	e.Int(1)
	e.Str("type")
	e.Str("diagnostic")
	e.Str("diagnostic")
	encodeDiagnostic(&e, d)
	e.EndObject()
	e.Flush()
	io.WriteString(out, "\n")
}

func encodeDiagnostic(e *json.Encoder, d diagnostic.Diagnostic) {
	e.BeginObject()
	e.Str("code")
	e.Str(d.Code)
	e.Str("severity")
	e.Str(diagnosticSeverity(d.Severity))
	e.Str("message")
	e.Str(d.Message)
	if d.Source != "" {
		e.Str("source")
		e.Str(d.Source)
		encodeSpan(e, d.Span)
	}
	if len(d.Notes) != 0 {
		e.Str("notes")
		e.BeginArray()
		for i := range d.Notes {
			e.Str(d.Notes[i])
		}
		e.EndArray()
	}
	if len(d.Related) != 0 {
		e.Str("related")
		e.BeginArray()
		for i := range d.Related {
			e.BeginObject()
			e.Str("message")
			e.Str(d.Related[i].Message)
			if d.Related[i].Source != "" {
				e.Str("source")
				e.Str(d.Related[i].Source)
				encodeSpan(e, d.Related[i].Span)
			}
			e.EndObject()
		}
		e.EndArray()
	}
	if len(d.Frames) != 0 {
		e.Str("frames")
		e.BeginArray()
		for i := range d.Frames {
			e.BeginObject()
			if d.Frames[i].Kind != "" {
				e.Str("kind")
				e.Str(d.Frames[i].Kind)
			}
			e.Str("label")
			e.Str(d.Frames[i].Label)
			if d.Frames[i].Source != "" {
				e.Str("source")
				e.Str(d.Frames[i].Source)
				encodeSpan(e, d.Frames[i].Span)
			}
			e.EndObject()
		}
		e.EndArray()
	}
	if d.Target != "" {
		e.Str("target")
		e.Str(d.Target)
	}
	if len(d.TargetStack) != 0 {
		e.Str("targetStack")
		encodeStringArray(e, d.TargetStack)
	}
	if len(d.Tips) != 0 {
		e.Str("tips")
		encodeStringArray(e, d.Tips)
	}
	if d.Cause.Kind != "" {
		encodeCause(e, d.Cause)
	}
	e.EndObject()
}

func encodeSpan(e *json.Encoder, span diagnostic.Span) {
	e.Str("span")
	e.BeginObject()
	e.Str("start")
	e.Int(int64(span.Start))
	e.Str("end")
	e.Int(int64(span.End))
	e.EndObject()
}

func encodeStringArray(e *json.Encoder, values []string) {
	e.BeginArray()
	for i := range values {
		e.Str(values[i])
	}
	e.EndArray()
}

func encodeCause(e *json.Encoder, cause diagnostic.Cause) {
	e.Str("cause")
	e.BeginObject()
	e.Str("kind")
	e.Str(cause.Kind)
	e.Str("message")
	e.Str(cause.Message)
	if cause.Program != "" {
		e.Str("program")
		e.Str(cause.Program)
	}
	if cause.HasStatus {
		e.Str("status")
		e.Int(int64(cause.Status))
	}
	if cause.HasSignal {
		e.Str("signal")
		e.Int(int64(cause.Signal))
	}
	if cause.Stdout != "" {
		e.Str("stdout")
		e.Str(cause.Stdout)
	}
	if cause.Stderr != "" {
		e.Str("stderr")
		e.Str(cause.Stderr)
	}
	if cause.Stdout != "" || cause.StdoutTruncated {
		e.Str("stdoutTruncated")
		e.Bool(cause.StdoutTruncated)
		e.Str("stdoutLimit")
		e.Int(int64(cause.StdoutLimit))
	}
	if cause.Stderr != "" || cause.StderrTruncated {
		e.Str("stderrTruncated")
		e.Bool(cause.StderrTruncated)
		e.Str("stderrLimit")
		e.Int(int64(cause.StderrLimit))
	}
	e.EndObject()
}

func eventType(kind program.EventKind) string {
	if kind == program.TargetStarted {
		return "target-started"
	}
	if kind == program.ProcessStarted {
		return "process-started"
	}
	if kind == program.ProcessExited {
		return "process-exited"
	}
	if kind == program.Stdout {
		return "stdout"
	}
	if kind == program.Stderr {
		return "stderr"
	}
	if kind == program.DependencyDiscovered {
		return "dependency"
	}
	if kind == program.Effect {
		return "effect"
	}
	if kind == program.TargetValue {
		return "target-value"
	}
	if kind == program.TargetCompleted {
		return "target-completed"
	}
	if kind == program.TargetFailed {
		return "target-failed"
	}
	if kind == program.TargetCancelled {
		return "target-cancelled"
	}
	return "cache-warning"
}

func base64Text(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := strings.NewBuilder(mem.System)
	defer b.Free()
	for i := 0; i < len(data); i += 3 {
		value := int(data[i]) << 16
		if i+1 < len(data) {
			value |= int(data[i+1]) << 8
		}
		if i+2 < len(data) {
			value |= int(data[i+2])
		}
		b.WriteByte(alphabet[(value>>18)&63])
		b.WriteByte(alphabet[(value>>12)&63])
		if i+1 < len(data) {
			b.WriteByte(alphabet[(value>>6)&63])
		} else {
			b.WriteByte('=')
		}
		if i+2 < len(data) {
			b.WriteByte(alphabet[value&63])
		} else {
			b.WriteByte('=')
		}
	}
	return strings.Clone(mem.System, b.String())
}
