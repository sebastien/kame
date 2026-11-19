package evalcheck

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
)

type operationState struct{ Calls int }
type fileState struct {
	Alloc mem.Allocator
	Text  string
}
type streamState struct {
	Alloc mem.Allocator
	Next  int
}

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
		if context.Submit(host.RequestCustom, core.Value{Kind: core.Nil}) == 0 {
			return eval.Result{Diagnostic: core.Diagnostic{Code: "HOST_FAIL"}}
		}
		return eval.Result{Waiting: true}
	}
	return eval.Result{Value: core.Value{Kind: core.Int, Int: 42}}
}

func readFileOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	payload := core.NewString(context.Run, "../outside.txt")
	id := context.Submit(host.RequestReadFile, payload)
	payload.Free(context.Run)
	if id == 0 {
		return eval.Result{Waiting: true}
	}
	return eval.Result{Waiting: true}
}

func loadSourceOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	payload := host.FilePayload(context.Run, host.OpRead, "/outside.kash")
	context.Submit(host.RequestLoadSource, payload)
	payload.Free(context.Run)
	return eval.Result{Waiting: true}
}

func recordReadFileOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	payload := host.FilePayload(context.Run, host.OpRead, "../outside.txt")
	id := context.Submit(host.RequestReadFile, payload)
	payload.Free(context.Run)
	if id == 0 {
		return eval.Result{Waiting: true}
	}
	return eval.Result{Waiting: true}
}

func customReadOperation(context *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = value, args
	if context.Submit(host.RequestCustom, core.Value{Kind: core.Nil}) == 0 {
		return eval.Result{Waiting: true}
	}
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
	if state.Next == 0 {
		state.Next++
		atom.Kind, atom.Value = core.AtomValue, core.NewString(state.Alloc, "first")
		return core.PollEmitted
	}
	if state.Next == 1 {
		state.Next++
		atom.Kind, atom.Value = core.AtomValue, core.NewString(state.Alloc, "second")
		return core.PollEmitted
	}
	atom.Kind = core.AtomEndStream
	return core.PollEmitted
}

func freeOperationValues(source *core.Source) {
	state := source.State.(*streamState)
	mem.Free(state.Alloc, state)
}

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
	if !context.Dependency(key) {
		return eval.Result{Waiting: true}
	}
	current := context.Value(key)
	if !current.OK {
		return eval.Result{Waiting: true}
	}
	return eval.Result{Value: current.Value.Clone(state.Alloc)}
}

func evaluate(t *testing.T, program *eval.Program, text string) eval.Result {
	parsed := expr.Parse(t.Allocator(), "test", text)
	if len(parsed.Diagnostics) != 0 {
		t.Error("parse failed")
		parsed.Free()
		return eval.Result{}
	}
	result := program.Evaluate(t.Allocator(), parsed.Expr, program.Scope)
	parsed.Free()
	return result
}

func TestMapCallbacksReceiveKeyAndMissingParametersAreNil(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(map (value key => (list value key)) [10, 20])")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].List[0].Int != 20 || result.Value.List[1].List[1].Int != 1 {
		t.Error("binary map callback did not receive value and key")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (value => value) [10, 20])")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Int != 20 {
		t.Error("unary map callback did not receive only the value")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (value key extra => (list value key extra)) [10])")
	if result.Diagnostic.Code != "" || result.Value.List[0].List[2].Kind != core.Nil {
		t.Error("missing callback parameter was not nil")
	}
	result.Free(a)
	result = evaluate(t, program, "((first second => (list first second)) 1)")
	if result.Diagnostic.Code != "" || result.Value.List[1].Kind != core.Nil {
		t.Error("missing function parameter was not nil")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestEvaluatesContainersReferencesAndSpecialForms(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((item => item.name) {name: \"kame\", count: 2})")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "kame" {
		t.Error("record reference was not evaluated")
	}
	result.Free(a)
	result = evaluate(t, program, `((item => item."name") {name: "kame"})`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "kame" {
		t.Error("quoted field did not evaluate to the matching record value")
	}
	result.Free(a)
	result = evaluate(t, program, `((name => {a: 1, ...{a: 2, b: 3}, b: 4, name}) "kame")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Record || len(result.Value.Record) != 3 ||
		result.Value.Record[0].Key != "a" || result.Value.Record[0].Value.Int != 2 ||
		result.Value.Record[1].Key != "b" || result.Value.Record[1].Value.Int != 4 ||
		result.Value.Record[2].Key != "name" || result.Value.Record[2].Value.Text != "kame" {
		t.Error("record spread did not preserve order, overwrite earlier fields, or resolve shorthand")
	}
	result.Free(a)
	result = evaluate(t, program, `((items => {...items, unused: missing}) [1])`)
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("invalid record spread did not fail before evaluating the next field")
	}
	result.Free(a)
	result = evaluate(t, program, `[1, 2].0`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 1 {
		t.Error("selection did not evaluate its base expression")
	}
	result.Free(a)
	result = evaluate(t, program, `(missing ?? "fallback").0`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "f" {
		t.Error("grouped fallback did not evaluate before field selection")
	}
	result.Free(a)
	result = evaluate(t, program, `if false then missing elif 0 then "chosen" else missing`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "chosen" {
		t.Error("if/elif/else evaluated an unselected arm or treated zero as false")
	}
	result.Free(a)
	result = evaluate(t, program, `"mode: @{if true then "debug" else missing}"`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "mode: debug" {
		t.Error("string interpolation did not evaluate its conditional expression")
	}
	result.Free(a)
	result = evaluate(t, program, `(x rest... => x) 42 7 8`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 {
		t.Error("arrow lambda did not bind its fixed and rest arguments")
	}
	result.Free(a)
	result = evaluate(t, program, `(() => 42) ()`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 {
		t.Error("zero-argument arrow lambda did not evaluate")
	}
	result.Free(a)
	result = evaluate(t, program, "missing ?? nil")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Nil {
		t.Error("fallback did not suppress missing reference")
	}
	result.Free(a)
	result = evaluate(t, program, "[...1] ?? missing")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("fallback suppressed a non-missing failure")
	}
	result.Free(a)
	result = evaluate(t, program, "((local => local) \"value\")")
	result.Free(a)
	result = evaluate(t, program, "local")
	if result.Diagnostic.Code != "REF_MISSING" {
		t.Error("let binding leaked into its parent scope")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestBindingNamesUseDefinitionGrammar(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "def value-name! = 42\ndef identity? value-name! = value-name!")
	program := eval.Compile(a, engine, parsed, registry)
	for _, input := range []string{"(module/value => module/value)", "(inputs/changed => inputs/changed)"} {
		invalid := expr.Parse(a, "test", input)
		if len(invalid.Diagnostics) == 0 || invalid.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("qualified lambda parameter accepted: %s", input)
		}
		invalid.Free()
	}
	invalid := []string{
		"def module/value = 42",
		"def module/function value = value",
		"def invalid module/value = 1",
		"def invalid valid module/value = 1",
	}
	for i := range invalid {
		result := script.Parse(a, "test", invalid[i])
		if len(result.Diagnostics) == 0 || result.Diagnostics[0].Code != "PARSE_ERR" {
			t.Error("namespaced reference was accepted as a binding name or parameter")
		}
		result.Free()
	}
	valid := []string{
		"((inputs => inputs) 42)",
		"((value-name? => value-name?) 42)",
		"identity? 42",
	}
	for i := range valid {
		result := evaluate(t, program, valid[i])
		if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 {
			t.Error("ordinary binding name or parameter was rejected")
		}
		result.Free(a)
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestIfAndOrWithMatchFormsAreLazy(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(if false then missing else \"fallback\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "fallback" {
		t.Error("if did not skip its unselected branch")
	}
	result.Free(a)
	result = evaluate(t, program, "(if false then \"a\" elif false then \"b\" else \"c\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "c" {
		t.Error("variadic if did not select else")
	}
	result.Free(a)
	result = evaluate(t, program, "true and 1 and 2")
	if result.Diagnostic.Code != "" || result.Value.Int != 2 {
		t.Error("and did not return its last true operand")
	}
	result.Free(a)
	result = evaluate(t, program, "false or nil or \"x\"")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" {
		t.Error("or did not return its first true operand")
	}
	result.Free(a)
	for _, input := range []string{"false and missing", "true or missing"} {
		result = evaluate(t, program, input)
		if result.Diagnostic.Code != "" || result.Value.Kind != core.Bool || result.Value.Bool != (input == "true or missing") {
			t.Errorf("%s did not return its deciding operand", input)
		}
		result.Free(a)
	}
	result = evaluate(t, program, "({...{a: 1}, a: 2}).a")
	if result.Diagnostic.Code != "" || result.Value.Int != 2 {
		t.Error("record spread did not use the last field value")
	}
	result.Free(a)
	result = evaluate(t, program, "match \"./a.c\"\n    ./{name:*}.c => name\n    else => \"no\"")
	if result.Diagnostic.Code != "" || result.Value.Text != "a" {
		t.Error("match did not bind its capture")
	}
	result.Free(a)
	for _, example := range []struct {
		expression string
		kind       core.Kind
		text       string
	}{
		{"match 2\n    1 => missing\n    2 => \"first\"\n    2 => missing\n    else => missing", core.String, "first"},
		{"match false\n    true => missing", core.Nil, ""},
		{"match nil\n    nil => \"empty\"", core.String, "empty"},
		{"match \"demo.c\"\n    \"{base}.c\" => base", core.String, "demo"},
		{"match \"./demo.c\"\n    ./*.c => \"glob\"", core.String, "glob"},
	} {
		result = evaluate(t, program, example.expression)
		if result.Diagnostic.Code != "" || result.Value.Kind != example.kind || result.Value.Text != example.text {
			t.Error("match selected the wrong arm: " + example.expression + ": " + result.Diagnostic.Code)
		}
		result.Free(a)
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestComparisonsCatAndText(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "1 == 1.0")
	if result.Diagnostic.Code != "" || !result.Value.Bool {
		t.Error("numeric equality across int and float failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(ne nil false)")
	if result.Diagnostic.Code != "" || !result.Value.Bool {
		t.Error("nil and false were not distinct")
	}
	result.Free(a)
	result = evaluate(t, program, "(lt 1 \"a\")")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("mixed ordering was not rejected")
	}
	result.Free(a)
	result = evaluate(t, program, "(template-cat \"a\" nil 1)")
	if result.Diagnostic.Code != "" || result.Value.Text != "a1" {
		t.Error("cat did not concatenate")
	}
	result.Free(a)
	result = evaluate(t, program, "(template-cat {a: 1})")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("cat accepted a record")
	}
	result.Free(a)
	result = evaluate(t, program, "(text \"hi\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "hi" {
		t.Error("text did not pass through a string")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestRenderContentTemplate(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(render \"hash\" \"# @if(true)\nhi\n# @end\n\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "hi\n" {
		t.Error("content render did not remove directive lines")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestLexicalFunctionAndOperationShadowing(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((x => x) \"ok\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "ok" {
		t.Error("lambda call failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestWideAndDeepScopeLookupHasOnlyEvaluatorOverhead(t *testing.T) {
	a := t.Allocator()
	tracker := a.(*mem.Tracker)
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "scope-index", "")
	program := eval.Compile(a, engine, parsed, registry)
	defer engine.Free()
	defer program.Free()
	defer parsed.Free()
	defer registry.Free()

	// Go allocates the evaluator frame on its heap. Scope lookup should add
	// no allocation beyond evaluating a literal through the same entry point.
	baselineExpr := expr.Parse(a, "literal", "0")
	baselineStart := tracker.Stats().TotalAlloc
	baselineResult := program.Evaluate(a, baselineExpr.Expr, program.Scope)
	evaluatorBytes := tracker.Stats().TotalAlloc - baselineStart
	baselineResult.Free(a)
	baselineExpr.Free()

	wideFields := make([]core.RecordField, 512)
	for i := range wideFields {
		name := "v" + string(byte('0'+i/100)) + string(byte('0'+i/10%10)) + string(byte('0'+i%10))
		wideFields[i] = core.RecordField{Key: name, Value: core.Value{Kind: core.Int, Int: int64(i)}}
	}
	wideRecord := core.NewRecord(a, wideFields)
	mem.FreeSlice(nil, wideFields)
	context := &eval.Context{Program: program, Run: a, Scope: program.Scope}
	wide := eval.RenderChildScope(context, program.Scope, wideRecord)
	wideRecord.Free(a)
	if wide == nil {
		t.Fatal("failed to construct wide scope")
		return
	}
	wideExpr := expr.Parse(a, "wide-scope", "v511")
	if len(wideExpr.Diagnostics) != 0 {
		t.Fatal("failed to parse wide-scope lookup")
		return
	}
	before := tracker.Stats().TotalAlloc
	wideResult := program.Evaluate(a, wideExpr.Expr, wide)
	wideAllocations := tracker.Stats().TotalAlloc - before
	if wideResult.Diagnostic.Code != "" || wideResult.Value.Kind != core.Int || wideResult.Value.Int != 511 {
		t.Error("wide-scope lookup returned the wrong binding")
	}
	wideResult.Free(a)
	wide.Free()
	wideExpr.Free()
	if wideAllocations > evaluatorBytes {
		t.Errorf("wide-scope lookup allocated %d bytes beyond evaluator overhead", wideAllocations-evaluatorBytes)
	}

	outer := core.NewRecord(a, []core.RecordField{{Key: "outer", Value: core.Value{Kind: core.Int, Int: 42}}})
	deep := eval.RenderChildScope(context, program.Scope, outer)
	outer.Free(a)
	for i := 0; i < 16; i++ {
		local := core.NewRecord(a, []core.RecordField{{Key: "local", Value: core.Value{Kind: core.Int, Int: int64(i)}}})
		child := eval.RenderChildScope(context, deep, local)
		local.Free(a)
		deep.Free()
		if child == nil {
			t.Fatal("failed to construct deep scope")
			return
		}
		deep = child
	}
	deepExpr := expr.Parse(a, "deep-scope", "outer")
	if len(deepExpr.Diagnostics) != 0 {
		t.Fatal("failed to parse deep-scope lookup")
		return
	}
	before = tracker.Stats().TotalAlloc
	deepResult := program.Evaluate(a, deepExpr.Expr, deep)
	deepAllocations := tracker.Stats().TotalAlloc - before
	if deepResult.Diagnostic.Code != "" || deepResult.Value.Kind != core.Int || deepResult.Value.Int != 42 {
		t.Error("deep-scope lookup returned the wrong binding")
	}
	deepResult.Free(a)
	deep.Free()
	deepExpr.Free()
	if deepAllocations > evaluatorBytes {
		t.Errorf("deep-scope lookup allocated %d bytes beyond evaluator overhead", deepAllocations-evaluatorBytes)
	}
}

func TestUnusedLambdaArgumentReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((f => \"x\") (x => x))")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" {
		t.Error("lambda application failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestReturnedLambdaArgumentReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((f => f) (x => x))")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Callable {
		t.Error("lambda argument was not returned as a callable")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestNestedLambdaArgumentsReleaseTheirScopes(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((f => ((g => \"x\") f)) (x => x))")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" {
		t.Error("nested lambda application failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

// Leak checks ride on t.Allocator's Tracker: each test below frees its
// result, engine, program, and registry, so a leaked Scope, wrapper, or
// Native state fails the test through malloc/free mismatch.

func TestSectionArgumentReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((s => \"x\") (list _0 _1))")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" {
		t.Error("section argument failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestNestedDefReleasesItsFunction(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.ParseKash(a, "test.kash", "def outer ()\n\tdef foo x = x\n\treturn (foo 0)\ndef result = (outer ())\n")
	program := eval.Compile(a, engine, parsed, registry)
	result := program.Definition("result")
	if result == nil {
		t.Errorf("nested def did not compile: %v", parsed.Diagnostics)
	} else {
		engine.Request(result)
		stepUntilCurrent(engine, result)
		if !result.Current || result.Latest.Kind != core.Int || result.Latest.Int != 0 {
			t.Errorf("nested def call failed: %s", result.Diagnostic.Code)
		}
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestNamedCallableReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "def f x = x")
	program := eval.Compile(a, engine, parsed, registry)
	reference := evaluate(t, program, "f")
	if reference.Diagnostic.Code != "" || reference.Value.Kind != core.Callable {
		t.Error("definition did not resolve to a callable")
	}
	reference.Free(a)
	result := evaluate(t, program, "f 42")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 {
		t.Error("freeing the callable reference changed its binding")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestKashLetRejectsBindingsWithEscapingCallables(t *testing.T) {
	for _, value := range []string{"(x => captured)", "[(x => captured)]", "{value: (x => captured)}"} {
		a := t.Allocator()
		engine := core.NewEngine(a)
		registry := eval.NewRegistry(a)
		parsed := script.ParseKash(a, "test.kash", "def probe ()\n\tlet bound = ((captured => "+value+") 42)\n\treturn 0\ndef result = (probe ())\n")
		if len(parsed.Diagnostics) != 0 {
			t.Errorf("eager binding did not parse: %v", parsed.Diagnostics)
		}
		program := eval.Compile(a, engine, parsed, registry)
		result := program.Definition("result")
		if result != nil {
			engine.Request(result)
			stepUntilCurrent(engine, result)
		}
		if result == nil || result.Diagnostic.Code != "DEF_ESCAPE" {
			t.Errorf("eager binding did not report DEF_ESCAPE for %s: %+v", value, result)
		}
		engine.Free()
		program.Free()
		parsed.Free()
		registry.Free()
	}
}

func TestReturnedDefinitionFunctionKeepsItsScopeAlive(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "def factory captured = (x => captured)")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(factory \"alive\") nil")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "alive" {
		t.Error("returned definition lost its captured scope")
	}
	result.Free(a)
	result = evaluate(t, program, "factory 42")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Callable {
		t.Error("definition function did not escape as an owned result")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDescendantStoreAllowed(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((a => ((b => (b 42)) a)) (x => x))")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 {
		t.Error("descendant function store was rejected")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestCallArgDescendantAllowed(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((f => (f 42)) (x => x))")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 42 {
		t.Error("same-scope function call failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDefValueIsLazy(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "def answer = 42")
	program := eval.Compile(a, engine, parsed, registry)
	result := program.Definition("answer")
	if result == nil || result.Current {
		t.Error("def value did not create an unmaterialized definition")
	} else {
		engine.Request(result)
		stepUntilCurrent(engine, result)
		if !result.Current || result.Latest.Kind != core.Int || result.Latest.Int != 42 {
			t.Error("def value failed to materialize")
		}
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestInvalidOperationArgsReleaseCallables(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(count (x => x))")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("count of callable was not rejected")
	}
	result.Free(a)
	result = evaluate(t, program, "(first (x => x))")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("first of callable was not rejected")
	}
	result.Free(a)
	result = evaluate(t, program, "(apply (x => x) (y => y))")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("apply of two callables was not rejected")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestFilterCallbackReturningLambdaReleases(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(filter (x => (y => y)) [1, 2])")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.List || len(result.Value.List) != 2 {
		t.Error("filter with lambda result failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestFlatmapCallbackReturningLambdaIsInvalid(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(flatmap (x => (y => y)) [1])")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("flatmap of lambda result was not rejected")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestApplyFormsReleaseCallables(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	// All functions receive the list's positional arguments.
	result := evaluate(t, program, "(apply (x => x) [1])")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 1 {
		t.Error("apply unary argument spreading failed")
	}
	result.Free(a)
	result = evaluate(t, program, "((list 1) |> apply (x => x))")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 1 {
		t.Error("piped apply failed")
	}
	result.Free(a)
	// Multi-parameter functions splat the list.
	result = evaluate(t, program, "(apply (x y => x) [1, 2])")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 1 {
		t.Error("apply splat failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestSectionDefBodyCloneReleases(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "def foo () = (_0)")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(foo ()) \"x\"")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" {
		t.Error("def with section body failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestNativeReplaceSectionArgumentReleasesItsState(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((r => \"x\") (subst ./src/{n:*}.c ./build/{n}.o))")
	if result.Diagnostic.Code != "" || result.Value.Text != "x" {
		t.Error("native section let failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestListErrorDiscardsCallableScopes(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "[(x => x), missing]")
	if result.Diagnostic.Code != "REF_MISSING" {
		t.Error("list error did not fail")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestRecordErrorDiscardsCallableScopes(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "{f: (x => x), bad: missing}")
	if result.Diagnostic.Code != "REF_MISSING" {
		t.Error("record error did not fail")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestCallErrorDiscardsCallableArgs(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((f => f) (x => x) missing)")
	if result.Diagnostic.Code != "REF_MISSING" {
		t.Error("call error did not fail")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDefinitionIsLazyAndPublishesTemplateValue(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "name = \"kame\"")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("name")
	if node == nil || node.Current {
		t.Error("definition started during compilation")
	}
	engine.Request(node)
	engine.Step()
	if !node.Current || node.Latest.Kind != core.String || node.Latest.Text != "kame" {
		t.Error("lazy definition did not publish its rendered value")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDefinitionDependenciesAreSharedByTheEngine(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "first = \"ka\"\nsecond = \"@{first}me\"")
	program := eval.Compile(a, engine, parsed, registry)
	engine.Request(program.Definition("second"))
	engine.Step()
	engine.Step()
	engine.Step()
	node := program.Definition("second")
	if !node.Current || node.Latest.Text != "kame" {
		t.Error("definition dependency did not resume and render")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestCapabilitiesBlockOperationsBeforeInvocation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	state := &operationState{}
	if !registry.Add(eval.Operation{Name: "read-one", Call: readOperation, Context: state, MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Read}}) {
		t.Error("operation registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(read-one ())")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" || len(result.Diagnostic.Frames) != 1 || state.Calls != 0 {
		t.Error("denied operation was invoked without an operation frame")
	}
	result.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestFieldAccessAndRuleSelectors(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((items => items.-1) [\"a\", \"b\", \"c\"])")
	if result.Diagnostic.Code != "" || result.Value.Text != "c" {
		t.Error("negative list index failed")
	}
	result.Free(a)
	expression := expr.Parse(a, "test", "@>")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Outputs: []core.Value{core.NewString(a, "out")}}
	result = program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "" || result.Value.Text != "out" {
		t.Error("rule output selector failed")
	}
	result.Free(a)
	context.Outputs[0].Free(a)
	expression.Free()
	expression = expr.Parse(a, "test", "@>1")
	context.Outputs = []core.Value{core.NewString(a, "first"), core.NewString(a, "second")}
	result = program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "" || result.Value.Text != "second" {
		t.Error("indexed rule selector failed")
	}
	result.Free(a)
	context.Outputs[0].Free(a)
	context.Outputs[1].Free(a)
	expression.Free()
	result = evaluate(t, program, "((item => {name: item.name, count: item.count}) {name: \"kame\", count: 2})")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Record || result.Value.Record[0].Value.Text != "kame" || result.Value.Record[1].Value.Int != 2 {
		t.Error("record selection failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestLiftedDefinitionReevaluatesAfterDependencyInvalidation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	state := &operationState{}
	registry.Add(eval.Operation{Name: "count-op", Call: countOperation, Context: state, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "test", "first = \"input\"\nalias = first\nsecond = (count-op alias)")
	program := eval.Compile(a, engine, parsed, registry)
	first, second := program.Definition("first"), program.Definition("second")
	engine.Request(second)
	for i := 0; i < 5; i++ {
		engine.Step()
	}
	if !second.Current || second.Latest.Int != 1 {
		t.Error("lifted definition did not publish initial operation value")
	}
	engine.Invalidate(first)
	for i := 0; i < 5; i++ {
		engine.Step()
	}
	if !second.Current || second.Latest.Int != 2 || state.Calls != 2 {
		t.Error("lifted definition did not reevaluate after dependency changed")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestOperationCanSubmitAndResumeFromHostCompletion(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "result = (async ())")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	engine.Step()
	request := program.Requests.Next()
	if !node.Submitted || !request.OK || request.Request.NodeID != node.ID || request.Request.ID != node.HostRequestID {
		t.Error("operation did not queue correlated host work")
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt + 1, RequestID: request.Request.ID})
	engine.Step()
	if node.Current || !node.Submitted {
		t.Error("stale operation completion resumed evaluation")
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	engine.Step()
	engine.Step()
	if !node.Current || node.Latest.Int != 42 {
		t.Error("operation did not resume from completion")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestOperationCanPublishAStream(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "emit-values", Call: streamOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "result = emit-values ()")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	for i := 0; i < 5; i++ {
		engine.Step()
	}
	if !node.Current || node.Latest.Text != "second" || node.State != core.NodeWaiting {
		t.Error("operation stream did not publish and return to dependency waiting")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestOperationCanCompleteWithoutAValue(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "complete", Call: completeOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "result = (complete ())")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	engine.Step()
	if node.Current || node.State != core.NodeWaiting {
		t.Error("valueless operation completion published a value")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
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
	for i := 0; i < 3; i++ {
		engine.Step()
	}
	first := program.Requests.Next()
	if !first.OK {
		t.Error("initial operation did not submit")
	}
	engine.Invalidate(input)
	for i := 0; i < 4; i++ {
		engine.Step()
	}
	cancellation := engine.NextCancellation()
	second := program.Requests.Next()
	if cancellation.RequestID != first.Request.ID || !second.OK || second.Request.ID == first.Request.ID {
		t.Error("dependency update did not cancel and restart operation")
	}
	engine.Complete(core.Completion{NodeID: first.Request.NodeID, Generation: first.Request.Generation, Attempt: first.Request.Attempt, RequestID: first.Request.ID})
	engine.Complete(core.Completion{NodeID: second.Request.NodeID, Generation: second.Request.Generation, Attempt: second.Request.Attempt, RequestID: second.Request.ID})
	first.Request.Free(a)
	second.Request.Free(a)
	for i := 0; i < 3; i++ {
		engine.Step()
	}
	if !result.Current || result.Latest.Int != 42 {
		t.Error("restarted operation did not publish")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestNestedOperationResumesThroughFunctionCall(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "def wrap value = (async ())\nresult = (wrap \"value\")")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	engine.Step()
	request := program.Requests.Next()
	if !request.OK {
		t.Error("nested operation did not submit host work")
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	engine.Step()
	engine.Step()
	if !node.Current || node.Latest.Int != 42 {
		t.Error("nested operation did not resume through function call")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestCapabilityRootsUseWholePathSegments(t *testing.T) {
	context := eval.Context{Run: t.Allocator(), Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"../src"}}}}
	if !context.Allows(eval.Read, "/workspace/src/main.km") {
		t.Error("granted path was denied")
	}
	if context.Allows(eval.Read, "/workspace/src-old/main.km") {
		t.Error("prefix sibling escaped capability root")
	}
	if context.Allows(eval.Read, "../../secret.txt") {
		t.Error("traversal escaped capability root")
	}
	resources := eval.Context{Run: t.Allocator(), Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Write, Names: []string{"mem://cache/"}}}}
	if !resources.Allows(eval.Write, "mem://cache/output.bin") {
		t.Error("trailing slash in resource namespace grant denied a child resource")
	}
	if resources.Allows(eval.Write, "mem://cache-other/output.bin") {
		t.Error("resource namespace grant allowed a sibling namespace")
	}
	filePaths := eval.Context{Run: t.Allocator(), Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"../src"}}}}
	if !filePaths.Allows(eval.Read, "file:///workspace/src/main.km") {
		t.Error("native filesystem grant denied an equivalent file URI")
	}
	fileURIs := eval.Context{Run: t.Allocator(), Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"file:///workspace/src/"}}}}
	if !fileURIs.Allows(eval.Read, "/workspace/src/main.km") {
		t.Error("file URI grant denied an equivalent filesystem path")
	}
}

func TestRestrictedHostRequestIsDeniedBeforeQueueing(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "read-file", Call: readFileOperation, MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Read}})
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(read-file ())")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"./allowed"}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" || program.Requests.Next().OK {
		t.Error("restricted host request was queued or not denied")
	}
	result.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestSourceLoadingIsNotAnOperationCapability(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "load-source", Call: loadSourceOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(load-source ())")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Grants: []eval.Grant{{Capability: eval.Read}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" || program.Requests.Next().OK {
		t.Error("operation could forge a privileged source-loading request")
	}
	result.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestCommandCaptureExecutionPolicy(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	forms := []string{"$(printf hello)", "(run! \"printf\" \"hello\")", "(pipe! (run! \"printf\" \"hello\") (run! \"cat\"))"}
	for i := range forms {
		expression := expr.Parse(a, "test", forms[i])
		context := eval.Context{Program: program, Scope: program.Scope, Run: a, Grants: []eval.Grant{{Capability: eval.Run}}}
		context.Phase = eval.PlanningPhase
		result := program.EvaluateWith(expression.Expr, &context)
		if result.Diagnostic.Code != "PHASE_INVALID" {
			t.Error("planning authorized a process")
		}
		result.Free(a)
		context.Phase = eval.ResolvingPhase
		result = program.EvaluateWith(expression.Expr, &context)
		if result.Diagnostic.Code != "PHASE_INVALID" {
			t.Error("resolution authorized a process")
		}
		result.Free(a)
		context.Phase = eval.EvaluatePhase
		context.Grants = nil
		result = program.EvaluateWith(expression.Expr, &context)
		if result.Diagnostic.Code != "CAP_DENIED" {
			t.Error("missing run capability was not denied")
		}
		result.Free(a)
		context.Grants = []eval.Grant{{Capability: eval.Run}}
		result = program.EvaluateWith(expression.Expr, &context)
		if result.Diagnostic.Code != "HOST_FAIL" {
			t.Error("missing execution host was not diagnosed")
		}
		result.Free(a)
		if program.Requests.Next().OK {
			t.Error("invalid execution context submitted a request")
		}
		expression.Free()
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestRestrictedRecordHostRequestUsesPayloadPath(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "read-file", Call: recordReadFileOperation, MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Read}})
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(read-file ())")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Cwd: "/workspace/project", Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"./allowed"}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" {
		t.Error("restricted record request escaped capability root")
	}
	result.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestRestrictedCustomRequestIsDeniedBeforeQueueing(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "custom-read", Call: customReadOperation, MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Read}})
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(custom-read ())")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"./allowed"}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" || program.Requests.Next().OK {
		t.Error("restricted custom request was queued or not denied")
	}
	result.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestEvalRetainsParserDiagnostic(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(eval \"(\")")
	if result.Diagnostic.Code != "PARSE_ERR" || result.Diagnostic.Span.End == 0 || result.Diagnostic.Message == "invalid eval expression" {
		t.Error("eval discarded its parser diagnostic")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
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
	if result.Diagnostic.Code != "REF_MISSING" || result.Diagnostic.Source != "input" {
		t.Error("evaluation diagnostic lacked its source")
	}
	result.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestTaskKeyIncludesOperationVersion(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "compile", Version: "v2", Call: countOperation, MinArity: 0, MaxArity: 0})
	key := registry.TaskKey(a, "app", "compile")
	if !key.OK || key.Key.Kind != core.ResourceTask || key.Key.Name != "app\x00compile\x00v2" {
		t.Error("task key omitted operation version")
	}
	key.Key.Free(a)
	registry.Free()
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
	if result.Diagnostic.Code != "" || result.Value.Text != "inner" {
		t.Error("selector did not use nearest rule frame")
	}
	result.Free(a)
	outer.Free(a)
	inner.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestResultFreeReleasesReturnedLambda(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(value => value)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Callable {
		t.Error("lambda evaluation failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestTemplateRejectsRecordInterpolation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "((value => \"@{value}\") {name: \"kame\"})")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("record interpolation did not fail")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDuplicateDefinitionsAreDiagnosedWithoutReplacingFirst(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "name = \"first\"\nname = \"second\"")
	program := eval.Compile(a, engine, parsed, registry)
	if program.Valid || len(program.Diagnostics) != 1 || program.Diagnostics[0].Code != "DEF_INVALID" {
		t.Error("duplicate definition was not diagnosed")
	}
	if program.Definition("name") != nil {
		t.Error("invalid evaluator program exposed executable definitions")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestCheckedCompileRejectsInvalidProgramBeforeRegistration(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "name = \"first\"\nname = \"second\"")
	result := eval.CompileChecked(a, engine, parsed, registry)
	if result.Program != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "DEF_INVALID" {
		t.Error("checked compile returned an executable invalid program")
	}
	result.Free(a)
	engine.Free()
	parsed.Free()
	registry.Free()
}

func TestDefinitionCycleFailsThroughEngine(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "a = b\nb = a")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("a")
	engine.Request(node)
	for i := 0; i < 6; i++ {
		engine.Step()
	}
	if node.State != core.NodeFailed || node.Diagnostic.Code != "DEP_CYCLE" || node.Diagnostic.Severity != diagnostic.Error {
		t.Error("definition cycle did not fail")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDefinitionCannotPublishCallable(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "callable = (value => value)")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("callable")
	engine.Request(node)
	engine.Step()
	if node.State != core.NodeFailed || node.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("callable definition crossed engine boundary")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestFunctionDefinitionsEvaluateExpressionStrings(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "def prefix value = \"pre-@{value}\"\ndef quoted value = \"@{value}!\"")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(prefix \"fix\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "pre-fix" {
		t.Error("function expression-string prefix was not evaluated")
	}
	result.Free(a)
	result = evaluate(t, program, "(quoted \"ok\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "ok!" {
		t.Error("function expression-string suffix was not evaluated")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestRecursiveEvaluationDepthIsBoundedAndRestored(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "depth.km", `def walk TEXT = (if (count TEXT == 0) then 0 else (character TEXT))
def character TEXT = (walk (slice 1 TEXT))`)
	program := eval.Compile(a, engine, parsed, registry)
	context := eval.Context{Program: program, Scope: program.Scope, Run: a}
	texts := []string{`(walk "x,y\n")`, `(walk (reduce (TEXT STEP => (str TEXT TEXT)) "x,y\n" [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]))`, `(walk "x,y\n")`, `(count (reduce (TEXT STEP => (str TEXT TEXT)) "x,y\n" [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]))`}
	for i := range texts {
		expression := expr.Parse(a, "call.km", texts[i])
		if len(expression.Diagnostics) != 0 {
			t.Error("depth fixture failed to parse")
		}
		result := program.EvaluateWith(expression.Expr, &context)
		if i == 1 {
			if result.Diagnostic.Code != "EVAL_DEPTH" {
				t.Error("deep recursion did not report EVAL_DEPTH")
			}
		} else if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || (i == 3 && result.Value.Int != 4096) || (i != 3 && result.Value.Int != 0) {
			t.Error("bounded recursion/fold failed or depth was not restored")
		}
		result.Free(a)
		expression.Free()
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestZeroArgumentScriptFunctionIsCallable(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "def constant () = \"value\"")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(constant ())")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "value" {
		t.Error("zero-argument script function was not callable")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDefFunctionSupportsRestAndRejectsDuplicates(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test.km", "def collect first rest... = rest")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(collect \"first\" \"second\" \"third\")")
	if result.Diagnostic.Code != "" {
		t.Errorf("def rest call failed: %s", result.Diagnostic.Code)
	} else if result.Value.Kind != core.List {
		t.Error("def rest parameter was not a list")
	} else if len(result.Value.List) != 2 {
		t.Error("def rest parameter had the wrong length")
	} else if result.Value.List[0].Text != "second" || result.Value.List[1].Text != "third" {
		t.Error("def rest parameter had wrong values")
	}
	result.Free(a)
	duplicate := script.Parse(a, "test", "def duplicate value value = value")
	if len(duplicate.Diagnostics) == 0 || duplicate.Diagnostics[0].Code != "PARSE_ERR" {
		t.Error("duplicate def parameters were accepted")
	}
	duplicate.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestReturnedClosureRetainsCallScopeAndNestedCallRestoresScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(((outer => (inner => outer)) \"captured\") \"ignored\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "captured" {
		t.Error("returned closure lost its call scope")
	}
	result.Free(a)
	result = evaluate(t, program, "((outer => ((ignored => outer) ((inner => inner) \"inner\"))) \"outer\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "outer" {
		t.Error("nested call left a stale scope")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestEvaluationContextFramesAndDefinitionCycleFrames(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "missing")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, Frames: []diagnostic.Frame{{Label: "rule", Span: diagnostic.Span{Start: 11, End: 15}}}}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "REF_MISSING" || result.Diagnostic.Severity != diagnostic.Error || result.Diagnostic.Span.Start != 0 || result.Diagnostic.Span.End != 7 || len(result.Diagnostic.Frames) != 1 || result.Diagnostic.Frames[0].Label != "rule" || result.Diagnostic.Frames[0].Span.Start != 11 {
		t.Error("evaluation context frame wrapping changed the original diagnostic")
	}
	result.Free(a)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()

	engine = core.NewEngine(a)
	registry = eval.NewRegistry(a)
	parsed = script.Parse(a, "test", "a = \"@{b}\"\nb = \"@{a}\"")
	program = eval.Compile(a, engine, parsed, registry)
	node := program.Definition("a")
	engine.Request(node)
	for i := 0; i < 6; i++ {
		engine.Step()
	}
	if node.Diagnostic.Code != "DEP_CYCLE" || len(node.Diagnostic.Frames) == 0 {
		t.Error("definition cycle lacked definition frames")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestNestedFunctionSelectorsUseNearestArguments(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "def inner value = @_\ndef outer value = (inner \"inner\")")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(outer \"outer\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "inner" {
		t.Error("nested function selector did not use nearest arguments")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestOperationRegistersAndReadsDynamicFileDependency(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	file := mem.Alloc[fileState](a)
	file.Alloc, file.Text = a, "file-data"
	engine.AddOwned(core.ResourceKey{Kind: core.ResourceFile, Name: "./input.txt"}, fileProducer, file, freeFileState)
	registry := eval.NewRegistry(a)
	registry.Add(eval.Operation{Name: "read-file", Call: dependencyOperation, Context: file, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "test", "result = (read-file ())")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	for i := 0; i < 4; i++ {
		engine.Step()
	}
	if !node.Current || node.Latest.Text != "file-data" {
		t.Error("dynamic file dependency did not resume evaluation")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func freeFileState(a mem.Allocator, value any) { mem.Free(a, value.(*fileState)) }

func TestPlaceholderSectionsEvaluateAsLambdas(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "def helper x = (nop x)")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `(_0) 7`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Int || result.Value.Int != 7 {
		t.Error("identity section failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(list _ __) "one" "two"`)
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "two" {
		t.Error("multi-placeholder section failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(list _ _) "same"`)
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 {
		t.Error("repeated placeholder did not reuse its argument")
	}
	result.Free(a)
	result = evaluate(t, program, `(_0) ()`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Nil {
		t.Error("section did not bind its missing argument to nil")
	}
	result.Free(a)
	result = evaluate(t, program, `(_0) 1 2`)
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("section accepted too many arguments")
	}
	result.Free(a)
	result = evaluate(t, program, `(helper _0) "bound"`)
	if result.Diagnostic.Code != "" || result.Value.Text != "bound" {
		t.Error("section over a defined function failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(map (nth 0 _) _) (list ["x"] ["y"])`)
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[0].Text != "x" || result.Value.List[1].Text != "y" {
		t.Error("nested section did not keep its own placeholder")
	}
	result.Free(a)
	result = evaluate(t, program, `(map (nth 0 _0) (list ["a", "b"] ["c"]))`)
	if result.Diagnostic.Code != "" || result.Value.List[0].Text != "a" || result.Value.List[1].Text != "c" {
		t.Error("nested section callback through map failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(_) 7`)
	if result.Diagnostic.Code != "" || result.Value.Int != 7 {
		t.Error("underscore identity section failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(nop _0) 7`)
	if result.Diagnostic.Code != "" || result.Value.Int != 7 {
		t.Error("digit identity section failed")
	}
	result.Free(a)
	result = evaluate(t, program, "((x => x) 7)")
	if result.Diagnostic.Code != "" || result.Value.Int != 7 {
		t.Error("explicit lambda failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(apply (list _0 _1) ["a", "b"])`)
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "b" {
		t.Error("section through apply failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestPatternValuesMaterializeAsText(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "./{**}/{*}.c")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Pattern || result.Value.Text != "./{**}/{*}.c" {
		t.Error("path pattern did not materialize")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestQuotedBraceValuesStayText(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "\"./{name:*}.c\"")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "./{name:*}.c" {
		t.Error("quoted braces did not remain literal text")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestPlainPathValuesMaterializeAsText(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "./src/main.c")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String {
		t.Error("plain path stopped materializing as a string")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestOperationWrappedDefinitionCycleOwnsDiagnosticFrames(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "cycle.km", "value = (str value)")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("value")
	engine.Request(node)
	for i := 0; i < 6; i++ {
		engine.Step()
	}
	if node.State != core.NodeFailed || node.Diagnostic.Code != "DEP_CYCLE" || node.Diagnostic.Severity != diagnostic.Error {
		t.Error("operation cycle overwrote the engine failure")
	}
	cloned := node.Diagnostic.Clone(a)
	cloned.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}
