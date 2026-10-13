package core_test

import (
	"kame/core"
	"kame/diagnostic"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func ownedFailure(a mem.Allocator, message string) core.Diagnostic {
	d := diagnostic.Diagnostic{
		Code:     core.DiagnosticHostFailure,
		Severity: diagnostic.Error,
		Message:  message,
		Frames:   []diagnostic.Frame{{Kind: "host", Label: "job", Source: "test"}},
		Notes:    []string{"detail"},
	}
	return d.Clone(a)
}

func TestInvalidationDiscardsQueuedOwnedTerminalDiagnostics(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	defer e.Free()
	n := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "job"}, nil, nil)
	early := e.Subscribe(n)
	e.Publish(n, core.NewString(a, "retained"))
	e.Fail(n, ownedFailure(a, "obsolete"))
	late := e.Subscribe(n)
	e.Invalidate(n)
	value := early.Next()
	if !value.HasValue() || value.Value.Text != "retained" {
		t.Error("invalidation discarded an independently owned queued value")
	}
	value.Value.Free(a)
	if early.Next().Kind != core.UpdateInvalidated || late.Next().Kind != core.UpdateInvalidated {
		t.Error("invalidation retained an obsolete terminal")
	}
	if early.Next().Kind != core.UpdateNone || late.Next().Kind != core.UpdateNone {
		t.Error("obsolete terminal remained queued after invalidation")
	}
	e.Fail(n, ownedFailure(a, "replacement"))
	first, second := early.Next(), late.Next()
	if first.Kind != core.UpdateFailed || second.Kind != core.UpdateFailed ||
		first.Diagnostic.Message != "replacement" || second.Diagnostic.Frames[0].Label != "job" {
		t.Error("replacement generation did not deliver its borrowed diagnostic")
	}
}

func TestUnknownCompletionReleasesOwnedDiagnostic(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	e.Complete(core.Completion{NodeID: 99, HasValue: true, Value: core.NewString(a, "discarded"), Diagnostic: ownedFailure(a, "unknown")})
	if e.Step() != nil {
		t.Error("unknown completion was accepted")
	}
	e.Free()
}

func TestAcceptedCompletionCancelReleasesOwnedDiagnostic(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "job", core.NewSource(a, contextPoll, freeContext, nil))
	e.Request(n)
	e.Step()
	e.Complete(core.Completion{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: 7, HasValue: true, Value: core.NewString(a, "discarded"), Diagnostic: ownedFailure(a, "cancelled")})
	if e.Step() != n || !n.HasCompletion {
		t.Error("matching completion was not accepted before cancellation")
	}
	e.Cancel(n)
	if n.State != core.NodeCancelled || n.HasCompletion {
		t.Error("cancellation retained the accepted completion")
	}
	e.Free()
}

func TestAcceptedCompletionEngineFreeReleasesOwnedDiagnostic(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "job", core.NewSource(a, contextPoll, freeContext, nil))
	e.Request(n)
	e.Step()
	e.Complete(core.Completion{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: 7, HasValue: true, Value: core.NewString(a, "discarded"), Diagnostic: ownedFailure(a, "teardown")})
	if e.Step() != n || !n.HasCompletion {
		t.Error("matching completion was not accepted before teardown")
	}
	e.Free()
}
