package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestKashExitRejectsMissingBoundaryAndFinalizationBeforeOperand(t *testing.T) {
	for mode := 0; mode < 4; mode++ {
		a := t.Allocator()
		engine := core.NewEngine(a)
		registry := eval.NewRegistry(a)
		state := operationState{}
		registry.Add(eval.Operation{Name: "tick", Call: countOperation, Context: &state, MinArity: 0, MaxArity: 0})
		parsed := script.ParseKash(a, "exit.kash", "exit (tick)")
		p := eval.Compile(a, engine, parsed, registry)
		p.ExitBoundary = mode != 0
		context := eval.Context{Program: p, Scope: p.Scope, Run: a, Finalizing: mode == 1, PurePredicate: mode == 2}
		if mode == 3 {
			context.Phase = eval.PlanningPhase
		}
		r := p.EvaluateWith(parsed.Items[0].Expression, &context)
		if r.Diagnostic.Code != "PHASE_INVALID" || state.Calls != 0 {
			t.Error("exit evaluated its operand without runtime exit authority")
		}
		r.Free(a)
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}
}

func TestKashExitOperandRetainsCompletedWorkAcrossSuspension(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKash(a, "exit.kash", "defn stop\n\texit (let [values (list (once) (async))] 7)\nresult = (stop)\n")
	p := eval.Compile(a, engine, parsed, registry)
	p.ExitBoundary = true
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	request := stepUntilRequest(engine, p)
	if !request.OK || state.Calls != 1 {
		t.Error("exit operand did not reach suspension")
	}
	if request.OK {
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
		request.Request.Free(a)
		stepUntilCurrent(engine, node)
	}
	if !node.Diagnostic.Exit.Requested || node.Diagnostic.Exit.Status != 7 || state.Calls != 1 || state.Effects != 1 {
		t.Error("exit operand replayed work or lost typed termination")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestKashExitWaitsForProcessRetirementEvenWithoutDefers(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.ParseKash(a, "exit.kash", "defn stop\n\texit 7\nresult = (stop)\n")
	p := eval.Compile(a, engine, parsed, registry)
	p.ExitBoundary = true
	host := finalizationHost{}
	p.ProcessesQuiet, p.ProcessesQuietState = finalizationQuiescent, &host
	node := p.Definition("result")
	engine.Request(node)
	for i := 0; i < 32; i++ {
		engine.Step()
	}
	if node.Diagnostic.Code != "" || node.Current {
		t.Error("exit completed while cancelled processes were still retiring")
	}
	host.Quiet = true
	stepUntilCurrent(engine, node)
	if !node.Diagnostic.Exit.Requested || node.Diagnostic.Exit.Status != 7 {
		t.Error("exit did not complete after retirement")
	}
	if p.Requests.Next().OK {
		t.Error("exit created a synthetic process or clock request")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
