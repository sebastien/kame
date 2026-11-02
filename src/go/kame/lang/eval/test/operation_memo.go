package eval_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type memoCountState struct {
	Calls   int
	Effects int
}

func memoCountOperation(c *eval.Context, value any, args []core.Value) eval.Result {
	_ = args
	state := value.(*memoCountState)
	state.Calls++
	c.Effects = slices.Append(c.Run, c.Effects, eval.Effect{Kind: eval.EffectOut, Data: slices.Clone(c.Run, []byte("once"))})
	return eval.Result{Value: core.Value{Kind: core.Int, Int: int64(state.Calls)}}
}

func memoEffectSink(value any, effect eval.Effect) {
	state := value.(*memoCountState)
	if effect.Kind == eval.EffectOut && string(effect.Data) == "once" {
		state.Effects++
	}
}

func TestCompletedOperationSurvivesSiblingWaitWithEffects(t *testing.T) {
	checkRetainedOperands(t, "result = (list (once) (async))")
}

func TestPipeLoweringPreservesRetainedEffects(t *testing.T) {
	checkRetainedOperands(t, "result = ((once) | list _ (async))")
}

func TestFunctionArgumentsSurviveSiblingWait(t *testing.T) {
	checkRetainedOperands(t, "(pair first second) = (list first second)\nresult = (pair (once) (async))")
}

func TestRecreatedLexicalScopeRetainsCompletedOperands(t *testing.T) {
	checkRetainedOperands(t, "result = (let [local 1] (list (once) (async)))")
}

func TestListSpreadRetainsCompletedOperands(t *testing.T) {
	checkRetainedOperands(t, "result = [...[(once)] ...(list (async))]")
}

func TestSuspendedOperationRetainsItsOwnArguments(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "wait", Call: asyncOperation, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "memo.km", "result = (wait (once))")
	p := eval.Compile(a, engine, parsed, registry)
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	for generation := 1; generation <= 2; generation++ {
		if generation == 1 {
			engine.Request(node)
		} else {
			engine.Invalidate(node)
		}
		request := stepUntilRequest(engine, p)
		if !request.OK {
			t.Error("operation did not suspend")
			break
		}
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
		request.Request.Free(a)
		stepUntilCurrent(engine, node)
		if !node.Current || node.Latest.Int != 42 || state.Calls != generation || state.Effects != generation {
			t.Error("suspended operation replayed its operand or lost its effects")
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestLoweredOperationsWithSameSpanKeepSeparateOperands(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	registry.Add(eval.Operation{Name: "wait", Call: asyncOperation, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "memo.km", "result = (list (wait 1))")
	// Template lowering assigns the wrapper and its sole include the same span.
	outer := parsed.Items[0].Definition.Expression
	outer.Items[1].Span = outer.Span
	p := eval.Compile(a, engine, parsed, registry)
	node := p.Definition("result")
	engine.Request(node)
	request := stepUntilRequest(engine, p)
	if !request.OK {
		t.Fatal("inner operation did not suspend")
		return
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	stepUntilCurrent(engine, node)
	if !node.Current || len(node.Latest.List) != 1 || node.Latest.List[0].Int != 42 {
		t.Error("outer operation consumed the suspended inner operation's operands")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func checkRetainedOperands(t *testing.T, source string) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "memo.km", source)
	p := eval.Compile(a, engine, parsed, registry)
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	request := stepUntilRequest(engine, p)
	if !request.OK || state.Calls != 1 || state.Effects != 0 {
		t.Fatal("first operand did not complete before suspension")
		return
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	stepUntilCurrent(engine, node)
	if !node.Current || len(node.Latest.List) != 2 || node.Latest.List[0].Int != 1 || state.Calls != 1 || state.Effects != 1 {
		t.Error("completed operand or its staged effect was replayed incorrectly")
	}
	engine.Invalidate(node)
	request = stepUntilRequest(engine, p)
	if !request.OK || state.Calls != 2 {
		t.Fatal("new generation reused an old intermediate")
		return
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	stepUntilCurrent(engine, node)
	if !node.Current || node.Latest.List[0].Int != 2 || state.Calls != 2 || state.Effects != 2 {
		t.Error("new generation did not compute exactly once")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

// Bound the wait to catch hangs, without depending on alias/dependency depth.
func stepUntilRequest(engine *core.Engine, p *eval.Program) host.NextResult {
	for i := 0; i < 64; i++ {
		engine.Step()
		request := p.Requests.Next()
		if request.OK {
			return request
		}
	}
	return host.NextResult{}
}

func stepUntilCurrent(engine *core.Engine, node *core.Node) {
	for i := 0; i < 64 && !node.Current && node.Diagnostic.Code == ""; i++ {
		engine.Step()
	}
}
