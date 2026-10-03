package lib_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func waitEach(context *eval.Context, state any, values []core.Value) eval.Result {
	_, _ = state, values
	if context.TakeCompletion().RequestID != 0 {
		return eval.Result{Value: values[0].Clone(context.Run)}
	}
	if context.Submit(host.RequestCustom, core.Value{Kind: core.Nil}) == 0 {
		return eval.Result{Diagnostic: core.Diagnostic{Code: "HOST_FAIL"}}
	}
	return eval.Result{Waiting: true}
}

type outputState struct{ Count int }

func captureOutput(value any, effect eval.Effect) {
	_ = effect
	state := value.(*outputState)
	state.Count++
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

func TestPureOperations(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(count (list \"one\" \"two\"))")
	if result.Diagnostic.Code != "" || result.Value.Int != 2 {
		t.Error("count/list failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(first (list \"first\" \"second\"))")
	if result.Diagnostic.Code != "" || result.Value.Text != "first" {
		t.Error("first failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(not :nil)")
	if result.Diagnostic.Code != "" || !result.Value.Bool {
		t.Error("not failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(map ([x] x) (list \"first\" \"second\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "second" {
		t.Error("map failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(map ([x] (list x x)) (list \"first\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 1 || len(result.Value.List[0].List) != 2 {
		t.Error("map container result failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (list \"first\" \"second\") ([x] x))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "second" {
		t.Error("legacy map argument order failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(filter (list \"first\" \"second\") ([x] (includes? x \"second\")))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 1 || result.Value.List[0].Text != "second" {
		t.Error("legacy filter argument order failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(flatmap \"first\" ([x] (list x x)))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "first" {
		t.Error("legacy scalar flatmap failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(apply (list \"first\" \"second\") ([values] (count values)))")
	if result.Diagnostic.Code != "" || result.Value.Int != 2 {
		t.Error("legacy list-consuming apply failed")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

type diagnosticCase struct {
	Text    string
	Message string
	Operand string
}

func TestOperationDiagnosticsRetainOperandAndContract(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	scriptSource := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, scriptSource, registry)
	cases := []diagnosticCase{
		{Text: "(first 1)", Message: "`first` argument 1 expects list; got int", Operand: "1"},
		{Text: "(nth [1] :false)", Message: "`nth` argument 2 expects int; got bool", Operand: ":false"},
		{Text: "(slice [1] 0 :false)", Message: "`slice` argument 3 expects int; got bool", Operand: ":false"},
		{Text: "(join [1] \"-\")", Message: "`join` argument 1 item 1 expects string; got int", Operand: "[1]"},
		{Text: "(first)", Message: "`first` expects 1 argument; got 0", Operand: "(first)"},
		{Text: "(slice)", Message: "`slice` expects 2 to 3 arguments; got 0", Operand: "(slice)"},
		{Text: "(out)", Message: "`out` expects at least 1 argument; got 0", Operand: "(out)"},
	}
	for i := range cases {
		parsed := expr.Parse(a, "expr.km", cases[i].Text)
		context := eval.Context{Program: program, Scope: program.Scope, Run: a, Source: "expr.km"}
		result := program.EvaluateWith(parsed.Expr, &context)
		parsed.Free()
		d := result.Diagnostic
		if d.Code != "EXPR_INVALID" || d.Source != "expr.km" || d.Message != cases[i].Message {
			t.Error("operation diagnostic lost its contract or source")
		}
		if d.Span.Start < 0 || d.Span.End > len(cases[i].Text) || cases[i].Text[d.Span.Start:d.Span.End] != cases[i].Operand {
			t.Error("operation diagnostic does not identify the operand")
		}
		if len(d.Frames) != 1 || d.Frames[0].Label == "operation" {
			t.Error("operation frame is unnamed")
		}
		result.Free(a)
	}
	engine.Free()
	program.Free()
	scriptSource.Free()
	registry.Free()
}

func TestOutRejectsCallable(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(out (replace ./{*} ./out/{_0}))")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("out accepted a callable")
	}
	result.Free(a)
	result = evaluate(t, program, "(err (replace ./{*} ./out/{_0}))")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("err accepted a callable")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestOutputEffectsReturnCombinedStandaloneValue(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(out \"hello\" 10)")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a}
	result := program.EvaluateWith(expression.Expr, &context)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "hello10" {
		t.Error("out did not return combined text")
	}
	if len(context.Effects) != 2 || string(context.Effects[0].Data) != "hello" || string(context.Effects[1].Data) != "10" {
		t.Error("out did not preserve output chunks")
	}
	result.Free(a)
	eval.FreeEffects(a, context.Effects)
	expression.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDefinitionOutputFlushesOnlyAfterSuccessfulResume(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	registry.Add(eval.Operation{Name: "wait-each", Call: waitEach, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "test", "result = (list (out \"once\") (wait-each \"done\"))")
	program := eval.Compile(a, engine, parsed, registry)
	state := &outputState{}
	program.SetDefinitionEffectSink(captureOutput, state)
	node := program.Definition("result")
	engine.Request(node)
	for i := 0; i < 2; i++ {
		engine.Step()
	}
	request := program.Requests.Next()
	if !request.OK {
		t.Fatal("definition did not wait")
		return
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	for i := 0; i < 3; i++ {
		engine.Step()
	}
	if !node.Current || state.Count != 1 {
		t.Error("definition output did not flush once after resume")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestMapResumesWithoutRepeatingCallbacks(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	registry.Add(eval.Operation{Name: "wait-each", Call: waitEach, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "test", "result = (map ([item] (wait-each item)) (list \"one\" \"two\"))")
	program := eval.Compile(a, engine, parsed, registry)
	node := program.Definition("result")
	engine.Request(node)
	for i := 0; i < 2; i++ {
		engine.Step()
	}
	first := program.Requests.Next()
	if !first.OK {
		t.Fatal("first callback did not submit")
		return
	}
	engine.Complete(core.Completion{NodeID: first.Request.NodeID, Generation: first.Request.Generation, Attempt: first.Request.Attempt, RequestID: first.Request.ID})
	first.Request.Free(a)
	for i := 0; i < 2; i++ {
		engine.Step()
	}
	second := program.Requests.Next()
	if !second.OK || second.Request.ID == first.Request.ID {
		t.Fatal("second callback did not submit separately")
		return
	}
	engine.Complete(core.Completion{NodeID: second.Request.NodeID, Generation: second.Request.Generation, Attempt: second.Request.Attempt, RequestID: second.Request.ID})
	second.Request.Free(a)
	for i := 0; i < 3; i++ {
		engine.Step()
	}
	if !node.Current || len(node.Latest.List) != 2 || node.Latest.List[0].Text != "one" || node.Latest.List[1].Text != "two" {
		t.Error("map did not resume in input order")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestPatternReplace(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(replace ./{**}/{*}.c ./build/{_0}/{_1}.c \"./a/b/x.c\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "./build/a/b/x.c" {
		t.Error("pattern replace did not expand positional captures")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace {name:*}.c 1 \"hello.c\")")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "1" { t.Error("leading pattern or integer expansion failed") }
	result.Free(a)
	result = evaluate(t, program, "(map (replace {*}.c :false) [\"a.c\" \"b.c\"])")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[0].Text != "false" || result.Value.List[1].Text != "false" { t.Error("scalar replacement section failed") }
	result.Free(a)
	result = evaluate(t, program, "(replace ./src/{name:*}.c ./build/{name}.o \"./src/demo.c\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "./build/demo.o" {
		t.Error("pattern replace did not expand named captures")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace ./{**}/{*}.c ./build/{_0}/{_1}.c \"./no-match.txt\")")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Nil {
		t.Error("pattern replace did not return nil without a match")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace ./{**}/{*}.c ./build/{_0}.o (list \"./a.c\" \"./x/y.c\" \"./z.obj\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 3 || result.Value.List[0].Kind != core.Nil || result.Value.List[1].Text != "./build/x.o" || result.Value.List[2].Kind != core.Nil {
		t.Error("pattern replace did not map a list subject in order")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (replace ./{**}/{*}.c ./build/{_0}.o) (list \"./a.c\" \"./x/y.c\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[0].Kind != core.Nil || result.Value.List[1].Text != "./build/x.o" {
		t.Error("pattern replace section did not work through map")
	}
	result.Free(a)
	result = evaluate(t, program, "(\"./a/x.c\" | replace ./{**}/{*}.c ./build/{_0}/{_1}.c)")
	if result.Diagnostic.Code != "" || result.Value.Text != "./build/a/x.c" {
		t.Error("pattern replace section did not work through a pipe")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace \"banana\" \"a\" \"b\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "bbnbnb" {
		t.Error("literal replace changed behavior")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace \"one\" \"two\")")
	if result.Diagnostic.Code == "" {
		t.Error("two string arguments were accepted as a match pattern")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace ./{name:*}.c ./build/{missing}.o \"./a.c\")")
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("missing capture reference was not diagnosed")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace ./{**}/{*}.c ./build/{_0}/{_9}.c \"./a/b.c\")")
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("out-of-range capture reference was not diagnosed")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace ./x/{a} ./y/{a} \"./x\")")
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("match pattern without matchers was accepted")
	}
	result.Free(a)
	result = evaluate(t, program, "(str (replace ./{**}/{*}.c ./build/{_0}.o \"./a.c\"))")
	if result.Diagnostic.Code != "" {
		t.Error("str broke on a replace result")
	}
	result.Free(a)
	result = evaluate(t, program, "(uppercase ./{**}/{*}.c)")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("uppercase accepted a pattern")
	}
	result.Free(a)
	result = evaluate(t, program, "(joinpath ./{**}/{*}.c \"x\")")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("joinpath accepted a pattern")
	}
	result.Free(a)
	result = evaluate(t, program, "(join [\"a\" ./x/{n}.c] \"-\")")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("join accepted a pattern element")
	}
	result.Free(a)
	result = evaluate(t, program, "(replace ./{**}/{*}.c ./build/{_0}.o)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Callable {
		t.Error("two-argument pattern replace did not return a callable")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (replace ./{**}/{*}.c ./build/{_0}.o) [\"./x/y.c\"])")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 1 || result.Value.List[0].Text != "./build/x.o" {
		t.Error("replace section did not apply through map")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}
