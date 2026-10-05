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
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "1" {
		t.Error("leading pattern or integer expansion failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (replace {*}.c :false) [\"a.c\" \"b.c\"])")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[0].Text != "false" || result.Value.List[1].Text != "false" {
		t.Error("scalar replacement section failed")
	}
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

func TestRuntimePatternConstruction(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.Parse(a, "pattern", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `(pattern (cat "./" "{" "name:*" "}.c"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Pattern || result.Value.Text != "./{name:*}.c" {
		t.Error("runtime pattern construction did not return the validated pattern")
	}
	result.Free(a)
	result = evaluate(t, program, `(pattern (cat "./" "{" "name:*" "}" "{" "_0" "}"))`)
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("runtime pattern construction accepted mixed matcher and reference groups")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestRegexCaptureOperations(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Error("library registration failed") }
	parsed := script.Parse(a, "regex", "")
	program := eval.Compile(a, engine, parsed, registry)
	match := `(regex-match (pattern (cat "{" "name:~" "[a-z]+" "}" "{" "~" "[0-9]+" "}")) "demo42")`
	result := evaluate(t, program, match)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Record {
		t.Error("regex-match did not return a match record")
	} else {
		captures := regexTestField(result.Value, "captures")
		named := regexTestField(result.Value, "named")
		if regexTestField(result.Value, "text").Text != "demo42" || captures.Kind != core.List || len(captures.List) != 2 || captures.List[0].Text != "demo" || captures.List[1].Text != "42" || regexTestField(named, "name").Text != "demo" {
			t.Error("regex-match returned incorrect positional or named captures")
		}
	}
	result.Free(a)
	result = evaluate(t, program, `(capture 1 (regex-match (pattern (cat "{" "name:~" "[a-z]+" "}" "{" "~" "[0-9]+" "}")) "demo42"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "42" { t.Error("capture did not select the indexed value") }
	result.Free(a)
	result = evaluate(t, program, `(capture "name" (regex-match (pattern (cat "{" "name:~" "[a-z]+" "}")) "demo"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "demo" { t.Error("capture did not select the named value") }
	result.Free(a)
	result = evaluate(t, program, `(capture 1 (regex-match (pattern (cat "{" "name:~[a-z]+}" "-" "{" "name:~[a-z]+}")) "abc-abc"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "abc" { t.Error("repeated named regex groups did not populate each positional slot") }
	result.Free(a)
	result = evaluate(t, program, `(regex-replace (pattern (cat "{" "name:~" "[a-z]+" "}")) (pattern (cat "x{" "name" "}")) "demo")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "xdemo" { t.Error("regex-replace did not expand a named capture") }
	result.Free(a)
	result = evaluate(t, program, `(regex-replace (pattern (cat "{" "name:~[a-z]+}")) (pattern (cat "x{" "name" "}")) ["demo" "42"])`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.List || len(result.Value.List) != 2 || result.Value.List[0].Kind != core.String || result.Value.List[0].Text != "xdemo" || result.Value.List[1].Kind != core.Nil {
		t.Error("regex-replace did not map captures over a list")
	}
	result.Free(a)
	result = evaluate(t, program, `(regex-replace (pattern (cat "{" "name:~[a-z]+}")) (pattern (cat "x{" "missing" "}")) "demo")`)
	if result.Diagnostic.Code != "PAT_INVALID" { t.Error("regex-replace accepted a missing capture reference") }
	result.Free(a)
	result = evaluate(t, program, `(regex-replace (pattern (cat "{" "name:~[a-z]+}")) "x" :nil)`)
	if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("regex-replace accepted a nil subject") }
	result.Free(a)
	result = evaluate(t, program, `(regex-replace (pattern (cat "{" "name:~[a-z]+}")) "x" 42)`)
	if result.Diagnostic.Code != "EXPR_INVALID" { t.Error("regex-replace accepted a non-string subject") }
	result.Free(a)
	result = evaluate(t, program, `(regex-match (pattern (cat "{" "~(ab|cd)+" "}")) "abcd")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Record { t.Error("regex groups, alternation or repetition did not match") }
	result.Free(a)
	result = evaluate(t, program, `(regex-match (pattern (cat "{" "~[" "}")) "a")`)
	if result.Diagnostic.Code != "PAT_INVALID" { t.Error("malformed runtime regex was accepted") }
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDocumentMatchRendersNamedCaptureAndFallback(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Error("library registration failed") }
	parsed := script.Parse(a, "document-match", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `(match "./posts/demo.md" [./posts/{slug:*}.md slug] [:else "other"])`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "demo" {
		t.Error("baseline expression match failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(render (cat "@match(\"./posts/demo.md\")\n@case(./posts/{slug:*}.md)\n@" "(slug)\n@else\nother\n@end(match)\n") "plain")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "demo\n" {
		t.Error("document match selected capture mismatch: " + result.Diagnostic.Code + " " + result.Diagnostic.Message + " " + result.Value.Text)
	}
	result.Free(a)
	result = evaluate(t, program, `(render (cat "@match(\"./other.txt\")\n@case(./posts/{slug:*}.md)\n@" "(slug)\n@else\nother\n@end(match)\n") "plain")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "other\n" {
		t.Error("document match fallback mismatch: " + result.Diagnostic.Code + " " + result.Diagnostic.Message + " " + result.Value.Text)
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDocumentForBindsIndexKeyAndValue(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Error("library registration failed") }
	parsed := script.Parse(a, "document-for", "")
	program := eval.Compile(a, engine, parsed, registry)
	templateText := `(cat "@for([item] xs)\n" "@" "(index)" ":" "@" "(key)" ":" "@" "(item)" ";\n@end(for)\n")`
	result := evaluate(t, program, `(render `+templateText+` [xs: ["a" "b"]] "plain")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "0::a;\n1::b;\n" {
		t.Error("list loop did not bind its zero-based index, nil key and item value")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestTemplateItemsOwnsInputRows(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Error("library registration failed") }
	parsed := script.Parse(a, "template-items", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `(template-items ["a" "b"])`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.List || len(result.Value.List) != 2 {
		t.Error("template-items did not return one row per list item")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func regexTestField(record core.Value, key string) core.Value {
	for i := range record.Record { if record.Record[i].Key == key { return record.Record[i].Value } }
	return core.Value{Kind: core.Nil}
}

func TestComputedRecordLookupTransfersSelectedCallables(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	defer engine.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	parsed := script.Parse(a, "lookup", "")
	defer parsed.Free()
	p := eval.Compile(a, engine, parsed, registry)
	defer p.Free()
	samples := []string{
		"(get [one: 1 two: 2] (cat \"t\" \"wo\"))",
		"(get [one: 1] \"missing\" 7)",
		"(get [one: 1] \"missing\")",
		"(apply (get [used: ([x] (cat \"hello \" x)) unused: ([x] x)] \"used\" ([x] x)) [\"Ada\"])",
		"(apply (get [unused: ([x] x)] \"missing\" ([x] (cat \"fallback \" x))) [\"Ada\"])",
		"(get [one: 1] 1)",
	}
	for i := range samples {
		r := evaluate(t, p, samples[i])
		if i < 5 && r.Diagnostic.Code != "" {
			t.Error("valid computed lookup failed")
		}
		if i == 0 && r.Value.Int != 2 {
			t.Error("computed key lost")
		}
		if i == 1 && r.Value.Int != 7 {
			t.Error("missing-key default lost")
		}
		if i == 2 && r.Value.Kind != core.Nil {
			t.Error("missing key must yield nil")
		}
		if i == 3 && r.Value.Text != "hello Ada" {
			t.Error("selected callable did not survive lookup")
		}
		if i == 4 && r.Value.Text != "fallback Ada" {
			t.Error("fallback callable did not survive lookup")
		}
		if i == 5 && r.Diagnostic.Code != "EXPR_INVALID" {
			t.Error("invalid key type was accepted")
		}
		r.Free(a)
	}
}
