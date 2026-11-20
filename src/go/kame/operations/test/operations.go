package operationscheck

import (
	"kame/core"
	"kame/host"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/template"
	"kame/operations"
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
	parsed := script.ParseKameFile(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(count (list \"one\" \"two\"))")
	if result.Diagnostic.Code != "" || result.Value.Int != 2 {
		t.Error("count/list failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(resource "mem://workspace/a/./b")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Resource || result.Value.Resource.Kind != core.ResourceFile || result.Value.Resource.Name != "mem://workspace/a/b" {
		t.Error("resource did not return a canonical resource identity")
	}
	result.Free(a)
	result = evaluate(t, program, `(resource "mem://workspace/../../escape")`)
	if result.Diagnostic.Code != "RES_INVALID" {
		t.Error("resource accepted a URI that escapes its root")
	}
	result.Free(a)
	result = evaluate(t, program, "(first (list \"first\" \"second\"))")
	if result.Diagnostic.Code != "" || result.Value.Text != "first" {
		t.Error("first failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(not nil)")
	if result.Diagnostic.Code != "" || !result.Value.Bool {
		t.Error("not failed")
	}
	result.Free(a)
	result = evaluate(t, program, "((list \"first\" \"second\") |> map (x => x))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "second" {
		t.Error("piped map failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (x => (list x x)) (list \"first\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 1 || len(result.Value.List[0].List) != 2 {
		t.Error("map container result failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (x => x) (list \"first\" \"second\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "second" {
		t.Error("function-first map failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(filter (x => (includes? \"second\" x)) (list \"first\" \"second\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 1 || result.Value.List[0].Text != "second" {
		t.Error("predicate-first filter failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(flatmap (x => (list x x)) [\"first\"])")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[1].Text != "first" {
		t.Error("function-first flatmap failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(apply (values => (count values)) [[\"first\", \"second\"]])")
	if result.Diagnostic.Code != "" || result.Value.Int != 2 {
		t.Error("list-consuming apply failed")
	}
	result.Free(a)
	checkPureOperationObservations(t, program, registry)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func checkPureOperationObservations(t *testing.T, program *eval.Program, registry *eval.Registry) {
	a := t.Allocator()
	examples := []string{
		`not nil`, `bool 1`, `blank? " "`, `fmt {name: 1}`, `eval "1 + 1"`,
		`parse-json "{\"name\":1}"`, `parse-args {} ["operand"]`, `sub-token "a" "b" "a a"`,
		`str "a" 1`, `json {name: 1}`, `count ["a"]`,
		`add 1 2`, `sub 3 2`, `mul 2 3`, `div 3 2`, `mod 3 2`, `neg 1`,
		`eq? 1 1`, `ne? 1 2`, `lt? 1 2`, `gt? 2 1`, `gte? 2 2`, `lte? 2 2`,
		`in? 1 [1, 2]`, `has-key? "x" {x: nil}`, `success? {status: 0, signal: 0}`,
		`template-cat "a" nil 1`, `text "a"`, `template-apply (a b => add a b) [1, 2]`,
		`template-map (x => uppercase x) ["a"]`, `template-trim-start " a"`, `template-trim-end "a "`,
		`source-dir ()`, `get "a" {a: 1}`, `first [1]`, `nth 0 [1]`,
		`apply (x => count x) [[1, 2]]`, `list 1 2`, `indexed ["a"]`, `items {x: 1}`,
		`put "y" 2 {x: 1}`, `merge {x: 1} {y: 2}`,
		`record? {}`, `list? []`, `string? "x"`, `nil? nil`, `bool? true`,
		`number? 1.0`, `int? 1`, `float? 1.0`, `bytes? "x"`, `callable? uppercase`,
		`resource? (resource "mem://suite/input")`, `pattern? (pattern "{name:*}")`,
		`process? nil`, `empty? []`, `digest "sha256" "abc"`,
		`map (x => x + 1) [1]`, `flatmap (x => [x, x]) [1]`,
		`filter (x => x > 0) [0, 1]`, `filter-out (x => x > 0) [0, 1]`,
		`reduce (a b => a + b) 0 [1, 2]`,
		`concat [1] [2]`, `slice 0 1 [1, 2]`, `sorted [2, 1]`, `unique [1, 1]`,
		`join "," ["a", "b"]`, `split "," "a,b"`, `strip " a "`, `line "a\n"`,
		`subst (pattern "{name:*}.c") ./out/{name}.o "a.c"`,
		`match? (pattern "{name:*}.c") "a.c"`, `includes? "b" "abc"`,
		`regex-group "name" (regex-match (pattern "{name:~[a-z]+}") "abc")`,
		`resub (pattern "{name:~[a-z]+}") ./out/{name} "abc"`,
		`capture ./in/{name:*}.c ./in/a.c`, `bind ./out/{name}.o {name: "a"}`,
		`starts? "a" "ab"`, `ends? "b" "ab"`,
		`black "a"`, `red "a"`, `green "a"`, `yellow "a"`, `blue "a"`, `magenta "a"`,
		`cyan "a"`, `white "a"`, `bold "a"`, `dim "a"`, `uppercase "a"`, `lowercase "A"`,
		`basename ./dir/file.txt`, `dirname ./dir/file.txt`, `splitext ./dir/file.txt`,
		`ext ./dir/file.txt`, `joinpath ./dir "file.txt"`, `relpath ./dir ./dir/file.txt`,
		`abspath ./file.txt`, `nop 1`,
	}
	reached := make(map[string]bool)
	program.AddSourcePart("/workspace/library.km", 0, 0, 0)
	program.SetSourcePartDirectory("/workspace")
	origin := len(program.SourceParts)
	for _, text := range examples {
		parsed := expr.Parse(a, "/workspace/library.km", text)
		if len(parsed.Diagnostics) != 0 {
			t.Errorf("invalid operation example %q: %v", text, parsed.Diagnostics)
			parsed.Free()
			continue
		}
		eval.SetExpressionSourceOrigin(parsed.Expr, origin, nil)
		observations := 0
		context := eval.Context{
			Program: program, Scope: program.Scope, Run: a, Cwd: "/workspace", Source: parsed.Source.Name, SourceOrigin: origin,
			Grants:             []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}, {Capability: eval.Env}},
			DirectHostRequests: true,
			DependencyObserver: func(any, core.ResourceKey) { observations++ },
			ToolResolver:       func(any, string) (string, bool) { observations++; return "", false },
			OperationObserver:  func(_ any, name, _ string) { reached[name] = true },
		}
		result := program.EvaluateWith(parsed.Expr, &context)
		if result.Diagnostic.Code != "" || result.Waiting || program.Requests.HasRequests() ||
			observations != 0 || len(context.Effects) != 0 || len(context.WritePaths) != 0 {
			t.Errorf("pure operation example %q: diagnostic=%s waiting=%t requests=%t observations=%d effects=%d writes=%d",
				text, result.Diagnostic.Message, result.Waiting, program.Requests.HasRequests(), observations,
				len(context.Effects), len(context.WritePaths))
		}
		eval.FreeEffects(a, context.Effects)
		result.Free(a)
		parsed.Free()
	}
	for _, operation := range registry.Items {
		if operation.Category == eval.PureOperation && !reached[operation.Name] {
			t.Errorf("missing host-free operation example for %s", operation.Name)
		}
	}
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
	scriptSource := script.ParseKameFile(a, "test", "")
	program := eval.Compile(a, engine, scriptSource, registry)
	cases := []diagnosticCase{
		{Text: "(first 1)", Message: "`first` argument 1 expects list or string; got int", Operand: "1"},
		{Text: "(nth false [1])", Message: "`nth` argument 1 expects int; got bool", Operand: "false"},
		{Text: "(slice 0 false [1])", Message: "`slice` argument 2 expects int; got bool", Operand: "false"},
		{Text: "(join \"-\" [1])", Message: "`join` argument 2 item 1 expects string; got int", Operand: "[1]"},
		{Text: "(first ())", Message: "`first` expects 1 argument; got 0", Operand: "first ()"},
		{Text: "(slice ())", Message: "`slice` expects 2 to 3 arguments; got 0", Operand: "slice ()"},
		{Text: "(out! ())", Message: "`out!` expects at least 1 argument; got 0", Operand: "out! ()"},
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
	parsed := script.ParseKameFile(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(out! (subst ./{*} ./out/{_0}))")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("out accepted a callable")
	}
	result.Free(a)
	result = evaluate(t, program, "(err! (subst ./{*} ./out/{_0}))")
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
	parsed := script.ParseKameFile(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	expression := expr.Parse(a, "test", "(out! \"hello\" 10)")
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
	parsed := script.ParseKameFile(a, "test", "result = (list (out! \"once\") (wait-each \"done\"))")
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
	parsed := script.ParseKameFile(a, "test", "result = (map (item => (wait-each item)) (list \"one\" \"two\"))")
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

func waitWithoutEngine(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Text == "wait" {
		return eval.Result{Waiting: true}
	}
	return eval.Result{Value: values[0].Clone(context.Run)}
}

func TestUntrackedCallbackSuspensionReleasesProgress(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	registry.Add(eval.Operation{Name: "untracked-wait", Call: waitWithoutEngine, MinArity: 1, MaxArity: 1})
	parsed := script.ParseKameFile(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		`(map untracked-wait ["first", "wait"])`,
		`(flatmap (x => (list (untracked-wait x))) ["first", "wait"])`,
		`(filter untracked-wait ["first", "wait"])`,
		`(filter-out untracked-wait ["first", "wait"])`,
		`(reduce (acc x => (untracked-wait x)) "initial" ["first", "wait"])`,
	}
	for i := range texts {
		result := evaluate(t, program, texts[i])
		if !result.Waiting || result.Diagnostic.Code != "" {
			t.Error("untracked callback did not suspend: " + texts[i])
		}
		result.Free(a)
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
	parsed := script.ParseKameFile(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, "(subst ./{**}/{*}.c ./build/{_0}/{_1}.c \"./a/b/x.c\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "./build/a/b/x.c" {
		t.Error("pattern replace did not expand positional captures")
	}
	result.Free(a)
	captureExpressions := []string{
		`(subst ./journal/{name:*} "${name}" "./journal/demo")`,
		`(subst ./journal/{name:*} name "./journal/demo")`,
		`((r => ((name => (r ./journal/{name:*} name "./journal/demo")) "outer")) subst)`,
		`(((prefix => (subst ./journal/{name:*} (str prefix name))) "") "./journal/demo")`,
		`((r => (r "./journal/demo")) (subst ./journal/{name:*} name))`,
		`((eval """(subst ./journal/{name:*} (get "value" {value: name}))""") "./journal/demo")`,
	}
	for i := range captureExpressions {
		result = evaluate(t, program, captureExpressions[i])
		if result.Diagnostic.Code != "" || result.Value.Text != "demo" {
			t.Error("capture expression failed: " + captureExpressions[i])
		}
		result.Free(a)
	}
	result = evaluate(t, program, `(subst ./journal/{name:*} missing "./other/demo")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "./other/demo" {
		t.Error("nonmatch evaluated replacement or lost subject")
	}
	result.Free(a)
	result = evaluate(t, program, `(map (subst ./journal/{name:*} (uppercase name)) ["./journal/a", "./journal/b"])`)
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[0].Text != "A" || result.Value.List[1].Text != "B" {
		t.Error("deferred captures did not bind per item")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst (pattern \"{name:*}.c\") 1 \"hello.c\")")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "1" {
		t.Error("leading pattern or integer expansion failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (subst (pattern \"{*}.c\") false) [\"a.c\", \"b.c\"])")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[0].Text != "false" || result.Value.List[1].Text != "false" {
		t.Error("scalar replacement section failed")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst ./src/{name:*}.c ./build/{name}.o \"./src/demo.c\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "./build/demo.o" {
		t.Error("pattern replace did not expand named captures")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst ./{**}/{*}.c ./build/{_0}/{_1}.c \"./no-match.txt\")")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "./no-match.txt" {
		t.Error("pattern replace did not preserve a nonmatch")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst ./{**}/{*}.c ./build/{_0}.o (list \"./a.c\" \"./x/y.c\" \"./z.obj\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 3 || result.Value.List[0].Text != "./a.c" || result.Value.List[1].Text != "./build/x.o" || result.Value.List[2].Text != "./z.obj" {
		t.Error("pattern replace did not map a list subject in order")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (subst ./{**}/{*}.c ./build/{_0}.o) (list \"./a.c\" \"./x/y.c\"))")
	if result.Diagnostic.Code != "" || len(result.Value.List) != 2 || result.Value.List[0].Text != "./a.c" || result.Value.List[1].Text != "./build/x.o" {
		t.Error("pattern replace section did not work through map")
	}
	result.Free(a)
	result = evaluate(t, program, "(\"./a/x.c\" |> subst ./{**}/{*}.c ./build/{_0}/{_1}.c)")
	if result.Diagnostic.Code != "" || result.Value.Text != "./build/a/x.c" {
		t.Error("pattern replace section did not work through a pipe")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst \"a\" \"b\" \"banana\")")
	if result.Diagnostic.Code != "" || result.Value.Text != "bbnbnb" {
		t.Error("literal replace changed behavior")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst \"one\" \"two\")")
	if result.Diagnostic.Code == "" {
		t.Error("two string arguments were accepted as a match pattern")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst ./{name:*}.c ./build/{missing}.o \"./a.c\")")
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("missing capture reference was not diagnosed")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst ./{**}/{*}.c ./build/{_0}/{_9}.c \"./a/b.c\")")
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("out-of-range capture reference was not diagnosed")
	}
	result.Free(a)
	result = evaluate(t, program, `(subst (pattern '{a}') ./y/{a} "./x")`)
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("match pattern without matchers was accepted")
	}
	result.Free(a)
	result = evaluate(t, program, "(str (subst ./{**}/{*}.c ./build/{_0}.o \"./a.c\"))")
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
	result = evaluate(t, program, "(join \"-\" [\"a\", ./x/{n:*}.c])")
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("join accepted a pattern element")
	}
	result.Free(a)
	result = evaluate(t, program, "(subst ./{**}/{*}.c ./build/{_0}.o)")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Callable {
		t.Error("two-argument pattern replace did not return a callable")
	}
	result.Free(a)
	result = evaluate(t, program, "(map (subst ./{**}/{*}.c ./build/{_0}.o) [\"./x/y.c\"])")
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
	parsed := script.ParseKameFile(a, "pattern", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `(pattern (str "./" "{" "name:*" "}.c"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.PathPattern || result.Value.Text != "./{name:*}.c" {
		t.Error("runtime pattern construction did not return the validated pattern")
	}
	result.Free(a)
	result = evaluate(t, program, `(pattern (str "./" "{" "name:*" "}" "{" "_0" "}"))`)
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
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.ParseKameFile(a, "regex", "")
	program := eval.Compile(a, engine, parsed, registry)
	match := `(regex-match (pattern (str "{" "name:~" "[a-z]+" "}" "{" "~" "[0-9]+" "}")) "demo42")`
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
	result = evaluate(t, program, `(regex-group 1 (regex-match (pattern (str "{" "name:~" "[a-z]+" "}" "{" "~" "[0-9]+" "}")) "demo42"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "42" {
		t.Error("capture did not select the indexed value")
	}
	result.Free(a)
	result = evaluate(t, program, `(regex-group "name" (regex-match (pattern (str "{" "name:~" "[a-z]+" "}")) "demo"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "demo" {
		t.Error("capture did not select the named value")
	}
	result.Free(a)
	result = evaluate(t, program, `(regex-group 1 (regex-match (pattern (str "{" "name:~[a-z]+}" "-" "{" "name:~[a-z]+}")) "abc-abc"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "abc" {
		t.Error("repeated named regex groups did not populate each positional slot")
	}
	result.Free(a)
	result = evaluate(t, program, `(resub (pattern (str "{" "name:~" "[a-z]+" "}")) (pattern (str "x{" "name" "}")) "demo")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "xdemo" {
		t.Error("resub did not expand a named capture")
	}
	result.Free(a)
	result = evaluate(t, program, `(resub (pattern (str "{" "name:~[a-z]+}")) (pattern (str "x{" "name" "}")) ["demo", "42"])`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.List || len(result.Value.List) != 2 || result.Value.List[0].Kind != core.String || result.Value.List[0].Text != "xdemo" || result.Value.List[1].Kind != core.Nil {
		t.Error("resub did not map captures over a list")
	}
	result.Free(a)
	result = evaluate(t, program, `(resub (pattern (str "{" "name:~[a-z]+}")) (pattern (str "x{" "missing" "}")) "demo")`)
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("resub accepted a missing capture reference")
	}
	result.Free(a)
	result = evaluate(t, program, `(resub ./{name:*} "x" "./a")`)
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("resub accepted a non-regex matcher")
	}
	result.Free(a)
	result = evaluate(t, program, `(resub (pattern (str "{" "name:~[a-z]+}")) "x" nil)`)
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("resub accepted a nil subject")
	}
	result.Free(a)
	result = evaluate(t, program, `(resub (pattern (str "{" "name:~[a-z]+}")) "x" 42)`)
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("resub accepted a non-string subject")
	}
	result.Free(a)
	result = evaluate(t, program, `(regex-match (pattern (str "{" "~(ab|cd)+" "}")) "abcd")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Record {
		t.Error("regex groups, alternation or repetition did not match")
	}
	result.Free(a)
	result = evaluate(t, program, `(regex-match (pattern (str "{" "~[" "}")) "a")`)
	if result.Diagnostic.Code != "PAT_INVALID" {
		t.Error("malformed runtime regex was accepted")
	}
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
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.ParseKameFile(a, "document-match", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `match "./posts/demo.md"
    ./posts/{slug:*}.md => slug
    else => "other"`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "demo" {
		t.Error("baseline expression match failed")
	}
	result.Free(a)
	result = evaluate(t, program, `(render "plain" (str "@match \"./posts/demo.md\"\n@case ./posts/" "{" "slug:*" "}.md\n@" "{" "slug" "}\n@else\nother\n@end match\n"))`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "demo\n" {
		t.Error("document match selected capture mismatch: " + result.Diagnostic.Code + " " + result.Diagnostic.Message + " " + result.Value.Text)
	}
	result.Free(a)
	result = evaluate(t, program, `(render "plain" (str "@match \"./other.txt\"\n@case ./posts/" "{" "slug:*" "}.md\n@" "{" "slug" "}\n@else\nother\n@end match\n"))`)
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
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.ParseKameFile(a, "document-for", "")
	program := eval.Compile(a, engine, parsed, registry)
	templateText := `(str "@for index item in indexed xs\n" "@" "{" "index" "}" ":" "@" "{" "item" "}" ";\n@end for\n")`
	result := evaluate(t, program, `(render {xs: ["a", "b"]} "plain" `+templateText+`)`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "0:a;\n1:b;\n" {
		t.Error("list loop did not destructure explicitly indexed values")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDocumentInlineBlocksRenderAcrossTextBoundaries(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.ParseKameFile(a, "document-inline", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `(render "plain" "Hi @if true there @end!")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "Hi @if true there @end!" {
		t.Error("plain style interpreted inline text as a directive")
	}
	result.Free(a)
	result = evaluate(t, program, `(render "html" "a<!-- @if true -->b<!-- @end if -->c")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "abc" {
		t.Error("HTML inline if block did not preserve text around the block")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestDocumentTrimMarkersRemoveAdjacentASCIIWhitespace(t *testing.T) {
	a := t.Allocator()
	doc := template.ParseDocument(a, "trim", "left \r\n \t @-if true -\r\n right \r\n\t@-end if - \r\n\t tail", "plain")
	if len(doc.Diagnostics) != 0 {
		t.Error("trim document parse: " + doc.Diagnostics[0].Code + " " + doc.Diagnostics[0].Message)
	}
	doc.Free()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.ParseKameFile(a, "document-trim", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `(render "plain" "left \r\n \t @-if true -\r\n right \r\n\t@-end if - \r\n\t tail")`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "leftrighttail" {
		t.Error("trim markers did not remove adjacent ASCII whitespace: " + result.Diagnostic.Code + " " + result.Diagnostic.Message + " " + result.Value.Text)
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
	if !operations.Register(registry) {
		t.Error("library registration failed")
	}
	parsed := script.ParseKameFile(a, "template-items", "")
	program := eval.Compile(a, engine, parsed, registry)
	result := evaluate(t, program, `(indexed ["a", "b"])`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.List || len(result.Value.List) != 2 {
		t.Error("indexed did not return one pair per list item")
	}
	result.Free(a)
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func regexTestField(record core.Value, key string) core.Value {
	for i := range record.Record {
		if record.Record[i].Key == key {
			return record.Record[i].Value
		}
	}
	return core.Value{Kind: core.Nil}
}

func TestComputedRecordLookupTransfersSelectedCallables(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	defer engine.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "lookup", "")
	defer parsed.Free()
	p := eval.Compile(a, engine, parsed, registry)
	defer p.Free()
	samples := []string{
		"(get (str \"t\" \"wo\") {one: 1, two: 2})",
		"(get \"missing\" 7 {one: 1})",
		"(get \"missing\" {one: 1})",
		"(apply (get \"used\" (x => x) {used: (x => (str \"hello \" x)), unused: (x => x)}) [\"Ada\"])",
		"(apply (get \"missing\" (x => (str \"fallback \" x)) {unused: (x => x)}) [\"Ada\"])",
		"(get 1 {one: 1})",
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
