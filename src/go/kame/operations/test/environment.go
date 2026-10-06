package lib_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestBoundEnvironmentReadsRespectSnapshotAndGrants(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "test", "(env \"MODE\")")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, HasEnvironment: true, Environment: []string{"MODE=scoped=with-equals", "OTHER=value"}, Grants: []eval.Grant{{Capability: eval.Env, Names: []string{"MODE"}}}}
	result := program.EvaluateWith(document.Expr, &context)
	if result.Waiting || result.Diagnostic.Code != "" || result.Value.Text != "scoped=with-equals" {
		t.Error("bound environment did not return its owned value without a host request")
	}
	result.Free(a)
	context.Environment = []string{"OTHER=value"}
	result = program.EvaluateWith(document.Expr, &context)
	if result.Waiting || result.Diagnostic.Code != "" || result.Value.Kind != core.Nil {
		t.Error("missing bound name did not return nil")
	}
	result.Free(a)
	context.Environment = nil
	result = program.EvaluateWith(document.Expr, &context)
	if result.Waiting || result.Diagnostic.Code != "" || result.Value.Kind != core.Nil {
		t.Error("empty bound snapshot fell through to ambient host requests")
	}
	result.Free(a)
	context.Environment = []string{"MODE=secret"}
	context.Grants = nil
	result = program.EvaluateWith(document.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" {
		t.Error("bound snapshot bypassed environment grants")
	}
	result.Free(a)
	document.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

type observedEnvironmentState struct {
	Program *eval.Program
	Expression *expr.Expr
	Environment []string
	Allowed bool
	Unbound bool
}

func evaluateObservedEnvironment(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*observedEnvironmentState)
	context := eval.Context{Program: state.Program, Scope: state.Program.Scope, Run: c.Allocator(), Engine: c, HasEnvironment: !state.Unbound, Environment: state.Environment, DirectHostRequests: state.Unbound, Grants: []eval.Grant{{Capability: eval.Env, Names: []string{"MODE"}}}}
	if !state.Allowed { context.Grants = nil }
	result := state.Program.EvaluateWith(state.Expression, &context)
	if result.Diagnostic.Code != "" { c.Fail(result.Diagnostic); result.Value.Free(c.Allocator()); return core.ProducerFailed }
	if result.Waiting { return core.ProducerWaiting }
	c.Publish(result.Value)
	return core.ProducerCompleted
}

func TestBoundEnvironmentObservesOnlyConsumedNames(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "test", "(env \"MODE\")")
	state := mem.Alloc[observedEnvironmentState](a)
	state.Program, state.Expression, state.Allowed = program, document.Expr, true
	state.Environment = []string{"MODE=scoped=with-equals", "UNREAD=first"}
	node := engine.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "environment"}, evaluateObservedEnvironment, state)
	engine.Request(node); engine.Step()
	if !node.Current { t.Errorf("environment producer state: %d", node.State) }
	if node.Diagnostic.Code != "" { t.Error(node.Diagnostic.Code) }
	if len(node.Observations) != 2 { t.Error("expected one operation and one consumed environment observation") }
	var first core.Signature
	var operation core.Signature
	for i := range node.Observations {
		observation := node.Observations[i]
		if observation.Key.Kind == core.ResourceEnvironment {
			if observation.Key.Name != "MODE" { t.Error("unread environment name was observed") }
			first = observation.Signature
		}
		if observation.Key.Kind == core.ResourceOperation {
			if observation.Key.Name != "env" { t.Error("unexecuted operation was observed") }
			operation = observation.Signature
		}
	}
	if operation.Mode != core.SignatureContent { t.Error("consumed operation version was not observed") }
	if !first.Equal(core.ValueSignature(core.Value{Kind: core.String, Text: "scoped=with-equals"})) { t.Error("consumed bound value was not observed") }
	state.Environment = []string{"MODE=scoped=with-equals", "UNREAD=second"}
	engine.Invalidate(node); engine.Step()
	var second core.Signature
	for i := range node.Observations { if node.Observations[i].Key.Kind == core.ResourceEnvironment { second = node.Observations[i].Signature } }
	if !second.Equal(first) { t.Error("unread environment value changed observed identity") }
	state.Environment = nil
	engine.Invalidate(node); engine.Step()
	var missing core.Signature
	for i := range node.Observations { if node.Observations[i].Key.Kind == core.ResourceEnvironment { missing = node.Observations[i].Signature } }
	if !missing.Equal(core.ValueSignature(core.Value{Kind: core.Nil})) || missing.Equal(first) { t.Error("absent consumed name has incorrect identity") }
	state.Allowed = false
	engine.Invalidate(node); engine.Step()
	for i := range node.Observations { if node.Observations[i].Key.Kind == core.ResourceEnvironment { t.Error("denied environment read observed a value") } }
	if node.Diagnostic.Code != "CAP_DENIED" { t.Error("observation bypassed capability checks") }
	engine.Free()
	program.Free(); parsed.Free(); registry.Free(); document.Free()
	mem.Free(a, state)
}

func TestHostEnvironmentCompletionRecordsConsumedValue(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "test", "(env \"MODE\")")
	state := mem.Alloc[observedEnvironmentState](a)
	state.Program, state.Expression, state.Allowed, state.Unbound = program, document.Expr, true, true
	node := engine.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "environment"}, evaluateObservedEnvironment, state)
	engine.Request(node); engine.Step()
	next := program.Requests.Next()
	if !next.OK || next.Request.Kind != host.RequestEnvironment || next.Request.Payload.Text != "MODE" { t.Error("unbound environment did not request its consumed name") }
	if next.OK {
		request := next.Request
		engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, HasValue: true, Value: core.NewString(a, "host-value")})
		next.Request.Free(a)
		engine.Step(); engine.Step()
	}
	var observed core.Signature
	for i := range node.Observations { if node.Observations[i].Key.Kind == core.ResourceEnvironment { observed = node.Observations[i].Signature } }
	if !observed.Equal(core.ValueSignature(core.Value{Kind: core.String, Text: "host-value"})) || !node.Current || node.Latest.Text != "host-value" { t.Error("host completion lost its consumed environment identity") }
	engine.Free()
	program.Free(); parsed.Free(); registry.Free(); document.Free()
	mem.Free(a, state)
}
