package lib_test

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

type observedFileState struct {
	Program    *eval.Program
	Expression *expr.Expr
}

func evaluateObservedFile(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*observedFileState)
	context := eval.Context{Program: state.Program, Scope: state.Program.Scope, Run: c.Allocator(), Engine: c, Cwd: "project", DirectHostRequests: true, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}}
	result := state.Program.EvaluateWith(state.Expression, &context)
	if result.Diagnostic.Code != "" {
		c.Fail(result.Diagnostic)
		result.Value.Free(c.Allocator())
		return core.ProducerFailed
	}
	if result.Waiting {
		return core.ProducerWaiting
	}
	c.Publish(result.Value)
	return core.ProducerCompleted
}

func TestFileReadsObserveContentExistenceAndMetadataSeparately(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "test", "[(read \"./input\") (exists? \"input\") (stat \"./input\")]")
	state := mem.Alloc[observedFileState](a)
	state.Program, state.Expression = program, document.Expr
	node := engine.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "files"}, evaluateObservedFile, state)
	engine.Request(node)
	for i := 0; i < 20 && !node.Current; i++ {
		engine.Step()
		next := program.Requests.Next()
		if !next.OK {
			continue
		}
		request := next.Request
		value := core.Value{Kind: core.Bool, Bool: true}
		op := host.PayloadText(request.Payload, host.FieldOp)
		if op == host.OpRead {
			value = core.NewBytes(a, []byte("A"))
		}
		if op == host.OpStat {
			value = core.NewRecord(a, []core.RecordField{{Key: "size", Value: core.Value{Kind: core.Int, Int: 1}}})
		}
		engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, HasValue: true, Value: value})
		request.Free(a)
	}
	if !node.Current {
		t.Error("file observations did not complete")
	}
	content, existence, metadata := false, false, false
	for i := range node.Observations {
		item := node.Observations[i]
		if item.Key.Kind != core.ResourceFile {
			continue
		}
		if item.Key.Name != "project/input" {
			t.Error("host observation differs from canonical graph path")
		}
		if item.Signature.Mode != core.SignatureContent {
			t.Error("compatible resource views conflicted")
		}
		if item.Aspect == core.ObservationContent {
			content = item.Signature.Equal(core.ContentSignature([]byte("A")))
		}
		if item.Aspect == core.ObservationExistence {
			existence = true
		}
		if item.Aspect == core.ObservationMetadata {
			metadata = true
		}
	}
	if !content || !existence || !metadata {
		t.Error("consumed file view was not observed")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
	document.Free()
	mem.Free(a, state)
}

func TestFailedReadObservationIsNotMissingOrReusable(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "test", "(read \"./input\")")
	state := mem.Alloc[observedFileState](a)
	state.Program, state.Expression = program, document.Expr
	node := engine.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "failed-read"}, evaluateObservedFile, state)
	engine.Request(node)
	engine.Step()
	next := program.Requests.Next()
	if !next.OK {
		t.Error("read did not request the host")
	} else {
		request := next.Request
		engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Diagnostic: core.Diagnostic{Code: "FS_ERR"}})
		request.Free(a)
		engine.Step()
		engine.Step()
	}
	found := false
	for i := range node.Observations {
		item := node.Observations[i]
		if item.Key.Kind != core.ResourceFile {
			continue
		}
		found = true
		if item.Key.Name != "project/input" || item.Aspect != core.ObservationContent || item.Signature.Mode != core.SignatureUnavailable || item.Signature.Equal(item.Signature) {
			t.Error("failed read has a reusable or missing identity")
		}
	}
	if !found || node.Diagnostic.Code != "FS_ERR" {
		t.Error("host read failure lost its observation or diagnostic")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
	document.Free()
	mem.Free(a, state)
}
