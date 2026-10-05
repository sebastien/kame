package wasm

import (
	"kame/plugins"
	"solod.dev/so/mem"
)

// RegisterPluginsJSON installs explicit operation declarations before the
// runtime starts evaluating roots. It is also used by the JavaScript callback
// adapter to keep declaration validation in the portable runtime.
func (r *Runtime) RegisterPluginsJSON(data []byte) PureResult {
	a := mem.System
	if r != nil { a = r.Alloc }
	if r == nil || r.Registry == nil || r.Program != nil || r.Session != nil || r.Node != nil {
		return PureResult{Code: pureText(a, "PHASE_INVALID"), Message: pureText(a, "plugin declarations must be registered before evaluation")}
	}
	if len(data) > 1<<20 || !plugins.RegisterJSON(r.Alloc, r.Registry, data) {
		return PureResult{Code: pureText(r.Alloc, "PLUGIN_CONFIG"), Message: pureText(r.Alloc, "invalid or duplicate plugin declarations")}
	}
	return PureResult{}
}
