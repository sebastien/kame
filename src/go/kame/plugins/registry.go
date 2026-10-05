// Package plugins registers explicitly configured host-backed operations.
package plugins

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
)

const (
	DefaultMaxRequestBytes = 1 << 20
	DefaultMaxResponseBytes = 1 << 20
	DefaultTimeoutMS int64 = 30000
)

type Operation struct {
	Name string
	Version string
	MinArity int
	MaxArity int
	Capabilities []eval.Capability
	MaxRequestBytes int
	MaxResponseBytes int
	TimeoutMS int64
}

type Plugin struct {
	Name string
	Version string
	TimeoutMS int64
	MaxRequestBytes int
	MaxResponseBytes int
	Operations []Operation
}

type registration struct {
	Alloc mem.Allocator
	Plugin string
	PluginVersion string
	Operation string
	OperationVersion string
	MinArity int
	MaxArity int
	MaxRequestBytes int
	MaxResponseBytes int
	TimeoutMS int64
}

type callState struct { RequestID int64 }

// Register validates every declaration before mutating the registry. Operation
// calls become correlated RequestPlugin requests; hosts supply one validated
// value or a diagnostic through the usual completion API.
func Register(a mem.Allocator, registry *eval.Registry, declarations []Plugin) bool {
	if registry == nil { return false }
	for i := range declarations {
		p := declarations[i]
		if !validName(p.Name) || p.Version == "" || strings.IndexByte(p.Version, 0) >= 0 || len(p.Operations) == 0 { return false }
		if p.TimeoutMS < 0 || p.TimeoutMS > DefaultTimeoutMS || p.MaxRequestBytes < 0 || p.MaxRequestBytes > DefaultMaxRequestBytes || p.MaxResponseBytes < 0 || p.MaxResponseBytes > DefaultMaxResponseBytes { return false }
		for previous := 0; previous < i; previous++ { if declarations[previous].Name == p.Name { return false } }
		for j := range p.Operations {
			op := p.Operations[j]
			if !validName(op.Name) || op.Version == "" || strings.IndexByte(op.Version, 0) >= 0 || op.MinArity < 0 || (op.MaxArity >= 0 && op.MaxArity < op.MinArity) { return false }
			if op.MaxRequestBytes < 0 || op.MaxRequestBytes > DefaultMaxRequestBytes || op.MaxResponseBytes < 0 || op.MaxResponseBytes > DefaultMaxResponseBytes || op.TimeoutMS < 0 || op.TimeoutMS > DefaultTimeoutMS { return false }
			if (p.MaxRequestBytes != 0 && op.MaxRequestBytes > p.MaxRequestBytes) || (p.MaxResponseBytes != 0 && op.MaxResponseBytes > p.MaxResponseBytes) || (p.TimeoutMS != 0 && op.TimeoutMS > p.TimeoutMS) { return false }
			for k := 0; k < len(op.Capabilities); k++ { if op.Capabilities[k] < eval.Read || op.Capabilities[k] > eval.Env { return false } }
			if registry.HasOperation(op.Name) { return false }
			for pj := 0; pj <= i; pj++ {
				end := len(declarations[pj].Operations)
				if pj == i { end = j }
				for pk := 0; pk < end; pk++ { if declarations[pj].Operations[pk].Name == op.Name { return false } }
			}
		}
	}
	for i := range declarations {
		p := declarations[i]
		for j := range p.Operations {
			op := p.Operations[j]
			maxRequest, maxResponse := effectiveLimit(op.MaxRequestBytes, p.MaxRequestBytes, DefaultMaxRequestBytes), effectiveLimit(op.MaxResponseBytes, p.MaxResponseBytes, DefaultMaxResponseBytes)
			timeout := effectiveTimeout(op.TimeoutMS, p.TimeoutMS)
			descriptor := mem.Alloc[registration](a)
			descriptor.Alloc = a
			descriptor.Plugin, descriptor.PluginVersion = copyText(a, p.Name), copyText(a, p.Version)
			descriptor.Operation, descriptor.OperationVersion = copyText(a, op.Name), copyText(a, op.Version)
			descriptor.MinArity, descriptor.MaxArity = op.MinArity, op.MaxArity
			descriptor.MaxRequestBytes, descriptor.MaxResponseBytes, descriptor.TimeoutMS = maxRequest, maxResponse, timeout
			version := copyText(a, p.Version+"/"+op.Version)
			added := registry.Add(eval.Operation{Name: op.Name, Call: invoke, Context: descriptor, FreeContext: freeRegistration, MinArity: op.MinArity, MaxArity: op.MaxArity, Capabilities: op.Capabilities, Version: version})
			mem.FreeString(a, version)
			if !added { freeRegistration(a, descriptor); return false }
		}
	}
	return true
}

func validName(name string) bool { return name != "" && strings.IndexByte(name, 0) < 0 }

func effectiveLimit(operation int, plugin int, defaultLimit int) int {
	if operation != 0 { return operation }
	if plugin != 0 { return plugin }
	return defaultLimit
}

func effectiveTimeout(operation int64, plugin int64) int64 {
	if operation != 0 { return operation }
	if plugin != 0 { return plugin }
	return DefaultTimeoutMS
}

func copyText(a mem.Allocator, text string) string {
	if text == "" { return "" }
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

func freeRegistration(a mem.Allocator, value any) {
	state := value.(*registration)
	mem.FreeString(a, state.Plugin)
	mem.FreeString(a, state.PluginVersion)
	mem.FreeString(a, state.Operation)
	mem.FreeString(a, state.OperationVersion)
	mem.Free(a, state)
}

func freeCallState(a mem.Allocator, value any) { mem.Free(a, value.(*callState)) }

func invoke(c *eval.Context, value any, args []core.Value) eval.Result {
	state := value.(*registration)
	if stored := c.OperationState(); stored != nil {
		call := stored.(*callState)
		if c.Completion().RequestID != call.RequestID { return eval.Result{Waiting: true} }
		completion := c.TakeCompletion()
		c.ClearOperationState()
		if completion.Diagnostic.Code != "" {
			result := eval.Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
			completion.Diagnostic.Free(c.Run)
			completion.Value.Free(c.Run)
			return result
		}
		if !completion.HasValue {
			completion.Value.Free(c.Run)
			return pluginFailure(c, "PLUGIN_PROTOCOL", "plugin host omitted its result")
		}
		encoded := core.PluginValueJSON(c.Run, completion.Value)
		completion.Value.Free(c.Run)
		if encoded == nil { return pluginFailure(c, "PLUGIN_PROTOCOL", "plugin returned an unsupported value") }
		if len(encoded) > state.MaxResponseBytes { mem.FreeSlice(c.Run, encoded); return pluginFailure(c, "PLUGIN_LIMIT", "plugin result exceeds its byte limit") }
		var result core.Value
		valid := core.ParsePluginValueJSON(c.Run, encoded, &result)
		mem.FreeSlice(c.Run, encoded)
		if !valid { return pluginFailure(c, "PLUGIN_PROTOCOL", "plugin returned an invalid value") }
		return eval.Result{Value: result}
	}
	requestBytes := 0
	for i := range args {
		encoded := core.PluginValueJSON(c.Run, args[i])
		if encoded == nil { return pluginFailure(c, "PLUGIN_PROTOCOL", "plugin arguments must be serializable Kame values") }
		requestBytes += len(encoded)
		mem.FreeSlice(c.Run, encoded)
		if requestBytes > state.MaxRequestBytes { return pluginFailure(c, "PLUGIN_LIMIT", "plugin request exceeds its byte limit") }
	}
	payload := host.PluginInvocationPayload(c.Run, state.Plugin, state.PluginVersion, state.Operation, state.OperationVersion, state.MaxRequestBytes, state.MaxResponseBytes, state.TimeoutMS, args)
	id := c.Submit(host.RequestPlugin, payload)
	payload.Free(c.Run)
	if id == 0 { return pluginFailure(c, "FEATURE_UNSUP", "plugin host request is unavailable") }
	call := mem.Alloc[callState](c.Program.Alloc)
	call.RequestID = id
	c.SetOperationState(call, freeCallState)
	return eval.Result{Waiting: true}
}

func pluginFailure(c *eval.Context, code string, message string) eval.Result {
	return eval.Result{Diagnostic: diagnostic.Diagnostic{Code: copyText(c.Run, code), Severity: diagnostic.Error, Message: copyText(c.Run, message), Span: diagnostic.Span{Start: c.Span.Start, End: c.Span.End}, Owned: true}}
}
