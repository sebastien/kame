package evalcheck

import (
	"kame/core"
	legacy "kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"testing"
)

func TestApplicationSpread(t *testing.T) {
	ctx := legacy.Wrap(t)
	a := ctx.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "spread.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	defer func() {
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}()

	for _, input := range []string{
		`nth ...[0, [42]]`,
		`(nth ...[] 0 ...[[42]])`,
		`(x y => nth x y) ...[0] [42]`,
		`(f x => f x) ...[(y => y), 42]`,
	} {
		r := evaluate(ctx, p, input)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Int || r.Value.Int != 42 {
			t.Errorf("%s: value=%+v, diagnostic=%s", input, r.Value, r.Diagnostic.Code)
		}
		r.Free(a)
	}

	result := evaluate(ctx, p, `subst ...[./{name:*}.c] "${name}.o" ...["./test.c"]`)
	if result.Diagnostic.Code != "" || result.Value.Text != "test.o" {
		t.Errorf("spread lost the deferred replacement scope: value=%q, diagnostic=%s", result.Value.Text, result.Diagnostic.Code)
	}
	result.Free(a)

	for _, input := range []string{`nth ...[1]`, `nth ...[1, 2, 3]`, `nth ...1 (fail! "must not run")`} {
		r := evaluate(ctx, p, input)
		if r.Diagnostic.Code != "EXPR_INVALID" {
			t.Errorf("%s: wanted EXPR_INVALID, got %q", input, r.Diagnostic.Code)
		}
		r.Free(a)
	}
}

func TestApplicationSpreadSurvivesSuspension(t *testing.T) {
	checkRetainedOperands(legacy.Wrap(t), "result = list ...[(once ())] ...(list (async ()))")
}

func TestValueFallbackUsesOnlyMissingLookups(t *testing.T) {
	ctx := legacy.Wrap(t)
	a := ctx.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "fallback.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	defer func() {
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}()
	for _, input := range []string{`nil ?? 42`, `missing ?? 42`, `{a: 1}.missing ?? nil ?? 42`, `42 ?? (fail! "must not run")`} {
		r := evaluate(ctx, p, input)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Int || r.Value.Int != 42 {
			t.Errorf("%s: value=%+v, diagnostic=%s", input, r.Value, r.Diagnostic.Code)
		}
		r.Free(a)
	}
	result := evaluate(ctx, p, `false ?? 42`)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Bool || result.Value.Bool {
		t.Error("present false was replaced")
	}
	result.Free(a)
	result = evaluate(ctx, p, `(nth "bad" []) ?? 42`)
	if result.Diagnostic.Code != "EXPR_INVALID" {
		t.Errorf("fallback swallowed a type error: %s", result.Diagnostic.Code)
	}
	result.Free(a)
}

func TestValuePipeStages(t *testing.T) {
	ctx := legacy.Wrap(t)
	a := ctx.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "pipes.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	defer func() {
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}()

	for _, input := range []string{
		`[42] |> nth 0`,
		`[42] |> nth 0 _`,
		`[42] |> nop _.0`,
		`[42] |> _.0`,
		`(_.0) [42]`,
		`[0, [42]] |> nth ..._`,
		`[[42]] |> map (nth 0 _) |> first`,
		`[42] |> list _ _ |> first |> first`,
	} {
		r := evaluate(ctx, p, input)
		if r.Diagnostic.Code != "" || r.Value.Kind != core.Int || r.Value.Int != 42 {
			t.Errorf("%s: value=%+v, diagnostic=%s", input, r.Value, r.Diagnostic.Code)
		}
		r.Free(a)
	}
}

func TestValuePipeReusesInputAcrossSuspension(t *testing.T) {
	checkRetainedOperandsCount(legacy.Wrap(t), "result = (once ()) |> list _ (async ()) _", 3)
}

func TestNumericOperatorsUseLibraryContracts(t *testing.T) {
	ctx := legacy.Wrap(t)
	a := ctx.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "numbers.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	defer func() {
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}()
	for _, tc := range []struct {
		input string
		want  core.Value
	}{
		{`2 + 3 * 4`, core.Value{Kind: core.Int, Int: 14}},
		{`sub 10 4`, core.Value{Kind: core.Int, Int: 6}},
		{`2 / 1`, core.Value{Kind: core.Float, Float: 2}},
		{`div 5 2`, core.Value{Kind: core.Float, Float: 2.5}},
		{`2 + 0.5`, core.Value{Kind: core.Float, Float: 2.5}},
		{`-5 % 2`, core.Value{Kind: core.Int, Int: -1}},
		{`neg 2.5`, core.Value{Kind: core.Float, Float: -2.5}},
		{`add ...[19, 23]`, core.Value{Kind: core.Int, Int: 42}},
	} {
		result := evaluate(ctx, p, tc.input)
		if result.Diagnostic.Code != "" || result.Value.Kind != tc.want.Kind || result.Value.Int != tc.want.Int || result.Value.Float != tc.want.Float {
			t.Errorf("%s: value=%+v, diagnostic=%s", tc.input, result.Value, result.Diagnostic.Code)
		}
		result.Free(a)
	}
	for _, input := range []string{
		`1 / 0`, `1 % 0`, `1.0 % 2`, `1 + false`,
		`9223372036854775807 + 1`, `-9223372036854775808 - 1`,
		`-9223372036854775808 * -1`, `neg -9223372036854775808`, `1e308 * 1e308`,
	} {
		result := evaluate(ctx, p, input)
		if result.Diagnostic.Code != "EXPR_INVALID" {
			t.Errorf("%s: wanted EXPR_INVALID, got %q", input, result.Diagnostic.Code)
		}
		result.Free(a)
	}
}

func TestLogicalAndRetainsEffectsAcrossSuspension(t *testing.T) {
	checkSuspendedValueEffect(t, "result = once () and async ()", false)
}

func TestValueFallbackRetainsEffectsInLeftOperand(t *testing.T) {
	checkSuspendedValueEffect(t, "result = once () ?? async ()", true)
}

func checkSuspendedValueEffect(t *testing.T, input string, nilInput bool) {
	t.Helper()
	ctx := legacy.Wrap(t)
	a := ctx.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Context: &state, MinArity: 0, MaxArity: 0, Call: func(c *eval.Context, value any, args []core.Value) eval.Result {
		result := memoCountOperation(c, value, args)
		if nilInput {
			result.Value = core.Value{Kind: core.Nil}
		}
		return result
	}})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKameFile(a, "suspended.km", input)
	p := eval.Compile(a, engine, parsed, registry)
	defer func() {
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}()
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	request := stepUntilRequest(engine, p)
	if !request.OK || state.Calls != 1 || state.Effects != 0 {
		t.Fatal("expression did not suspend after evaluating the input once")
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	stepUntilCurrent(engine, node)
	if !node.Current || node.Latest.Int != 42 || state.Calls != 1 || state.Effects != 1 {
		t.Fatal("expression replayed the input or lost its effect")
	}
}

func TestGroupingDoesNotInvokeCallable(t *testing.T) {
	ctx := legacy.Wrap(t)
	a := ctx.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "groups.km", "def example () = 42")
	p := eval.Compile(a, engine, parsed, registry)
	defer func() {
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}()

	value := evaluate(ctx, p, "((example))")
	if value.Diagnostic.Code != "" || value.Value.Kind != core.Callable {
		t.Errorf("group evaluated a callable: %s", value.Diagnostic.Code)
	}
	value.Free(a)
	value = evaluate(ctx, p, "(example ())")
	if value.Diagnostic.Code != "" || value.Value.Kind != core.Int || value.Value.Int != 42 {
		t.Errorf("explicit call failed: %s", value.Diagnostic.Code)
	}
	value.Free(a)
}
