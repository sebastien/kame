package corecheck

import (
	"kame/core"
	"kame/internal/mem"
	"kame/internal/testing"
)

func publishingNode(e *core.Engine, a mem.Allocator, name string, dependency string) (*core.Node, *producerState) {
	state := mem.Alloc[producerState](a)
	state.Alloc, state.Text = a, name
	if dependency != "" {
		state.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: dependency}
	}
	return e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: name}, publishOnce, state), state
}

// A suspended root's work is not offered; resuming runs it once, as if it
// had never been suspended.
func TestSuspendedRootIsNotOffered(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n, state := publishingNode(e, a, "job", "")
	r := e.RequestRoot(n)
	e.Suspend(r)
	if !e.Suspended(n) || len(e.Ready(4)) != 0 || e.Step() != nil {
		t.Error("suspended root was offered")
	}
	e.Resume(r)
	if e.Suspended(n) {
		t.Error("resumed root is still suspended")
	}
	for i := 0; i < 4 && n.State != core.NodeComplete; i++ {
		e.Step()
	}
	if n.State != core.NodeComplete || n.Latest.Text != "job" || state.Runs != 1 {
		t.Errorf("resumed root: state=%d value=%q runs=%d", n.State, n.Latest.Text, state.Runs)
	}
	e.Release(r)
	e.Free()
	mem.Free(a, state)
}

// Suspension never delays another root: a dependency shared with a live root
// keeps running, while the suspended root's own node waits.
func TestSuspendedRootKeepsSharedDependencyRunning(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	shared, sharedState := publishingNode(e, a, "shared", "")
	left, leftState := publishingNode(e, a, "left", "")
	right, rightState := publishingNode(e, a, "right", "")
	e.AddStatic(left, shared)
	e.AddStatic(right, shared)
	first, second := e.RequestRoot(left), e.RequestRoot(right)
	e.Suspend(first)
	if e.Suspended(shared) || !e.Suspended(left) || e.Suspended(right) {
		t.Error("suspension did not follow interest")
	}
	for i := 0; i < 8; i++ {
		e.Step()
	}
	if shared.State != core.NodeComplete || right.State != core.NodeComplete {
		t.Error("live root was delayed by a suspended one")
	}
	if left.State == core.NodeComplete {
		t.Error("suspended root completed")
	}
	e.Release(second)
	if !e.Suspended(shared) {
		t.Error("dependency of only a suspended root is runnable")
	}
	e.Resume(first)
	for i := 0; i < 8 && left.State != core.NodeComplete; i++ {
		e.Step()
	}
	if left.State != core.NodeComplete || leftState.Runs != 1 {
		t.Errorf("resumed root: state=%d runs=%d", left.State, leftState.Runs)
	}
	e.Release(first)
	e.Free()
	mem.Free(a, sharedState)
	mem.Free(a, leftState)
	mem.Free(a, rightState)
}

// Submitted host work receives one suspension and one resume request;
// repeated calls add none. A completion that arrives while suspended is
// kept and consumed after resume.
func TestSuspensionRequestsHostWork(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "job"}, waitForCompletion, state)
	r := e.RequestRoot(n)
	e.Step()
	e.Suspend(r)
	e.Suspend(r)
	if s := e.NextSuspension(); s.NodeID != n.ID || s.RequestID != 1 || !s.Suspend {
		t.Errorf("suspension request: %+v", s)
	}
	if s := e.NextSuspension(); s.RequestID != 0 {
		t.Errorf("repeated suspension queued %+v", s)
	}
	e.Complete(core.Completion{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: 1, Value: core.NewString(a, "done"), HasValue: true})
	for i := 0; i < 4; i++ {
		e.Step()
	}
	if n.State == core.NodeComplete || state.Runs != 1 {
		t.Error("a suspended node consumed its completion")
	}
	e.Resume(r)
	e.Resume(r)
	if s := e.NextSuspension(); s.RequestID != 0 {
		t.Errorf("completed host work was resumed: %+v", s)
	}
	for i := 0; i < 4 && n.State != core.NodeComplete; i++ {
		e.Step()
	}
	if n.State != core.NodeComplete || n.Latest.Text != "done" {
		t.Errorf("resumed completion: state=%d value=%q", n.State, n.Latest.Text)
	}
	e.Release(r)
	e.Free()
	mem.Free(a, state)
}

// Resume requests are queued for work still submitted, and a host stop never
// suspends a root by itself (the owner decides).
func TestResumeRequestsSubmittedWork(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "job"}, waitForCompletion, state)
	r := e.RequestRoot(n)
	e.Step()
	e.Suspend(r)
	e.NextSuspension()
	e.Resume(r)
	if s := e.NextSuspension(); s.NodeID != n.ID || s.RequestID != 1 || s.Suspend {
		t.Errorf("resume request: %+v", s)
	}
	if !r.Reaches(n) || r.Node() != n {
		t.Error("root does not reach its node")
	}
	e.Release(r)
	e.Free()
	mem.Free(a, state)
}

// Cancelling a suspended root resumes its host work and cancels it; late
// completions are rejected.
func TestCancelSuspendedRoot(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "job"}, waitForCompletion, state)
	r := e.RequestRoot(n)
	e.Step()
	e.Suspend(r)
	e.NextSuspension()
	generation, attempt := n.Generation, n.Attempt
	e.Release(r)
	if s := e.NextSuspension(); s.RequestID != 1 || s.Suspend {
		t.Errorf("cancelled suspended work was not resumed: %+v", s)
	}
	if c := e.NextCancellation(); c.NodeID != n.ID || c.RequestID != 1 {
		t.Errorf("cancelled suspended work: %+v", c)
	}
	e.Complete(core.Completion{NodeID: n.ID, Generation: generation, Attempt: attempt, RequestID: 1, Value: core.NewString(a, "late"), HasValue: true})
	e.Step()
	if n.State == core.NodeComplete {
		t.Error("late completion accepted after cancellation")
	}
	e.Free()
	mem.Free(a, state)
}
