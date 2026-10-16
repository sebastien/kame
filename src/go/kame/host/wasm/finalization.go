package wasm

import (
	"kame/host"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/slices"
)

// FinalizationTick opts an embedding driver into graceful cleanup and services
// its monotonic deadline. stopping begins disposal; the caller must keep
// servicing requests until this method reports no armed scopes.
func (r *Runtime) FinalizationTick(nowNS int64, stopping bool) bool {
	if r == nil { return false }
	// A stale tick cannot extend a deadline or make armed scopes disappear.
	if nowNS < r.FinalizationNS { nowNS = r.FinalizationNS }
	r.FinalizationEnabled, r.FinalizationNS = true, nowNS
	r.configureFinalization()
	p := r.finalizationProgram()
	if p == nil { return false }
	if stopping { p.BeginFinalization() }
	p.PollFinalizers()
	if r.Session != nil && r.Handle != nil { r.Session.Observe(r.Handle) }
	return p.Finalizing()
}

func (r *Runtime) finalizationProgram() *eval.Program {
	if r.Program != nil { return r.Program.Eval }
	return r.Eval
}

func (r *Runtime) configureFinalization() {
	if !r.FinalizationEnabled { return }
	p := r.finalizationProgram()
	if p == nil { return }
	p.FinalizationNow, p.FinalizationQuiet, p.FinalizationHost = runtimeFinalizationNow, runtimeFinalizationQuiet, r
	if r.Program == nil { p.SetDefinitionEffectSink(runtimeFinalizationEffect, r) }
}

func runtimeFinalizationNow(value any) int64 { r := value.(*Runtime); return r.FinalizationNS }

func runtimeFinalizationQuiet(value any, node int64, generation int64) bool {
	r := value.(*Runtime)
	_ = generation
	for i := range r.FinalizationRequests { if r.FinalizationRequests[i].NodeID == node { return false } }
	return true
}

func runtimeFinalizationEffect(value any, effect eval.Effect) {
	r := value.(*Runtime)
	copy := effect
	copy.Data = slices.Clone(r.Alloc, effect.Data)
	r.Effects = slices.Append(r.Alloc, r.Effects, copy)
}

func (r *Runtime) trackFinalizationRequest(next host.NextResult) {
	if !r.FinalizationEnabled || !next.OK { return }
	request := host.Request{ID: next.Request.ID, NodeID: next.Request.NodeID, Generation: next.Request.Generation, Attempt: next.Request.Attempt, Kind: next.Request.Kind}
	r.FinalizationRequests = slices.Append(r.Alloc, r.FinalizationRequests, request)
}

func (r *Runtime) retireFinalizationRequest(id int64) {
	for i := range r.FinalizationRequests {
		if r.FinalizationRequests[i].ID == id {
			copy(r.FinalizationRequests[i:], r.FinalizationRequests[i+1:])
			r.FinalizationRequests = r.FinalizationRequests[:len(r.FinalizationRequests)-1]
			return
		}
	}
}

func (r *Runtime) FinalizationBudget(node int64) int64 {
	p := r.finalizationProgram()
	if p == nil { return 0 }
	return p.FinalizerBudget(node)
}

func (r *Runtime) FinalizationPending() bool {
	if r == nil { return false }
	p := r.finalizationProgram()
	return p != nil && p.Finalizing()
}

func (r *Runtime) FinalizationTimeout(nowNS int64) bool {
	if r == nil { return false }
	r.FinalizationTick(nowNS, false)
	p := r.finalizationProgram()
	if p == nil { return false }
	p.BeginFinalizationWith(diagnostic.Diagnostic{Code: "RECIPE_TIMEOUT", Severity: diagnostic.Error, Message: "invocation timed out"})
	return p.Finalizing()
}

// ResultDiagnosticJSON copies the terminal diagnostic without consuming output,
// releasing roots or losing the secondary failures recorded during teardown.
func (r *Runtime) ResultDiagnosticJSON() PureResult {
	if r == nil { return PureResult{} }
	node := r.Node
	if r.Program != nil && r.Handle != nil { node = r.Handle.Node }
	if node == nil || node.Diagnostic.Code == "" { return PureResult{} }
	buffer := bytes.NewBuffer(r.Alloc, nil)
	program.WriteJSONDiagnostic(&buffer, node.Diagnostic)
	text := pureText(r.Alloc, buffer.String())
	buffer.Free()
	return PureResult{Text: text}
}
