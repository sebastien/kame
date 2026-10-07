package eval_test

import (
	"kame/core"
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

func TestFunctionArgumentsSurviveSiblingWait(t *testing.T) {
	checkRetainedOperands(t, "(pair first second) = (list first second)\nresult = (pair (once) (async))")
}

func TestRecreatedLexicalScopeRetainsCompletedOperands(t *testing.T) {
	checkRetainedOperands(t, "result = (let [local 1] (list (once) (async)))")
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
	engine.Step()
	request := p.Requests.Next()
	if !request.OK || state.Calls != 1 || state.Effects != 0 {
		t.Fatal("first operand did not complete before suspension")
		return
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	engine.Step()
	engine.Step()
	if !node.Current || len(node.Latest.List) != 2 || node.Latest.List[0].Int != 1 || state.Calls != 1 || state.Effects != 1 {
		t.Error("completed operand or its staged effect was replayed incorrectly")
	}
	engine.Invalidate(node)
	engine.Step()
	request = p.Requests.Next()
	if !request.OK || state.Calls != 2 {
		t.Fatal("new generation reused an old intermediate")
		return
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	request.Request.Free(a)
	engine.Step()
	engine.Step()
	if !node.Current || node.Latest.List[0].Int != 2 || state.Calls != 2 || state.Effects != 2 {
		t.Error("new generation did not compute exactly once")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
