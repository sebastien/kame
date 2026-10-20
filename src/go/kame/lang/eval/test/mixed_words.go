package eval_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestMixedWordRetainsCompletedFragmentsAcrossSuspension(t *testing.T) {
	sources := []string{
		`result = $(printf %s prefix-(once)-(async).txt)`,
		`result = $(printf(once)(async) value)`,
		`result = $(printf value > ./prefix-(once)-(async).txt)`,
		`result = $(:cwd ./prefix-(once)-(async) printf value)`,
		`result = $(:timeout (once).(async) printf value)`,
		`result = $(:MODE prefix-(once)-(async) printf value)`,
	}
	for i := range sources {
		checkMixedWordRetainedFragments(t, sources[i])
	}
}

func TestInterpolationRetainsCompletedCallsAcrossSuspension(t *testing.T) {
	sources := []string{
		`result = "prefix-@(once)-@(async).txt"`,
		`result = (render "plain" """prefix-@(once)-@(async).txt""")`,
	}
	for i := range sources {
		a := t.Allocator()
		engine := core.NewEngine(a)
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		state := memoCountState{}
		registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
		registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
		parsed := script.Parse(a, "interpolation.km", sources[i])
		p := eval.Compile(a, engine, parsed, registry)
		p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
		node := p.Definition("result")
		engine.Request(node)
		request := stepUntilRequest(engine, p)
		if !request.OK || state.Calls != 1 {
			t.Error("interpolation did not suspend after its first call")
		} else {
			engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
			request.Request.Free(a)
			stepUntilCurrent(engine, node)
			if !node.Current || node.Latest.Text != "prefix-1-42.txt" || state.Calls != 1 || state.Effects != 1 {
				t.Error("interpolation replayed a completed call or effect")
			}
		}
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}
}

func checkMixedWordRetainedFragments(t *testing.T, source string) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "words.km", source)
	p := eval.Compile(a, engine, parsed, registry)
	p.SetGrants([]eval.Grant{{Capability: eval.Run}, {Capability: eval.Write}})
	p.DryRun = true
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	for generation := 1; generation <= 2; generation++ {
		request := stepUntilRequest(engine, p)
		if !request.OK || request.Request.Kind == host.RequestProcess || state.Calls != generation || state.Effects != generation-1 {
			t.Error("word fragment did not suspend before process launch")
			break
		}
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
		request.Request.Free(a)
		stepUntilCurrent(engine, node)
		if !node.Current || state.Calls != generation || state.Effects != generation {
			t.Error("completed fragment or effect replayed")
		}
		if generation == 1 {
			engine.Invalidate(node)
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
