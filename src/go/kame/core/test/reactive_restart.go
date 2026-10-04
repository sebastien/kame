package core_test

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

type rebindingConsumer struct {
	Primary  core.ResourceKey
	Optional core.ResourceKey
}

func consumeChangingInputs(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*rebindingConsumer)
	if !c.Dependency(state.Primary) {
		return core.ProducerWaiting
	}
	value := c.Value(state.Primary)
	if value.Value.Int == 1 {
		if !c.Dependency(state.Optional) {
			return core.ProducerWaiting
		}
		c.Submit(9)
		return core.ProducerSubmitted
	}
	c.Publish(value.Value.Clone(c.Allocator()))
	return core.ProducerWaiting
}

func testReactiveRebinding(t *testing.T, cancel bool) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	defer engine.Free()
	primary := addSource(engine, "a-primary", core.NewSource(a, contextPoll, freeContext, nil))
	optional := addSource(engine, "b-optional", core.NewSource(a, contextPoll, freeContext, nil))
	state := mem.Alloc[rebindingConsumer](a)
	defer mem.Free(a, state)
	state.Primary, state.Optional = primary.Key, optional.Key
	consumer := engine.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "c-consumer"}, consumeChangingInputs, state)
	engine.Request(consumer)
	for step := 0; step < 16 && !primary.Submitted; step++ {
		engine.Step()
	}
	engine.Complete(core.Completion{NodeID: primary.ID, Generation: primary.Generation, Attempt: primary.Attempt, RequestID: primary.HostRequestID, HasValue: true, Value: core.Value{Kind: core.Int, Int: 1}})
	for step := 0; step < 16 && !optional.Submitted; step++ {
		engine.Step()
	}
	engine.Complete(core.Completion{NodeID: optional.ID, Generation: optional.Generation, Attempt: optional.Attempt, RequestID: optional.HostRequestID, HasValue: true, Value: core.Value{Kind: core.Int, Int: 5}})
	for step := 0; step < 16 && (!consumer.Submitted || !primary.Submitted || !optional.Submitted); step++ {
		engine.Step()
	}
	if !consumer.Submitted {
		t.Fatal("consumer did not submit on the first complete input set")
		return
	}
	oldGeneration := consumer.Generation
	engine.Complete(core.Completion{NodeID: primary.ID, Generation: primary.Generation, Attempt: primary.Attempt, RequestID: primary.HostRequestID, HasValue: true, Value: core.Value{Kind: core.Int, Int: 2}})
	for step := 0; step < 16 && consumer.Generation == oldGeneration; step++ {
		engine.Step()
	}
	if consumer.Generation == oldGeneration || primary.State == core.NodeCancelled || optional.State == core.NodeCancelled {
		t.Error("reactive restart cancelled sources before rediscovering dependencies")
	}
	if cancel {
		engine.Cancel(consumer)
		if primary.Interest != 0 || optional.Interest != 0 || primary.State != core.NodeCancelled || optional.State != core.NodeCancelled {
			t.Error("last consumer did not release retained dependency interest")
		}
		return
	}
	for step := 0; step < 16 && !consumer.Current; step++ {
		engine.Step()
	}
	if !consumer.Current || consumer.Latest.Int != 2 || len(consumer.Dynamic) != 1 || primary.Interest != 1 || optional.Interest != 0 || optional.State != core.NodeCancelled {
		t.Error("restart failed to adopt the retained source or release an obsolete branch")
	}
}

func TestReactiveRestartRebindsAndReleasesObsoleteDependencies(t *testing.T) {
	testReactiveRebinding(t, false)
}

func TestCancellationReleasesDependenciesHeldDuringReactiveRebinding(t *testing.T) {
	testReactiveRebinding(t, true)
}
