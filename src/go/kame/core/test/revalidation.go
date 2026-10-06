package core_test

import (
	"kame/core"
	"solod.dev/so/testing"
)

type revalidationProducer struct {
	Dependency        string
	Second            string
	Ordered           bool
	Text              string
	Runs              int
	Fail              bool
	Live              bool
	Unavailable       bool
	UnavailableResult bool
}

func publishRevalidated(c *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	state := c.Context().(*revalidationProducer)
	if state.Dependency != "" {
		key := core.ResourceKey{Kind: core.ResourceFile, Name: state.Dependency}
		ready := false
		if state.Ordered {
			ready = c.OrderDependency(key)
		} else {
			ready = c.Dependency(key)
		}
		if !ready {
			return core.ProducerWaiting
		}
		if !state.Ordered {
			c.Value(key)
		}
	}
	if state.Second != "" {
		key := core.ResourceKey{Kind: core.ResourceFile, Name: state.Second}
		if !c.Dependency(key) {
			return core.ProducerWaiting
		}
		c.Value(key)
	}
	state.Runs++
	if state.Unavailable {
		c.Observe(core.ResourceKey{Kind: core.ResourceOperation, Name: "opaque"}, core.Signature{})
	}
	if state.Fail {
		c.Fail(core.Diagnostic{Code: "TEST_FAILED"})
		return core.ProducerFailed
	}
	if state.UnavailableResult {
		c.PublishSigned(core.NewString(c.Allocator(), state.Text), core.Signature{})
	} else {
		c.Publish(core.NewString(c.Allocator(), state.Text))
	}
	if state.Live {
		return core.ProducerWaiting
	}
	return core.ProducerCompleted
}

func TestRevalidationRejectsUnavailableObservationsAndResults(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	sourceState := revalidationProducer{Text: "A"}
	rootState := revalidationProducer{Dependency: "source", Text: "root", Unavailable: true}
	source := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "source"}, publishRevalidated, &sourceState)
	e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "root"}, publishRevalidated, &rootState)
	e.Request(e.Lookup(core.ResourceKey{Kind: core.ResourceFile, Name: "root"}))
	for i := 0; i < 12; i++ {
		e.Step()
	}
	e.Revalidate(source)
	for i := 0; i < 12; i++ {
		e.Step()
	}
	if rootState.Runs != 2 {
		t.Error("unavailable observation without a graph edge proved reuse")
	}
	rootState.Unavailable, rootState.UnavailableResult = false, true
	e.Revalidate(source)
	for i := 0; i < 12; i++ {
		e.Step()
	}
	e.Revalidate(source)
	for i := 0; i < 12; i++ {
		e.Step()
	}
	if rootState.Runs != 4 {
		t.Error("unavailable result signature proved reuse")
	}
	e.Free()
}

func TestRevalidationMixedDiamondAndOverlappingNotifications(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	sourceState := revalidationProducer{Text: "A"}
	otherState := revalidationProducer{Text: "other"}
	orderedState := revalidationProducer{Dependency: "source", Text: "ordered", Ordered: true}
	normalState := revalidationProducer{Dependency: "source", Text: "normal"}
	rootState := revalidationProducer{Dependency: "ordered", Second: "normal", Text: "root"}
	source := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "source"}, publishRevalidated, &sourceState)
	other := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "other"}, publishRevalidated, &otherState)
	e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "ordered"}, publishRevalidated, &orderedState)
	normal := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "normal"}, publishRevalidated, &normalState)
	root := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "root"}, publishRevalidated, &rootState)
	normalState.Second = "other"
	e.Request(root)
	for i := 0; i < 24; i++ {
		e.Step()
	}
	generation := root.Generation
	sourceState.Text = "B"
	otherState.Text = "changed"
	e.Revalidate(source)
	e.Revalidate(other)
	for i := 0; i < 24; i++ {
		e.Step()
	}
	if orderedState.Runs != 1 || normalState.Runs != 2 || rootState.Runs != 1 || root.Generation != generation || root.ValidationPending {
		t.Error("mixed diamond or overlapping refreshes propagated equal results")
	}
	// Duplicate notifications arriving before dispatch must not throw away
	// accepted consumers while the same resource is already queued for refresh.
	e.Revalidate(source)
	e.Revalidate(source)
	for i := 0; i < 24; i++ {
		e.Step()
	}
	if normalState.Runs != 2 || rootState.Runs != 1 || root.Generation != generation {
		t.Error("duplicate queued refresh discarded accepted downstream state")
	}
	// Rebinding a changed branch must remove the old source's reverse edge.
	normalState.Dependency = "other"
	otherState.Text = "new branch"
	e.Revalidate(other)
	for i := 0; i < 24; i++ {
		e.Step()
	}
	for i := range source.Dependents {
		if source.Dependents[i] == normal {
			t.Error("branch switch retained obsolete source edge")
		}
	}
	e.Free()
}

func TestRevalidationSuppressesEqualResultsAcrossSettledGraph(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	sourceState := revalidationProducer{Text: "A"}
	middleState := revalidationProducer{Dependency: "source", Text: "fixed", Live: true}
	rootState := revalidationProducer{Dependency: "middle", Text: "root"}
	source := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "source"}, publishRevalidated, &sourceState)
	e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "middle"}, publishRevalidated, &middleState)
	root := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "root"}, publishRevalidated, &rootState)
	e.Request(root)
	for i := 0; i < 12; i++ {
		e.Step()
	}
	if root.State != core.NodeComplete || sourceState.Runs != 1 || middleState.Runs != 1 || rootState.Runs != 1 {
		t.Fatal("initial graph did not settle")
		e.Free()
		return
	}
	generation := root.Generation
	e.Revalidate(source)
	if !root.ValidationPending || !root.Current || root.Generation != generation {
		t.Error("revalidation discarded accepted downstream state eagerly")
	}
	for i := 0; i < 12; i++ {
		e.Step()
	}
	if root.ValidationPending || sourceState.Runs != 2 || middleState.Runs != 1 || rootState.Runs != 1 {
		t.Error("equal source result propagated")
	}
	sourceState.Text = "B"
	e.Revalidate(source)
	for i := 0; i < 12; i++ {
		e.Step()
	}
	if middleState.Runs != 2 || rootState.Runs != 1 || root.Generation != generation {
		t.Error("equal intermediate result propagated downstream")
	}
	middleState.Text = "different"
	sourceState.Text = "C"
	e.Revalidate(source)
	for i := 0; i < 12; i++ {
		e.Step()
	}
	if middleState.Runs != 3 || rootState.Runs != 2 || root.ValidationPending {
		t.Error("changed intermediate result did not propagate")
	}
	root.DisableReuse = true
	e.Revalidate(source)
	for i := 0; i < 12; i++ {
		e.Step()
	}
	if rootState.Runs != 3 {
		t.Error("always consumer was reused")
	}
	sourceState.Fail = true
	e.Revalidate(source)
	for i := 0; i < 12; i++ {
		e.Step()
	}
	if root.State != core.NodeFailed || root.ValidationPending {
		t.Error("failed refreshed source left a stale accepted root")
	}
	e.Free()
}

func submitRevalidationConsumer(c *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	key := core.ResourceKey{Kind: core.ResourceFile, Name: "source"}
	if !c.Dependency(key) {
		return core.ProducerWaiting
	}
	c.Value(key)
	c.Submit(77)
	return core.ProducerSubmitted
}

func TestRevalidationCancelsActiveConsumersBeforeResampling(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	state := revalidationProducer{Text: "A"}
	source := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "source"}, publishRevalidated, &state)
	root := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "active"}, submitRevalidationConsumer, nil)
	e.Request(root)
	for i := 0; i < 6; i++ {
		e.Step()
	}
	generation := root.Generation
	if !root.Submitted {
		t.Error("consumer did not submit")
	}
	e.Revalidate(source)
	cancel := e.NextCancellation()
	if root.Submitted || root.Generation == generation || cancel.RequestID != 77 || cancel.NodeID != root.ID {
		t.Error("revalidation retained stale in-flight host work")
	}
	e.Free()
}

func rejectSpeculativeDependencies(c *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	key := core.ResourceKey{Kind: core.ResourceFile, Name: "source"}
	if !c.OrderDependency(key) {
		return core.ProducerWaiting
	}
	checkpoint := c.CheckpointDependencies()
	c.TryDependency(key)
	c.Value(key)
	failed := core.ResourceKey{Kind: core.ResourceFile, Name: "failed"}
	if c.TryDependency(failed) {
		c.DependencyDiagnostic(failed)
	}
	c.RestoreDependencies(&checkpoint)
	c.Publish(core.NewString(c.Allocator(), "accepted"))
	return core.ProducerCompleted
}

func TestRejectedReuseRestoresDependenciesAndObservations(t *testing.T) {
	e := core.NewEngine(t.Allocator())
	sourceState := revalidationProducer{Text: "A"}
	failureState := revalidationProducer{Fail: true}
	source := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "source"}, publishRevalidated, &sourceState)
	failed := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "failed"}, publishRevalidated, &failureState)
	e.Request(failed)
	e.Step()
	root := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "root"}, rejectSpeculativeDependencies, nil)
	e.Request(root)
	for i := 0; i < 8; i++ {
		e.Step()
	}
	if root.State != core.NodeComplete || len(root.Dynamic) != 1 || root.Dynamic[0] != source || len(root.OrderOnly) != 1 || root.OrderOnly[0] != source || len(root.Observed) != 0 || len(root.Observations) != 0 || len(failed.Dependents) != 0 || failed.Interest != 1 {
		t.Error("rejected reuse retained speculative edges, reads or failure interest")
	}
	e.Free()
}
