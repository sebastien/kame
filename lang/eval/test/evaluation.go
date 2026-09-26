package eval_test

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lang/eval"
	"littlemake/lang/expr"
	"littlemake/host"
	"littlemake/lang/script"
	"littlemake/operations"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

type operationState struct { Calls int }
type fileState struct { Alloc mem.Allocator; Text string }
type streamState struct { Alloc mem.Allocator; Next int }

func readOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = context, args
	state := value.(*operationState)
	state.Calls++
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func countOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = context, args
	state := value.(*operationState)
	state.Calls++
	return eval.Result{Value: core.Value{Kind: core.Int, Int: int64(state.Calls)}}
}

func asyncOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	if context.Completion().RequestID == 0 {
		if context.Submit(host.RequestCustom, core.Value{Kind: core.Nil}) == 0 { return eval.Result{Diagnostic: core.Diagnostic{Code: "HOST_FAIL"}} }
		return eval.Result{Waiting: true}
	}
	return eval.Result{Value: core.Value{Kind: core.Int, Int: 42}}
}

func readFileOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	payload := core.NewString(context.Run, "../outside.txt")
	id := context.Submit(host.RequestReadFile, payload)
	payload.Free(context.Run)
	if id == 0 { return eval.Result{Waiting: true} }
	return eval.Result{Waiting: true}
}

func recordReadFileOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	payload := host.FilePayload(context.Run, host.OpRead, "../outside.txt")
	id := context.Submit(host.RequestReadFile, payload)
	payload.Free(context.Run)
	if id == 0 { return eval.Result{Waiting: true} }
	return eval.Result{Waiting: true}
}

func customReadOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	if context.Submit(host.RequestCustom, core.Value{Kind: core.Nil}) == 0 { return eval.Result{Waiting: true} }
	return eval.Result{Waiting: true}
}

func streamOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	state := mem.Alloc[streamState](context.Run)
	state.Alloc = context.Run
	return eval.Result{Stream: core.NewSource(context.Run, pollOperationValues, freeOperationValues, state)}
}

func pollOperationValues(context *core.EngineContext, source *core.Source, atom *core.Atom) core.PollResult {
	_ = context
	state := source.State.(*streamState)
	if state.Next == 0 { state.Next++; atom.Kind, atom.Value = core.AtomValue, core.NewString(state.Alloc, "first"); return core.PollEmitted }
	if state.Next == 1 { state.Next++; atom.Kind, atom.Value = core.AtomValue, core.NewString(state.Alloc, "second"); return core.PollEmitted }
	atom.Kind = core.AtomEndStream
	return core.PollEmitted
}

func freeOperationValues(source *core.Source) { state := source.State.(*streamState); mem.Free(state.Alloc, state) }

func completeOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _, _ = context, value, args
	return eval.Result{Completed: true}
}

func fileProducer(context *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := context.Context().(*fileState)
	context.Publish(core.NewString(state.Alloc, state.Text))
	return core.ProducerCompleted
}

func dependencyOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_ = args
	state := value.(*fileState)
	key := core.ResourceKey{Kind: core.ResourceFile, Name: "./input.txt"}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	current := context.Value(key)
	if !current.OK { return eval.Result{Waiting: true} }
	return eval.Result{Value: current.Value.Clone(state.Alloc)}
}

func evaluate(t *testing.T, program *eval.Program, text string) eval.Result {
	parsed := expr.Parse(t.Allocator(), "test", text)
	if len(parsed.Diagnostics) != 0 { t.Error("parse failed"); parsed.Free(); return eval.Result{} }
	result := program.Evaluate(t.Allocator(), parsed.Expr, program.Scope)
	parsed.Free()
	return result
}

func TestEvaluatesContainersReferencesAndSpecialForms(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [item [name: \"littlemake\" count: 2]] item.name)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "littlemake" { t.Error("record reference was not evaluated") }
	result.Free(a)
	result = evaluate(t, program, "(? missing :nil)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Nil { t.Error("fallback did not suppress missing reference") }
	result.Free(a)
	result = evaluate(t, program, "(? (let [only-name] only-name) missing)")
	if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("fallback suppressed a non-missing failure") }
	result.Free(a)
	result = evaluate(t, program, "(let [local \"value\"] local)")
	result.Free(a)
	result = evaluate(t, program, "local")
	if result.Diagnostic.Code != "REF_MISSING" { t.Error("let binding leaked into its parent scope") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestLexicalFunctionAndOperationShadowing(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(( [x] x) \"ok\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "ok" { t.Error("lambda call failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestLetBoundLambdaReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [f ([x] x)] \"x\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" { t.Error("let expression failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestEscapedLetLambdaRetainsThenReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [f ([x] x)] f)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Callable { t.Error("let lambda did not escape") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestLetLambdaAliasReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [f ([x] x) g f] \"x\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" { t.Error("let lambda alias failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

// Leak checks ride on t.Allocator's Tracker: each test below frees its
// result, engine, program, and registry, so a leaked Scope, wrapper, or
// Native state fails the test through malloc/free mismatch.

func TestSectionInLetReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Error("library registration failed") }
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [s ((list _0 _1))] \"x\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" { t.Error("let section failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestDefInsideLetReleasesItsFunction(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [] (def foo [x] x) \"x\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" { t.Error("def inside let failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestNativeReplaceSectionInLetReleasesItsState(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Error("library registration failed") }
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [r (replace ./src/{n:*}.c ./build/{n}.o)] \"x\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" { t.Error("native section let failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestListErrorDiscardsCallableScopes(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "[([x] x) missing]")
	if result.Diagnostic.Code != "REF_MISSING" { t.Error("list error did not fail") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestRecordErrorDiscardsCallableScopes(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "[f: ([x] x) bad: missing]")
	if result.Diagnostic.Code != "REF_MISSING" { t.Error("record error did not fail") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestCallErrorDiscardsCallableArgs(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(([f] f) ([x] x) missing)")
	if result.Diagnostic.Code != "REF_MISSING" { t.Error("call error did not fail") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestDefinitionIsLazyAndPublishesTemplateValue(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "name = \"littlemake\"")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("name")
	if node == nil || node.Current { t.Error("definition started during compilation") }
	engine.Request(node)
	engine.Step()
	if !node.Current || node.Latest.Kind != core.String || node.Latest.Text != "littlemake" { t.Error("lazy definition did not publish its rendered value") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestDefinitionDependenciesAreSharedByTheEngine(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "first = \"little\"\nsecond = \"@(first)make\"")
	program := eval.Compile(a, engine, parsed, registry)
	engine.Request(program.Definition("second"))
	engine.Step()
	engine.Step()
	engine.Step()
	node := program.Definition("second")
	if !node.Current || node.Latest.Text != "littlemake" { t.Error("definition dependency did not resume and render") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestCapabilitiesBlockOperationsBeforeInvocation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	state := &operationState{}
	if !registry.Add(eval.Operation{Name: "read-one", Call: readOperation, Context: state, MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Read}}) { t.Error("operation registration failed") }
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(read-one)")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" || len(result.Diagnostic.Frames) != 1 || state.Calls != 0 { t.Error("denied operation was invoked without an operation frame") }
	result.Free(a); expression.Free()
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestReferenceSlicesAndRuleSelectors(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [items [\"a\" \"b\" \"c\"]] items.-1)")
	if result.Diagnostic.Code != "" || result.Value.Text != "c" { t.Error("negative list index failed") }
	result.Free(a)
	expression := expr.Parse(a, "test", "@>")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Outputs: []core.Value{core.NewString(a, "out")}}
	result = program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "" || result.Value.Text != "out" { t.Error("rule output selector failed") }
	result.Free(a); context.Outputs[0].Free(a); expression.Free()
	expression = expr.Parse(a, "test", "@>1")
	context.Outputs = []core.Value{core.NewString(a, "first"), core.NewString(a, "second")}
	result = program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "" || result.Value.Text != "second" { t.Error("indexed rule selector failed") }
	result.Free(a); context.Outputs[0].Free(a); context.Outputs[1].Free(a); expression.Free()
	result = evaluate(t, program, "(let [item [name: \"littlemake\" count: 2]] item.{name,count})")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Record || result.Value.Record[0].Value.Text != "littlemake" || result.Value.Record[1].Value.Int != 2 { t.Error("record selection failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestLiftedDefinitionReevaluatesAfterDependencyInvalidation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	state := &operationState{}
	registry.Add(eval.Operation{Name: "count-op", Call: countOperation, Context: state, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "test", "first = \"input\"\nsecond = (count-op first)")
	program := eval.Compile(a, engine, parsed, registry)
	first, second := program.Definition("first"), program.Definition("second")
	engine.Request(second)
	for i := 0; i < 5; i++ { engine.Step() }
	if !second.Current || second.Latest.Int != 1 { t.Error("lifted definition did not publish initial operation value") }
	engine.Invalidate(first)
	for i := 0; i < 5; i++ { engine.Step() }
	if !second.Current || second.Latest.Int != 2 || state.Calls != 2 { t.Error("lifted definition did not reevaluate after dependency changed") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestOperationCanSubmitAndResumeFromHostCompletion(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "result = (async)")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	engine.Step()
	request := program.Requests.Next()
	if !node.Submitted || !request.OK || request.Request.NodeID != node.ID || request.Request.ID != node.HostRequestID { t.Error("operation did not queue correlated host work") }
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt + 1, RequestID: request.Request.ID})
	engine.Step()
	if node.Current || !node.Submitted { t.Error("stale operation completion resumed evaluation") }
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	engine.Step()
	engine.Step()
	if !node.Current || node.Latest.Int != 42 { t.Error("operation did not resume from completion") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestOperationCanPublishAStream(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "stream", Call: streamOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "result = (stream)")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	for i := 0; i < 5; i++ { engine.Step() }
	if !node.Current || node.Latest.Text != "second" || node.State != core.NodeWaiting { t.Error("operation stream did not publish and return to dependency waiting") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestOperationCanCompleteWithoutAValue(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "complete", Call: completeOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "result = (complete)")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	engine.Step()
	if node.Current || node.State != core.NodeWaiting { t.Error("valueless operation completion published a value") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestDependencyUpdateCancelsStaleOperationAndRestarts(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "test", "input = \"first\"\nresult = (async input)")
	program := eval.Compile(a, engine, parsed, registry)
	input, result := program.Definition("input"), program.Definition("result")
	engine.Request(result)
	for i := 0; i < 3; i++ { engine.Step() }
	first := program.Requests.Next()
	if !first.OK { t.Error("initial operation did not submit") }
	engine.Invalidate(input)
	for i := 0; i < 4; i++ { engine.Step() }
	cancellation := engine.NextCancellation()
	second := program.Requests.Next()
	if cancellation.RequestID != first.Request.ID || !second.OK || second.Request.ID == first.Request.ID { t.Error("dependency update did not cancel and restart operation") }
	engine.Complete(core.Completion{NodeID: first.Request.NodeID, Generation: first.Request.Generation, Attempt: first.Request.Attempt, RequestID: first.Request.ID})
	engine.Complete(core.Completion{NodeID: second.Request.NodeID, Generation: second.Request.Generation, Attempt: second.Request.Attempt, RequestID: second.Request.ID})
	first.Request.Free(a); second.Request.Free(a)
	for i := 0; i < 3; i++ { engine.Step() }
	if !result.Current || result.Latest.Int != 42 { t.Error("restarted operation did not publish") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestNestedOperationResumesThroughFunctionCall(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "(wrap value) = (async)\nresult = (wrap \"value\")")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	engine.Step()
	request := program.Requests.Next()
	if !request.OK { t.Error("nested operation did not submit host work") }
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	engine.Step(); engine.Step()
	if !node.Current || node.Latest.Int != 42 { t.Error("nested operation did not resume through function call") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestCapabilityRootsUseWholePathSegments(t *testing.T) {
	context := eval.Context{Run: t.Allocator(), Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"../src"}}}}
	if !context.Allows(eval.Read, "/workspace/src/main.lm") { t.Error("granted path was denied") }
	if context.Allows(eval.Read, "/workspace/src-old/main.lm") { t.Error("prefix sibling escaped capability root") }
	if context.Allows(eval.Read, "../../secret.txt") { t.Error("traversal escaped capability root") }
}

func TestRestrictedHostRequestIsDeniedBeforeQueueing(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "read-file", Call: readFileOperation, MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Read}})
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(read-file)")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"./allowed"}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" || program.Requests.Next().OK { t.Error("restricted host request was queued or not denied") }
	result.Free(a); expression.Free()
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestRestrictedRecordHostRequestUsesPayloadPath(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "read-file", Call: recordReadFileOperation, MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Read}})
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(read-file)")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"./allowed"}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" { t.Error("restricted record request escaped capability root") }
	result.Free(a); expression.Free()
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestRestrictedCustomRequestIsDeniedBeforeQueueing(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "custom-read", Call: customReadOperation, MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Read}})
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(custom-read)")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"./allowed"}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" || program.Requests.Next().OK { t.Error("restricted custom request was queued or not denied") }
	result.Free(a); expression.Free()
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestEvalRetainsParserDiagnostic(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(eval \"(\")")
	if result.Diagnostic.Code != "PARSE_ERR" || result.Diagnostic.Span.End == 0 || result.Diagnostic.Message == "invalid eval expression" { t.Error("eval discarded its parser diagnostic") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestEvaluationContextAttachesDiagnosticSource(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "input", "missing")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Source: "input"}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "REF_MISSING" || result.Diagnostic.Target != "input" { t.Error("evaluation diagnostic lacked its source") }
	result.Free(a); expression.Free()
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestTaskKeyIncludesOperationVersion(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "compile", Version: "v2", Call: countOperation, MinArity: 0, MaxArity: 0})
	key := registry.TaskKey(a, "app", "compile")
	if !key.OK || key.Key.Kind != core.ResourceTask || key.Key.Name != "app\x00compile\x00v2" { t.Error("task key omitted operation version") }
	key.Key.Free(a); registry.Free()
}

func TestSelectorsUseNearestRuleFrame(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "@>")
	outer := core.NewString(a, "outer")
	inner := core.NewString(a, "inner")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, RuleFrames: []eval.RuleFrame{{Outputs: []core.Value{outer}}, {Outputs: []core.Value{inner}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "" || result.Value.Text != "inner" { t.Error("selector did not use nearest rule frame") }
	result.Free(a); outer.Free(a); inner.Free(a); expression.Free()
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestResultFreeReleasesReturnedLambda(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "([value] value)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Callable { t.Error("lambda evaluation failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestTemplateRejectsRecordInterpolation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(let [value [name: \"littlemake\"]] \"{(value)}\")")
	if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("record interpolation did not fail") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestDuplicateDefinitionsAreDiagnosedWithoutReplacingFirst(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "name = \"first\"\nname = \"second\"")
	program := eval.Compile(a, engine, parsed, registry)
	if program.Valid || len(program.Diagnostics) != 1 || program.Diagnostics[0].Code != "DEF_INVALID" { t.Error("duplicate definition was not diagnosed") }
	if program.Definition("name") != nil { t.Error("invalid evaluator program exposed executable definitions") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestCheckedCompileRejectsInvalidProgramBeforeRegistration(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "name = \"first\"\nname = \"second\"")
	result := eval.CompileChecked(a, engine, parsed, registry)
	if result.Program != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "DEF_INVALID" { t.Error("checked compile returned an executable invalid program") }
	result.Free(a)
	engine.Free(); parsed.Free(); registry.Free()
}

func TestDefinitionCycleFailsThroughEngine(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "a = \"@(b)\"\nb = \"@(a)\"")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("a")
	engine.Request(node)
	for i := 0; i < 6; i++ { engine.Step() }
	if node.State != core.NodeFailed || node.Diagnostic.Code != "DEP_CYCLE" { t.Error("definition cycle did not fail") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestDefinitionCannotPublishCallable(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "callable = ([value] value)")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("callable")
	engine.Request(node)
	engine.Step()
	if node.State != core.NodeFailed || node.Diagnostic.Code != "EXPR_INVALID" { t.Error("callable definition crossed engine boundary") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestFunctionDefinitionsRenderWordAndTemplateRHS(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "(prefix value) = pre-@{value}\n(quoted value) = \"@{value}!\"")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(prefix \"fix\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "pre-fix" { t.Error("word function RHS was not rendered") }
	result.Free(a)
	result = evaluate(t, program, "(quoted \"ok\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "ok!" { t.Error("template function RHS was not rendered") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestZeroArgumentScriptFunctionIsCallable(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "(constant) = \"value\"")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(constant)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "value" { t.Error("zero-argument script function was not callable") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestDefFunctionSupportsRestAndRejectsDuplicates(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(def collect [first rest...] rest)")
	if result.Diagnostic.Code != "" { t.Error("def rest parameters were rejected") }
	result.Free(a)
	result = evaluate(t, program, "(collect \"first\" \"second\" \"third\")")
	if result.Diagnostic.Code != "" { t.Errorf("def rest call failed: %s", result.Diagnostic.Code)
	} else if result.Value.Kind != core.List { t.Error("def rest parameter was not a list")
	} else if len(result.Value.List) != 2 { t.Error("def rest parameter had the wrong length")
	} else if result.Value.List[0].Text != "second" || result.Value.List[1].Text != "third" { t.Error("def rest parameter had wrong values") }
	result.Free(a)
	result = evaluate(t, program, "(def duplicate [value value] value)")
	if result.Diagnostic.Code != "DEF_INVALID" { t.Error("duplicate def parameters were accepted") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestReturnedClosureRetainsCallScopeAndNestedCallRestoresScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((([outer] ([inner] outer)) \"captured\") \"ignored\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "captured" { t.Error("returned closure lost its call scope") }
	result.Free(a)
	result = evaluate(t, program, "(([outer] (([inner] inner) \"inner\") outer) \"outer\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "outer" { t.Error("nested call left a stale scope") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestEvaluationContextFramesAndDefinitionCycleFrames(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "missing")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Frames: []diagnostic.Frame{{Label: "rule"}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "REF_MISSING" || len(result.Diagnostic.Frames) != 1 || result.Diagnostic.Frames[0].Label != "rule" { t.Error("evaluation context frames were not attached") }
	result.Free(a); expression.Free()
	engine.Free(); program.Free(); parsed.Free(); registry.Free()

	engine = core.NewEngine(a)
	registry = eval.NewRegistry(a)
	parsed = script.Parse(a, "test", "a = \"@(b)\"\nb = \"@(a)\"")
	program = eval.Compile(a, engine, parsed, registry)
	node := program.Definition("a")
	engine.Request(node)
	for i := 0; i < 6; i++ { engine.Step() }
	if node.Diagnostic.Code != "DEP_CYCLE" || len(node.Diagnostic.Frames) == 0 { t.Error("definition cycle lacked definition frames") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestNestedFunctionSelectorsUseNearestArguments(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "(inner value) = @_\n(outer value) = (inner \"inner\")")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(outer \"outer\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "inner" { t.Error("nested function selector did not use nearest arguments") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestOperationRegistersAndReadsDynamicFileDependency(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	file := mem.Alloc[fileState](a)
	file.Alloc, file.Text = a, "file-data"
	engine.AddOwned(core.ResourceKey{Kind: core.ResourceFile, Name: "./input.txt"}, fileProducer, file, freeFileState)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "read-file", Call: dependencyOperation, Context: file, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "result = (read-file)")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	for i := 0; i < 4; i++ { engine.Step() }
	if !node.Current || node.Latest.Text != "file-data" { t.Error("dynamic file dependency did not resume evaluation") }
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func freeFileState(a mem.Allocator, value any) { mem.Free(a, value.(*fileState)) }

func TestPlaceholderSectionsEvaluateAsLambdas(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Error("library registration failed") }
	parsed := script.Parse(a, "test", "(helper x) = (nop x)")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(((([x] x) _0)) 7)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 7 { t.Error("identity section failed") }
	result.Free(a)
	result = evaluate(t, program, "(((([a b] (list a b)) _ __)) \"one\" \"two\")")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "two" { t.Error("multi-placeholder section failed") }
	result.Free(a)
	result = evaluate(t, program, "(((list _ _)) \"same\")")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 { t.Error("repeated placeholder did not reuse its argument") }
	result.Free(a)
	result = evaluate(t, program, "(((([x] x) _0)))")
	if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("section accepted too few arguments") }
	result.Free(a)
	result = evaluate(t, program, "(((([x] x) _0)) 1 2)")
	if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("section accepted too many arguments") }
	result.Free(a)
	result = evaluate(t, program, "_0")
	if result.Diagnostic.Code != "REF_MISSING" { t.Error("placeholder outside a section stayed a name") }
	result.Free(a)
	result = evaluate(t, program, "(((helper _0)) \"bound\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "bound" { t.Error("section over a defined function failed") }
	result.Free(a)
	result = evaluate(t, program, "(((list (nth _0 0) _1)) [\"x\"] \"y\")")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[0].Text != "x" || result.Value.List[1].Text != "y" { t.Error("section placeholders did not descend into nested calls") }
	result.Free(a)
	result = evaluate(t, program, "(map ((nth _0 0)) (list [\"a\" \"b\"] [\"c\"]))")
	if result.Diagnostic.Code != "" || result.Value.List[0].Text != "a" || result.Value.List[1].Text != "c" { t.Error("nested section callback through map failed") }
	result.Free(a)
	result = evaluate(t, program, "(((nop _0)) 7)")
	if result.Diagnostic.Code != "" || result.Value.Int != 7 { t.Error("underscore identity section failed") }
	result.Free(a)
	result = evaluate(t, program, "(((([x] x) _0)) 7)")
	if result.Diagnostic.Code != "" || result.Value.Int != 7 { t.Error("digit identity section failed") }
	result.Free(a)
	result = evaluate(t, program, "(([x] x) 7)")
	if result.Diagnostic.Code != "" || result.Value.Int != 7 { t.Error("explicit lambda failed") }
	result.Free(a)
	result = evaluate(t, program, "(apply ((list _0 _1)) [\"a\" \"b\"])")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "b" { t.Error("section through apply failed") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestPatternValuesMaterializeAsText(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "./{**}/{*}.c")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Pattern || result.Value.Text != "./{**}/{*}.c" { t.Error("path pattern did not materialize") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestPatternStringValuesMaterializeAsText(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "\"./{name:*}.c\"")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Pattern { t.Error("string pattern did not materialize") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}

func TestPlainPathValuesMaterializeAsText(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "./src/main.c")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String { t.Error("plain path stopped materializing as a string") }
	result.Free(a)
	engine.Free(); program.Free(); parsed.Free(); registry.Free()
}
