package evalcheck

import (
	"kame/core"
	"kame/host"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
)

func TestKashLoopTransfersSkipStatementsAndRetainCleanupProgress(t *testing.T) {
	texts := []string{
		"def probe\n\tfor item in [1 2 3]\n\t\tlet value = (tick)\n\t\tif (eq? item 2)\n\t\t\tcontinue\n\t\tif (eq? item 3)\n\t\t\tbreak\n\t\t(tick)\n\treturn 0\nresult = (probe)\n",
		"def probe\n\tfor item in [1 2]\n\t\twith\n\t\t\tdefer (async)\n\t\t\tlet value = (tick)\n\t\t\tcontinue\n\t\t(tick)\n\treturn 0\nresult = (probe)\n",
	}
	for i := range texts {
		a := t.Allocator()
		engine := core.NewEngine(a)
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		state := operationState{}
		registry.Add(eval.Operation{Name: "tick", Call: countOperation, Context: &state, MinArity: 0, MaxArity: 0})
		registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
		parsed := script.ParseKash(a, "transfers.kash", texts[i])
		if len(parsed.Diagnostics) != 0 {
			t.Fatal("invalid transfer test source")
			return
		}
		p := eval.Compile(a, engine, parsed, registry)
		node := p.Definition("result")
		engine.Request(node)
		custom := 0
		for j := 0; j < 128 && !node.Current && node.Diagnostic.Code == ""; j++ {
			request := stepUntilRequest(engine, p)
			if !request.OK {
				break
			}
			if request.Request.Kind == host.RequestCustom {
				custom++
			} else if request.Request.Kind != host.RequestMonotonicTime {
				t.Error("transfer created an unexpected host request")
			}
			engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, Value: core.Value{Kind: core.Int}})
			request.Request.Free(a)
		}
		stepUntilCurrent(engine, node)
		expected := 4
		if i == 1 {
			expected = 2
		}
		if !node.Current || node.Diagnostic.Code != "" || state.Calls != expected || (i == 1 && custom != 2) {
			t.Errorf("transfer lost its target or replayed statements/cleanup: calls=%d custom=%d diagnostic=%s", state.Calls, custom, node.Diagnostic.Code)
		}
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}
}

func TestKashLoopTransfersRejectEvaluationWithoutALocalLoop(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.ParseKash(a, "transfer.kash", "for item in [1]\n\tbreak\n\tcontinue\n")
	p := eval.Compile(a, engine, parsed, registry)
	for i := range parsed.Items[0].Expression.Items[0].Body {
		context := eval.Context{Program: p, Scope: p.Scope, Run: a}
		r := p.EvaluateWith(parsed.Items[0].Expression.Items[0].Body[i], &context)
		if r.Diagnostic.Code != "PHASE_INVALID" {
			t.Error("unmatched trusted-AST transfer escaped evaluation")
		}
		r.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
