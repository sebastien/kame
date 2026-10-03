// Package host defines portable requests issued by evaluators and runtimes.
package host

import (
	"kame/core"
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// Payload record fields shared by library operations and runtime hosts.
const (
	FieldOp     = "op"
	FieldPath   = "path"
	FieldData   = "data"
	FieldScript = "script"
	FieldArgv   = "argv"
	FieldStages = "stages"
	FieldInput = "input"
	FieldOutput = "output"
	FieldAppend = "append"
	FieldSetup = "setup"
	FieldCwd = "cwd"
	FieldTimeout = "timeoutMS"
	FieldEnvironment = "environment"
	FieldKey    = "key"
	FieldRecord = "record"
	OpRead      = "read"
	OpExists    = "exists"
 OpOutputExists = "output-exists"
	OpStat      = "stat"
	OpWildcard  = "wildcard"
)

// FilePayload creates a filesystem request payload. A string payload remains a
// read request for callers that predate the record protocol.
func FilePayload(a mem.Allocator, op string, name string) core.Value {
	fields := []core.RecordField{{Key: FieldOp, Value: core.NewString(a, op)}, {Key: FieldPath, Value: core.NewString(a, name)}}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	return payload
}

func ProcessPayload(a mem.Allocator, script string) core.Value {
	fields := []core.RecordField{{Key: FieldScript, Value: core.NewString(a, script)}}
	payload := core.NewRecord(a, fields)
	fields[0].Value.Free(a)
	return payload
}


// RecipePayload carries the file outputs whose parent directories the embedding
// host must create before starting a shell recipe. Script remains available to
// in-process hosts; Data is the structured freestanding wire spelling.
func RecipePayload(a mem.Allocator, script string, outputs []string) core.Value {
 return RecipeEnvironmentPayload(a, script, outputs, nil)
}

// RecipeEnvironmentPayload also carries the exact child environment when scoped.
func RecipeEnvironmentPayload(a mem.Allocator, script string, outputs []string, environment []string) core.Value {
 b := strings.NewBuilder(a)
 e := json.NewEncoder(&b)
 e.BeginObject(); e.Str("script"); e.Str(script); e.Str("outputs"); e.BeginArray()
 for i := range outputs { e.Str(outputs[i]) }
 e.EndArray()
 if environment != nil {
  e.Str("environment"); e.BeginArray()
  for i := range environment { e.Str(environment[i]) }
  e.EndArray()
 }
 e.EndObject(); e.Flush()
 fields := []core.RecordField{{Key: FieldOp, Value: core.NewString(a, "recipe")}, {Key: FieldScript, Value: core.NewString(a, script)}, {Key: FieldData, Value: core.NewString(a, b.String())}}
 payload := core.NewRecord(a, fields)
 for i := range fields { fields[i].Value.Free(a) }
 b.Free()
 return payload
}

// ArgvPayload is a structured process request, distinct from shell script text.
// The JSON data is the portable wire spelling used by freestanding hosts.
func ArgvPayload(a mem.Allocator, argv []core.Value) core.Value {
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginArray()
	for i := range argv { e.Str(argv[i].Text) }
	e.EndArray()
	e.Flush()
	fields := []core.RecordField{{Key: FieldArgv, Value: core.NewList(a, argv)}, {Key: FieldData, Value: core.NewString(a, b.String())}, {Key: FieldPath, Value: core.NewString(a, argv[0].Text)}}
	payload := core.NewRecord(a, fields)
	for i := range fields { fields[i].Value.Free(a) }
	b.Free()
	return payload
}

// PayloadArgv returns a borrowed argv list, or nil for legacy shell requests.
func PayloadArgv(payload core.Value) []core.Value {
	for i := range payload.Record { if payload.Record[i].Key == FieldArgv { return payload.Record[i].Value.List } }
	return nil
}

// PipelinePayload carries all stage argv lists in one launch request. Hosts
// connect them with streaming pipes, never shell text or intermediate capture.
func PipelinePayload(a mem.Allocator, stages []core.Value) core.Value {
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginArray()
	for i := range stages {
		e.BeginArray()
		for j := range stages[i].List { e.Str(stages[i].List[j].Text) }
		e.EndArray()
	}
	e.EndArray()
	e.Flush()
	fields := []core.RecordField{{Key: FieldStages, Value: core.NewList(a, stages)}, {Key: FieldData, Value: core.NewString(a, b.String())}, {Key: FieldPath, Value: core.NewString(a, stages[0].List[0].Text)}}
	payload := core.NewRecord(a, fields)
	for i := range fields { fields[i].Value.Free(a) }
	b.Free()
	return payload
}

func PayloadStages(payload core.Value) []core.Value {
	for i := range payload.Record { if payload.Record[i].Key == FieldStages { return payload.Record[i].Value.List } }
	return nil
}

// ConfigureRedirections replaces only the wire spelling; argv stays structured.
func ConfigureRedirections(a mem.Allocator, payload *core.Value, input string, output string, appendOutput bool) {
	fields := []core.RecordField{{Key: FieldInput, Value: core.NewString(a, input)}, {Key: FieldOutput, Value: core.NewString(a, output)}, {Key: FieldAppend, Value: core.Value{Kind: core.Bool, Bool: appendOutput}}}
	for i := range fields { payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, fields[i].Key).Text, Value: fields[i].Value}) }
	writeConfiguredGraph(a, payload)
}

func StageSetupPayload(a mem.Allocator, cwd string, timeout int64, environment []core.Value) core.Value {
	fields := []core.RecordField{{Key: FieldCwd, Value: core.NewString(a, cwd)}, {Key: FieldTimeout, Value: core.Value{Kind: core.Int, Int: timeout}}, {Key: FieldEnvironment, Value: core.NewList(a, environment)}}
	value := core.NewRecord(a, fields)
	for i := range fields { fields[i].Value.Free(a) }
	return value
}

func ConfigureStages(a mem.Allocator, payload *core.Value, setups []core.Value) {
	payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, FieldSetup).Text, Value: core.NewList(a, setups)})
	writeConfiguredGraph(a, payload)
}

func PayloadSetups(payload core.Value) []core.Value { return PayloadList(payload, FieldSetup) }

func PayloadList(payload core.Value, field string) []core.Value {
	for i := range payload.Record { if payload.Record[i].Key == field { return payload.Record[i].Value.List } }
	return nil
}

func PayloadInt(payload core.Value, field string) int64 {
	for i := range payload.Record { if payload.Record[i].Key == field { return payload.Record[i].Value.Int } }
	return 0
}

func writeConfiguredGraph(a mem.Allocator, payload *core.Value) {
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginObject()
	e.Str("stages"); e.BeginArray()
	stages := PayloadStages(*payload)
	for i := range stages { e.BeginArray(); for j := range stages[i].List { e.Str(stages[i].List[j].Text) }; e.EndArray() }
	e.EndArray()
	e.Str("input"); e.Str(PayloadText(*payload, FieldInput))
	e.Str("output"); e.Str(PayloadText(*payload, FieldOutput))
	e.Str("append"); e.Bool(PayloadAppend(*payload))
	for i := range payload.Record { if payload.Record[i].Key == "stream" { e.Str("stream"); e.Bool(payload.Record[i].Value.Bool) } }
	for i := range payload.Record { if payload.Record[i].Key == "acceptExit" { e.Str("acceptExit"); e.Bool(payload.Record[i].Value.Bool) } }
	e.Str("setup"); e.BeginArray()
	setups := PayloadSetups(*payload)
	for i := range setups {
		e.BeginObject()
		e.Str(FieldCwd); e.Str(PayloadText(setups[i], FieldCwd))
		e.Str(FieldTimeout); e.Int(PayloadInt(setups[i], FieldTimeout))
		e.Str(FieldEnvironment); e.BeginArray()
		values := PayloadList(setups[i], FieldEnvironment)
		for j := range values { e.Str(values[j].Text) }
		e.EndArray(); e.EndObject()
	}
	e.EndArray()
	e.EndObject(); e.Flush()
	for i := range payload.Record { if payload.Record[i].Key == FieldData { payload.Record[i].Value.Free(a); payload.Record[i].Value = core.NewString(a, b.String()) } }
	b.Free()
}

func PayloadAppend(payload core.Value) bool {
	for i := range payload.Record { if payload.Record[i].Key == FieldAppend { return payload.Record[i].Value.Bool } }
	return false
}

func WritePayload(a mem.Allocator, name string, data []byte) core.Value {
	fields := []core.RecordField{{Key: FieldPath, Value: core.NewString(a, name)}, {Key: FieldData, Value: core.NewBytes(a, data)}}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	return payload
}

// CacheGetPayload identifies one opaque cache record. Cache keys are bytes,
// rather than paths, because the portable runtime defines their encoding.
func CacheGetPayload(a mem.Allocator, key []byte) core.Value {
	return core.NewBytes(a, key)
}

// CachePutPayload carries a complete cache record. Hosts must make a complete
// record visible atomically, or report a failure; partial records are never
// valid cache hits.
func CachePutPayload(a mem.Allocator, key []byte, record []byte) core.Value {
	fields := []core.RecordField{{Key: FieldKey, Value: core.NewBytes(a, key)}, {Key: FieldRecord, Value: core.NewBytes(a, record)}}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	return payload
}

// CacheKey returns the cache key from a cache get, put, or delete payload.
func CacheKey(payload core.Value) []byte {
	if payload.Kind == core.Bytes {
		return payload.Bytes
	}
	return PayloadBytes(payload, FieldKey)
}

// CacheRecord returns a cache record from a cache put payload.
func CacheRecord(payload core.Value) []byte { return PayloadBytes(payload, FieldRecord) }

// PayloadPath returns the path or environment name carried by a request.
func PayloadPath(payload core.Value) string {
	if payload.Kind == core.String {
		return payload.Text
	}
	if payload.Kind != core.Record {
		return ""
	}
	for i := range payload.Record {
		if payload.Record[i].Key == FieldPath && payload.Record[i].Value.Kind == core.String {
			return payload.Record[i].Value.Text
		}
	}
	return ""
}

func PayloadText(payload core.Value, field string) string {
	if payload.Kind == core.String && field == FieldScript {
		return payload.Text
	}
	if payload.Kind != core.Record {
		return ""
	}
	for i := range payload.Record {
		if payload.Record[i].Key == field && payload.Record[i].Value.Kind == core.String {
			return payload.Record[i].Value.Text
		}
	}
	return ""
}

func PayloadBytes(payload core.Value, field string) []byte {
	if payload.Kind != core.Record {
		return nil
	}
	for i := range payload.Record {
		if payload.Record[i].Key == field && payload.Record[i].Value.Kind == core.Bytes {
			return payload.Record[i].Value.Bytes
		}
	}
	return nil
}
