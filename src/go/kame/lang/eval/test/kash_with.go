package eval_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestKashWithSetupRetainsCompletedWorkAcrossSuspension(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKash(a, "with.kash", "defn probe\n\twith :cwd (let [unused (once)] \"./scope\") :env (let [unused (list (once) (async))] [MODE: \"new\"])\n\t\t(once)\n\treturn 0\nresult = (probe)\n")
	p := eval.Compile(a, engine, parsed, registry)
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	request := stepUntilRequest(engine, p)
	if !request.OK || state.Calls != 2 {
		t.Error("with setup did not evaluate left to right before suspension")
	}
	if request.OK {
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
		request.Request.Free(a)
		stepUntilCurrent(engine, node)
	}
	if !node.Current || node.Diagnostic.Code != "" || state.Calls != 3 || state.Effects != 3 {
		t.Error("with replayed setup work or lost effects: " + node.Diagnostic.Code)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

type withBudgetState struct {
	Now       int64
	Remaining int64
	Calls     int
}

func withBudgetNow(value any) int64 {
	state := value.(*withBudgetState)
	return state.Now
}
func withBudgetAdvance(c *eval.Context, value any, args []core.Value) eval.Result {
	_, _ = c, args
	state := value.(*withBudgetState)
	state.Now += 4000000000
	return eval.Result{}
}
func withBudgetInspect(c *eval.Context, value any, args []core.Value) eval.Result {
	_ = args
	state := value.(*withBudgetState)
	state.Remaining, state.Calls = c.TimeoutMS, state.Calls+1
	return eval.Result{}
}

func TestKashWithCannotRestartAnEnclosingCleanupBudget(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := withBudgetState{}
	registry.Add(eval.Operation{Name: "advance", Call: withBudgetAdvance, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "budget", Call: withBudgetInspect, Context: &state, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKash(a, "budget.kash", "defn cleanup\n\twith\n\t\tdefer (budget)\n\t\t(advance)\ndefn probe\n\tdefer (cleanup)\n\treturn 0\nresult = (probe)\n")
	p := eval.Compile(a, engine, parsed, registry)
	p.FinalizationNow, p.FinalizationHost = withBudgetNow, &state
	node := p.Definition("result")
	engine.Request(node)
	for i := 0; i < 64 && !node.Current && node.Diagnostic.Code == ""; i++ {
		request := stepUntilRequest(engine, p)
		if !request.OK {
			break
		}
		if request.Request.Kind != host.RequestMonotonicTime {
			t.Error("unexpected cleanup host request")
		}
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, Value: core.Value{Kind: core.Int, Int: state.Now}})
		request.Request.Free(a)
	}
	stepUntilCurrent(engine, node)
	if !node.Current || node.Diagnostic.Code != "" || state.Calls != 1 || state.Remaining != 1000 {
		t.Errorf("nested with restarted cleanup budget: remaining=%d calls=%d diagnostic=%s", state.Remaining, state.Calls, node.Diagnostic.Code)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
