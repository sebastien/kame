// Package host defines portable requests issued by evaluators and runtimes.
package host

import (
	"kame/core"
	"solod.dev/so/encoding/json"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
)

// Payload record fields shared by library operations and runtime hosts.
const (
	FieldOp          = "op"
	FieldPath        = "path"
	FieldData        = "data"
	FieldScript      = "script"
	FieldArgv        = "argv"
	FieldStages      = "stages"
	FieldInput       = "input"
	FieldOutput      = "output"
	FieldAppend      = "append"
	FieldError       = "error"
	FieldErrorAppend = "errorAppend"
	FieldErrorMode   = "errorMode"
	FieldCapture     = "capture"
	FieldSetup       = "setup"
	FieldCwd         = "cwd"
	FieldTimeout     = "timeoutMS"
	FieldEnvironment = "environment"
	FieldKey         = "key"
	FieldRecord      = "record"
	OpRead           = "read"
	OpFileContent    = "file-content"
	OpToolContent    = "tool-content"
	OpOutputContent  = "output-content"
	OpExists         = "exists"
	OpOutputExists   = "output-exists"
	OpFileTimes      = "file-times"
	OpStat           = "stat"
	OpLstat          = "lstat"
	OpReadlink       = "readlink"
	OpWildcard       = "wildcard"
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
	return RecipeExecutionPayload(a, script, outputs, environment, nil)
}

// RecipeExecutionPayload carries the selected rule shell argv as well as its environment.
func RecipeExecutionPayload(a mem.Allocator, script string, outputs []string, environment []string, shell []string) core.Value {
	return recipeExecutionPayload(a, script, outputs, environment, shell, environment != nil, false)
}

// ScopedProcessPayload carries an exact environment for a collected shell call,
// including an explicitly empty snapshot. It declares no file outputs and
// marks stdout/stderr/status as a captured operation result, not recipe streams.
func ScopedProcessPayload(a mem.Allocator, script string, environment []string) core.Value {
	return recipeExecutionPayload(a, script, nil, environment, nil, true, true)
}

// PluginInvocationPayload copies one operation call into a serializable host
// request. The host owns the payload and must return one core value or a
// diagnostic through the existing request correlation contract.
func PluginInvocationPayload(a mem.Allocator, plugin string, pluginVersion string, operation string, operationVersion string, maxRequestBytes int, maxResponseBytes int, timeoutMS int64, generation int64, attempt int64, args []core.Value) core.Value {
	values := core.NewList(a, args)
	var encodedArgs []core.Value
	for i := range args {
		encoded := core.PluginValueJSON(a, args[i])
		if encoded == nil {
			continue
		}
		encodedArgs = slices.Append(a, encodedArgs, core.NewString(a, string(encoded)))
		mem.FreeSlice(a, encoded)
	}
	wireValues := core.NewList(a, encodedArgs)
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginObject()
	e.Str("plugin")
	e.Str(plugin)
	e.Str("pluginVersion")
	e.Str(pluginVersion)
	e.Str("operation")
	e.Str(operation)
	e.Str("operationVersion")
	e.Str(operationVersion)
	e.Str("maxRequestBytes")
	e.Int(int64(maxRequestBytes))
	e.Str("maxResponseBytes")
	e.Int(int64(maxResponseBytes))
	e.Str("timeoutMS")
	e.Int(timeoutMS)
	e.Str("generation")
	e.Int(generation)
	e.Str("attempt")
	e.Int(attempt)
	e.Str("argsJSON")
	e.BeginArray()
	for i := range encodedArgs {
		e.Str(encodedArgs[i].Text)
	}
	e.EndArray()
	e.EndObject()
	e.Flush()
	fields := []core.RecordField{
		{Key: "plugin", Value: core.NewString(a, plugin)},
		{Key: "pluginVersion", Value: core.NewString(a, pluginVersion)},
		{Key: "operation", Value: core.NewString(a, operation)},
		{Key: "operationVersion", Value: core.NewString(a, operationVersion)},
		{Key: "maxRequestBytes", Value: core.Value{Kind: core.Int, Int: int64(maxRequestBytes)}},
		{Key: "maxResponseBytes", Value: core.Value{Kind: core.Int, Int: int64(maxResponseBytes)}},
		{Key: "timeoutMS", Value: core.Value{Kind: core.Int, Int: timeoutMS}},
		{Key: "generation", Value: core.Value{Kind: core.Int, Int: generation}},
		{Key: "attempt", Value: core.Value{Kind: core.Int, Int: attempt}},
		{Key: "args", Value: values},
		{Key: "argsJSON", Value: wireValues},
		{Key: FieldData, Value: core.NewString(a, b.String())},
	}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	b.Free()
	for i := range encodedArgs {
		encodedArgs[i].Free(a)
	}
	slices.Free(a, encodedArgs)
	return payload
}

func recipeExecutionPayload(a mem.Allocator, script string, outputs []string, environment []string, shell []string, includeEnvironment bool, capture bool) core.Value {
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginObject()
	e.Str("script")
	e.Str(script)
	e.Str("outputs")
	e.BeginArray()
	for i := range outputs {
		e.Str(outputs[i])
	}
	e.EndArray()
	if includeEnvironment {
		e.Str("environment")
		e.BeginArray()
		for i := range environment {
			e.Str(environment[i])
		}
		e.EndArray()
	}
	if shell != nil {
		e.Str("shell")
		e.BeginArray()
		for i := range shell {
			e.Str(shell[i])
		}
		e.EndArray()
	}
	if capture {
		e.Str("capture")
		e.Bool(true)
	}
	e.EndObject()
	e.Flush()
	fields := []core.RecordField{{Key: FieldOp, Value: core.NewString(a, "recipe")}, {Key: FieldScript, Value: core.NewString(a, script)}, {Key: FieldData, Value: core.NewString(a, b.String())}}
	payload := core.NewRecord(a, fields)
	if capture {
		payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, FieldCapture).Text, Value: core.Value{Kind: core.Bool, Bool: true}})
	}
	if includeEnvironment {
		var values []core.Value
		for i := range environment {
			values = slices.Append(a, values, core.NewString(a, environment[i]))
		}
		payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, FieldEnvironment).Text, Value: core.NewList(a, values)})
		for i := range values {
			values[i].Free(a)
		}
		slices.Free(a, values)
	}
	for i := range fields {
		fields[i].Value.Free(a)
	}
	b.Free()
	return payload
}

// ArgvPayload is a structured process request, distinct from shell script text.
// The JSON data is the portable wire spelling used by freestanding hosts.
func ArgvPayload(a mem.Allocator, argv []core.Value) core.Value {
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginArray()
	for i := range argv {
		e.Str(argv[i].Text)
	}
	e.EndArray()
	e.Flush()
	fields := []core.RecordField{{Key: FieldArgv, Value: core.NewList(a, argv)}, {Key: FieldData, Value: core.NewString(a, b.String())}, {Key: FieldPath, Value: core.NewString(a, argv[0].Text)}}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	b.Free()
	return payload
}

// ServiceProbePayload carries a direct-argv readiness probe and its execution
// setup through the same structured process boundary used by native hosts.
func ServiceProbePayload(a mem.Allocator, argv []string, cwd string, timeout int64, environment []string) core.Value {
	var arguments []core.Value
	for i := range argv {
		arguments = slices.Append(a, arguments, core.NewString(a, argv[i]))
	}
	var envValues []core.Value
	for i := range environment {
		envValues = slices.Append(a, envValues, core.NewString(a, environment[i]))
	}
	setup := StageSetupPayload(a, cwd, timeout, envValues)
	var setups []core.Value
	setups = slices.Append(a, setups, setup)
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginObject()
	e.Str("serviceReady")
	e.Bool(true)
	e.Str("stages")
	e.BeginArray()
	e.BeginArray()
	for i := range argv {
		e.Str(argv[i])
	}
	e.EndArray()
	e.EndArray()
	e.Str(FieldInput)
	e.Str("")
	e.Str(FieldOutput)
	e.Str("")
	e.Str(FieldAppend)
	e.Bool(false)
	e.Str(FieldSetup)
	e.BeginArray()
	e.BeginObject()
	e.Str(FieldCwd)
	e.Str(cwd)
	e.Str(FieldTimeout)
	e.Int(timeout)
	e.Str(FieldEnvironment)
	e.BeginArray()
	for i := range environment {
		e.Str(environment[i])
	}
	e.EndArray()
	e.EndObject()
	e.EndArray()
	e.EndObject()
	e.Flush()
	fields := []core.RecordField{
		{Key: FieldArgv, Value: core.NewList(a, arguments)},
		{Key: FieldSetup, Value: core.NewList(a, setups)},
		{Key: FieldData, Value: core.NewString(a, b.String())},
		{Key: FieldPath, Value: core.NewString(a, argv[0])},
	}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	setup.Free(a)
	slices.Free(a, setups)
	for i := range arguments {
		arguments[i].Free(a)
	}
	slices.Free(a, arguments)
	for i := range envValues {
		envValues[i].Free(a)
	}
	slices.Free(a, envValues)
	b.Free()
	return payload
}

func ServiceTimerPayload(a mem.Allocator, milliseconds int64) core.Value {
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.Int(milliseconds)
	e.Flush()
	fields := []core.RecordField{
		{Key: FieldOp, Value: core.NewString(a, "service-timer")},
		{Key: FieldData, Value: core.NewString(a, b.String())},
	}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	b.Free()
	return payload
}

func ServiceCancelPayload(a mem.Allocator, processID int64, graceMS int64) core.Value {
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginObject()
	var id [strconv.MaxIntBase10Len]byte
	e.Str("id")
	e.Str(strconv.FormatInt(id[:], processID, 10))
	e.Str("graceMS")
	e.Int(graceMS)
	e.EndObject()
	e.Flush()
	fields := []core.RecordField{
		{Key: FieldOp, Value: core.NewString(a, "service-cancel")},
		{Key: FieldData, Value: core.NewString(a, b.String())},
	}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	b.Free()
	return payload
}

// PayloadArgv returns a borrowed argv list, or nil for legacy shell requests.
func PayloadArgv(payload core.Value) []core.Value {
	for i := range payload.Record {
		if payload.Record[i].Key == FieldArgv {
			return payload.Record[i].Value.List
		}
	}
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
		for j := range stages[i].List {
			e.Str(stages[i].List[j].Text)
		}
		e.EndArray()
	}
	e.EndArray()
	e.Flush()
	program := ""
	for i := range stages {
		if len(stages[i].List) != 0 {
			program = stages[i].List[0].Text
			break
		}
	}
	fields := []core.RecordField{{Key: FieldStages, Value: core.NewList(a, stages)}, {Key: FieldData, Value: core.NewString(a, b.String())}, {Key: FieldPath, Value: core.NewString(a, program)}}
	payload := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	b.Free()
	return payload
}

func PayloadStages(payload core.Value) []core.Value {
	for i := range payload.Record {
		if payload.Record[i].Key == FieldStages {
			return payload.Record[i].Value.List
		}
	}
	return nil
}

// ConfigureStderr records how a stage's stderr is routed: mode 0 keeps it live,
// 1 writes path (truncating unless appendFile), 2 duplicates the stage stdout,
// and 3 duplicates the presentation stdout a later disposition replaced.
func ConfigureStderr(a mem.Allocator, payload *core.Value, path string, appendFile bool, mode int) {
	fields := []core.RecordField{{Key: FieldError, Value: core.NewString(a, path)}, {Key: FieldErrorAppend, Value: core.Value{Kind: core.Bool, Bool: appendFile}}, {Key: FieldErrorMode, Value: core.Value{Kind: core.Int, Int: int64(mode)}}}
	for i := range fields {
		payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, fields[i].Key).Text, Value: fields[i].Value})
	}
}

// ConfigureRedirections replaces only the wire spelling; argv stays structured.
func ConfigureRedirections(a mem.Allocator, payload *core.Value, input string, output string, appendOutput bool) {
	fields := []core.RecordField{{Key: FieldInput, Value: core.NewString(a, input)}, {Key: FieldOutput, Value: core.NewString(a, output)}, {Key: FieldAppend, Value: core.Value{Kind: core.Bool, Bool: appendOutput}}}
	for i := range fields {
		payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, fields[i].Key).Text, Value: fields[i].Value})
	}
	writeConfiguredGraph(a, payload)
}

func StageSetupPayload(a mem.Allocator, cwd string, timeout int64, environment []core.Value) core.Value {
	fields := []core.RecordField{{Key: FieldCwd, Value: core.NewString(a, cwd)}, {Key: FieldTimeout, Value: core.Value{Kind: core.Int, Int: timeout}}, {Key: FieldEnvironment, Value: core.NewList(a, environment)}}
	value := core.NewRecord(a, fields)
	for i := range fields {
		fields[i].Value.Free(a)
	}
	return value
}

func ConfigureStages(a mem.Allocator, payload *core.Value, setups []core.Value) {
	payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, FieldSetup).Text, Value: core.NewList(a, setups)})
	writeConfiguredGraph(a, payload)
}

func ConfigureTopology(a mem.Allocator, payload *core.Value, kinds []core.Value, edges []core.Value) {
	payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, "kinds").Text, Value: core.NewList(a, kinds)})
	payload.Record = slices.Append(a, payload.Record, core.RecordField{Key: core.NewString(a, "edges").Text, Value: core.NewList(a, edges)})
	writeConfiguredGraph(a, payload)
}

func PayloadSetups(payload core.Value) []core.Value { return PayloadList(payload, FieldSetup) }

func PayloadList(payload core.Value, field string) []core.Value {
	for i := range payload.Record {
		if payload.Record[i].Key == field {
			return payload.Record[i].Value.List
		}
	}
	return nil
}

func PayloadInt(payload core.Value, field string) int64 {
	for i := range payload.Record {
		if payload.Record[i].Key == field {
			return payload.Record[i].Value.Int
		}
	}
	return 0
}

func writeConfiguredGraph(a mem.Allocator, payload *core.Value) {
	b := strings.NewBuilder(a)
	e := json.NewEncoder(&b)
	e.BeginObject()
	e.Str("stages")
	e.BeginArray()
	stages := PayloadStages(*payload)
	for i := range stages {
		e.BeginArray()
		for j := range stages[i].List {
			e.Str(stages[i].List[j].Text)
		}
		e.EndArray()
	}
	e.EndArray()
	kinds := PayloadList(*payload, "kinds")
	if len(kinds) != 0 {
		e.Str("kinds")
		e.BeginArray()
		for i := range kinds {
			e.Str(kinds[i].Text)
		}
		e.EndArray()
		framed := PayloadList(*payload, "framed")
		if len(framed) != 0 {
			e.Str("framed")
			e.BeginArray()
			for i := range framed {
				e.Bool(framed[i].Bool)
			}
			e.EndArray()
		}
		buffers := PayloadList(*payload, "buffers")
		if len(buffers) != 0 {
			e.Str("buffers")
			e.BeginArray()
			for i := range buffers {
				e.Int(buffers[i].Int)
			}
			e.EndArray()
		}
		policyFields := []string{"overflows", "bufferItems", "orders"}
		for _, name := range policyFields {
			values := PayloadList(*payload, name)
			if len(values) != 0 {
				e.Str(name)
				e.BeginArray()
				for i := range values {
					e.Int(values[i].Int)
				}
				e.EndArray()
			}
		}
		for i := range payload.Record {
			if payload.Record[i].Key == "failurePolicy" {
				e.Str("failurePolicy")
				e.Int(payload.Record[i].Value.Int)
			}
		}
		e.Str("edges")
		e.BeginArray()
		edges := PayloadList(*payload, "edges")
		for i := range edges {
			e.BeginArray()
			for j := range edges[i].List {
				e.Int(edges[i].List[j].Int)
			}
			e.EndArray()
		}
		e.EndArray()
	}
	e.Str("input")
	e.Str(PayloadText(*payload, FieldInput))
	if mode := PayloadText(*payload, "inputMode"); mode != "" {
		e.Str("inputMode")
		e.Str(mode)
		if mode == "invocation" {
			e.Str("inputRoot")
			e.Int(PayloadInt(*payload, "inputRoot"))
		}
	}
	e.Str("output")
	e.Str(PayloadText(*payload, FieldOutput))
	e.Str("append")
	e.Bool(PayloadAppend(*payload))
	for i := range payload.Record {
		if payload.Record[i].Key == "stream" {
			e.Str("stream")
			e.Bool(payload.Record[i].Value.Bool)
		}
	}
	for i := range payload.Record {
		if payload.Record[i].Key == "acceptExit" {
			e.Str("acceptExit")
			e.Bool(payload.Record[i].Value.Bool)
		}
	}
	for i := range payload.Record {
		if payload.Record[i].Key != "capture" {
			continue
		}
		// This is a closed, normalized policy, never argv or captured bytes.
		e.Str("capture")
		e.BeginObject()
		policy := payload.Record[i].Value
		e.Str("stdout")
		e.Bool(PayloadBool(policy, "stdout"))
		e.Str("stderr")
		e.Bool(PayloadBool(policy, "stderr"))
		e.Str("stdoutLimit")
		e.Int(PayloadInt(policy, "stdoutLimit"))
		e.Str("stderrLimit")
		e.Int(PayloadInt(policy, "stderrLimit"))
		e.EndObject()
	}
	e.Str("setup")
	e.BeginArray()
	setups := PayloadSetups(*payload)
	for i := range setups {
		e.BeginObject()
		e.Str("acceptExit")
		e.BeginArray()
		accepted := PayloadList(setups[i], "acceptExit")
		for j := range accepted {
			e.Int(accepted[j].Int)
		}
		e.EndArray()
		e.Str(FieldInput)
		e.Str(PayloadText(setups[i], FieldInput))
		e.Str(FieldOutput)
		e.Str(PayloadText(setups[i], FieldOutput))
		e.Str(FieldAppend)
		e.Bool(PayloadAppend(setups[i]))
		e.Str(FieldError)
		e.Str(PayloadText(setups[i], FieldError))
		e.Str(FieldErrorAppend)
		e.Bool(PayloadBool(setups[i], FieldErrorAppend))
		e.Str(FieldErrorMode)
		e.Int(PayloadInt(setups[i], FieldErrorMode))
		e.Str(FieldCwd)
		e.Str(PayloadText(setups[i], FieldCwd))
		e.Str(FieldTimeout)
		e.Int(PayloadInt(setups[i], FieldTimeout))
		e.Str(FieldEnvironment)
		e.BeginArray()
		values := PayloadList(setups[i], FieldEnvironment)
		for j := range values {
			e.Str(values[j].Text)
		}
		e.EndArray()
		e.EndObject()
	}
	e.EndArray()
	e.EndObject()
	e.Flush()
	for i := range payload.Record {
		if payload.Record[i].Key == FieldData {
			payload.Record[i].Value.Free(a)
			payload.Record[i].Value = core.NewString(a, b.String())
		}
	}
	b.Free()
}

// PayloadAppend is the named form of PayloadBool for the output-append field.
func PayloadAppend(payload core.Value) bool { return PayloadBool(payload, FieldAppend) }

// PayloadBool reads an optional boolean field; a missing field is false.
func PayloadBool(payload core.Value, field string) bool {
	for i := range payload.Record {
		if payload.Record[i].Key == field {
			return payload.Record[i].Value.Bool
		}
	}
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
