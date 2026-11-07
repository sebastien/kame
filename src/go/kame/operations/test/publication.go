package operations_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

type publicationTestState struct {
	Program    *eval.Program
	Expression *expr.Expr
}

func evaluatePublication(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*publicationTestState)
	context := eval.Context{Program: state.Program, Scope: state.Program.Scope, Run: c.Allocator(), Engine: c, Grants: []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}}}
	r := state.Program.EvaluateWith(state.Expression, &context)
	if r.Diagnostic.Code != "" {
		c.Fail(r.Diagnostic)
		r.Value.Free(c.Allocator())
		return core.ProducerFailed
	}
	if r.Waiting {
		return core.ProducerWaiting
	}
	c.Publish(r.Value)
	return core.ProducerCompleted
}

func TestPublicationRetainsCommitAcrossSuspensionAndPartialFailure(t *testing.T) {
	a := t.Allocator()
	for scenario := 0; scenario < 2; scenario++ {
		engine := core.NewEngine(a)
		registry := eval.NewRegistry(a)
		if !operations.Register(registry) {
			t.Error("library registration failed")
		}
		parsed := script.Parse(a, "publication", "")
		program := eval.Compile(a, engine, parsed, registry)
		text := `(let [result (publish! "./staged" "./final")] (list result (now)))`
		if scenario == 1 {
			text = `(publish! "./staged" "./final")`
		}
		document := expr.Parse(a, "publication", text)
		state := mem.Alloc[publicationTestState](a)
		state.Program, state.Expression = program, document.Expr
		node := engine.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "publication"}, evaluatePublication, state)
		engine.Request(node)
		publications := 0
		for step := 0; step < 20 && node.State != core.NodeComplete && node.State != core.NodeFailed; step++ {
			engine.Step()
			request := program.Requests.Next()
			if !request.OK {
				continue
			}
			r := request.Request
			var value core.Value
			if r.Kind == host.RequestPublishFile {
				if host.PayloadText(r.Payload, host.FieldData) == "" {
					t.Error("publication omitted its wire JSON")
				}
				publications++
				outcome := host.PublishResult{Published: true, StagedRemoved: true}
				if scenario == 1 {
					outcome.Code, outcome.StagedRemoved = "FS_PUBLISHED", false
				}
				value = outcome.Value(a)
			} else if r.Kind == host.RequestWallTime {
				value = core.Value{Kind: core.Int, Int: 123}
			} else {
				t.Error("unexpected publication host request")
			}
			engine.Complete(core.Completion{NodeID: r.NodeID, Generation: r.Generation, Attempt: r.Attempt, RequestID: r.ID, Value: value, HasValue: true})
			r.Free(a)
		}
		if publications != 1 {
			t.Error("publication repeated across suspension")
		}
		if scenario == 0 && node.State != core.NodeComplete {
			t.Error("publication did not resume")
		}
		if scenario == 1 && (node.Diagnostic.Code != "FS_PUBLISHED" || !node.Diagnostic.Cause.HasPublication || !node.Diagnostic.Cause.Published || node.Diagnostic.Cause.StagedRemoved) {
			t.Error("partial publication lost its structured commit outcome")
		}
		engine.Free()
		program.Free()
		parsed.Free()
		document.Free()
		registry.Free()
		mem.Free(a, state)
	}
}
