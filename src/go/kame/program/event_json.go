package program

import (
	"kame/core"
	"kame/diagnostic"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
	"solod.dev/so/unicode/utf8"
)

func diagnosticSeverity(severity diagnostic.Severity) string {
	if severity == diagnostic.Warning {
		return "warning"
	}
	if severity == diagnostic.Fatal {
		return "fatal"
	}
	return "error"
}

func WriteJSONEvent(out io.Writer, event Event) {
	WriteJSONEventWithAllocator(mem.System, out, event)
}

// WriteJSONEventWithAllocator keeps encoding scratch storage in the caller's
// allocator. Freestanding hosts must not leak binary encoding into a global
// bump heap whose Free operation cannot reclaim it.
func WriteJSONEventWithAllocator(a mem.Allocator, out io.Writer, event Event) {
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
	if event.Key.Name != "" {
		e.Str("resource")
		encodeResourceKey(&e, event.Key)
	}
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
			encoded := base64Text(a, event.Data)
			e.Str(encoded)
			mem.FreeString(a, encoded)
			e.Str("encoding")
			e.Str("base64")
		}
	}
	if event.DependencyKey.Name != "" {
		e.Str("dependency")
		e.BeginObject()
		e.Str("node")
		e.Int(event.DependencyID)
		e.Str("resource")
		encodeResourceKey(&e, event.DependencyKey)
		e.EndObject()
	}
	if event.Effect != "" {
		e.Str("effect")
		e.Str(event.Effect)
	}
	if event.Kind == ServiceState {
		e.Str("state")
		e.Str(event.State)
	}
	if event.Program != "" {
		e.Str("program")
		e.Str(event.Program)
		e.Str("argv")
		encodeStringArray(&e, event.Argv)
	}
	if event.HasRuntime {
		e.Str("runtimeMS")
		e.Int(event.RuntimeMS)
	}
	if event.DisplayTruncated {
		e.Str("displayTruncated")
		e.Bool(true)
	}
	if event.Span.Start != 0 || event.Span.End != 0 {
		encodeSpan(&e, event.Span)
	}
	if event.Kind == TargetValue {
		e.Str("value")
		encodeValue(a, &e, event.Value)
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

func encodeResourceKey(e *json.Encoder, key core.ResourceKey) {
	e.BeginObject()
	e.Str("kind")
	e.Str(eventResourceKind(key.Kind))
	e.Str("name")
	e.Str(key.Name)
	e.EndObject()
}

func eventResourceKind(kind core.ResourceKind) string {
	if kind == core.ResourceDefinition {
		return "definition"
	}
	if kind == core.ResourceTarget {
		return "target"
	}
	if kind == core.ResourceFile {
		return "file"
	}
	if kind == core.ResourceTask {
		return "task"
	}
	if kind == core.ResourceService {
		return "service"
	}
	if kind == core.ResourceGlob {
		return "glob"
	}
	if kind == core.ResourceTool { return "tool" }
	if kind == core.ResourceOperation { return "operation" }
	return "environment"
}

func encodeValue(a mem.Allocator, e *json.Encoder, value core.Value) {
	e.BeginObject()
	e.Str("kind")
	e.Str(valueKind(value.Kind))
	if value.Kind == core.Bool {
		e.Str("data")
		e.Bool(value.Bool)
	}
	if value.Kind == core.Int {
		e.Str("data")
		e.Int(value.Int)
	}
	if value.Kind == core.Float {
		e.Str("data")
		e.Float(value.Float)
	}
	if value.Kind == core.String || value.Kind == core.Pattern {
		e.Str("data")
		e.Str(value.Text)
	}
	if value.Kind == core.Bytes {
		e.Str("data")
		if utf8.Valid(value.Bytes) {
			e.Str(string(value.Bytes))
			e.Str("encoding")
			e.Str("utf-8")
		} else {
			encoded := base64Text(a, value.Bytes)
			e.Str(encoded)
			mem.FreeString(a, encoded)
			e.Str("encoding")
			e.Str("base64")
		}
	}
	if value.Kind == core.List {
		e.Str("items")
		e.BeginArray()
		for i := range value.List {
			encodeValue(a, e, value.List[i])
		}
		e.EndArray()
	}
	if value.Kind == core.Record {
		e.Str("fields")
		e.BeginArray()
		for i := range value.Record {
			e.BeginObject()
			e.Str("name")
			e.Str(value.Record[i].Key)
			e.Str("value")
			encodeValue(a, e, value.Record[i].Value)
			e.EndObject()
		}
		e.EndArray()
	}
	if value.Kind == core.Resource {
		e.Str("resource")
		encodeResourceKey(e, value.Resource)
	}
	e.EndObject()
}

func valueKind(kind core.Kind) string {
	if kind == core.Nil {
		return "nil"
	}
	if kind == core.Bool {
		return "bool"
	}
	if kind == core.Int {
		return "int"
	}
	if kind == core.Float {
		return "float"
	}
	if kind == core.String {
		return "string"
	}
	if kind == core.Bytes {
		return "bytes"
	}
	if kind == core.List {
		return "list"
	}
	if kind == core.Record {
		return "record"
	}
	if kind == core.Callable {
		return "callable"
	}
	if kind == core.Resource {
		return "resource"
	}
	return "pattern"
}

func WriteJSONDiagnostic(out io.Writer, d diagnostic.Diagnostic) {
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
	if cause.OutputWasStreamed {
		e.Str("outputWasStreamed")
		e.Bool(true)
	}
	if cause.StdoutLimit > 0 || cause.StdoutTruncated {
		e.Str("stdoutTruncated")
		e.Bool(cause.StdoutTruncated)
		e.Str("stdoutLimit")
		e.Int(int64(cause.StdoutLimit))
	}
	if cause.StderrLimit > 0 || cause.StderrTruncated {
		e.Str("stderrTruncated")
		e.Bool(cause.StderrTruncated)
		e.Str("stderrLimit")
		e.Int(int64(cause.StderrLimit))
	}
	e.EndObject()
}

func eventType(kind EventKind) string {
	if kind == TargetStarted {
		return "target-started"
	}
	if kind == ProcessStarted {
		return "process-started"
	}
	if kind == ProcessExited {
		return "process-exited"
	}
	if kind == Stdout {
		return "stdout"
	}
	if kind == Stderr {
		return "stderr"
	}
	if kind == DependencyDiscovered {
		return "dependency"
	}
	if kind == Effect {
		return "effect"
	}
	if kind == TargetValue {
		return "target-value"
	}
	if kind == TargetCompleted {
		return "target-completed"
	}
	if kind == TargetFailed {
		return "target-failed"
	}
	if kind == TargetCancelled {
		return "target-cancelled"
	}
	if kind == ServiceState {
		return "service-state"
	}
	return "cache-warning"
}

func base64Text(a mem.Allocator, data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := strings.NewBuilder(a)
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
	return strings.Clone(a, b.String())
}
