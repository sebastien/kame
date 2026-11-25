package evalcheck

import (
	"kame/core"
	legacy "kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"testing"
)

func TestOperandPairAcrossCopiedContextAndSuspension(t *testing.T) {
	a := legacy.Wrap(t).Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	original := eval.Context{Run: a}
	registry.Add(eval.Operation{Name: "reenter", Category: eval.PureOperation, Phases: eval.AllPhases, MinArity: 2, MaxArity: 2, Call: func(_ *eval.Context, _ any, args []core.Value) eval.Result {
		inner := original.EvalText("sub 99 10")
		defer inner.Free(a)
		if inner.Value.Int != 89 {
			t.Fatal("inner call failed:", inner.Diagnostic.Code)
		}
		return eval.Result{Value: core.Value{Kind: core.Int, Int: args[0].Int + args[1].Int}}
	}})
	registry.Add(eval.Operation{Name: "pause", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKameFile(a, "operands.km", "result = sub (add 50 7) (pause ())")
	p := eval.Compile(a, engine, parsed, registry)
	defer func() {
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}()
	original.Program, original.Scope = p, p.Scope
	warm := original.EvalText("add 1 2")
	warm.Free(a)
	copied := original
	check := func() {
		r := copied.EvalText("reenter 19 23")
		defer r.Free(a)
		if r.Diagnostic.Code != "" || r.Value.Int != 42 {
			t.Fatalf("reentrant call overwrote outer operands: got %d (%s)", r.Value.Int, r.Diagnostic.Code)
		}
	}
	check()
	node := p.Definition("result")
	engine.Request(node)
	for attempt := 0; attempt < 2; attempt++ {
		request := stepUntilRequest(engine, p)
		if !request.OK {
			t.Fatal("second operand did not suspend")
		}
		check()
		if attempt == 0 {
			engine.Invalidate(node)
		}
		r := request.Request
		engine.Complete(core.Completion{NodeID: r.NodeID, Generation: r.Generation, Attempt: r.Attempt, RequestID: r.ID})
		r.Free(a)
	}
	stepUntilCurrent(engine, node)
	if !node.Current || node.Latest.Int != 15 {
		t.Fatalf("resumed with the wrong operands: got %d (%s)", node.Latest.Int, node.Diagnostic.Code)
	}
}
