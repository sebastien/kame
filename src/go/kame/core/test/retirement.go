package core_test

import (
	"kame/core"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func retirementSource(c *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	c.Publish(core.NewString(c.Allocator(), "done"))
	return core.ProducerCompleted
}

func retirementConsumer(c *core.EngineContext, id int64) core.ProducerResult {
	_ = id
	if !c.Dependency(core.ResourceKey{Kind: core.ResourceDefinition, Name: "source"}) {
		return core.ProducerWaiting
	}
	c.Publish(core.Value{})
	return core.ProducerCompleted
}

func TestRetirementRequiresReleasedRootsAndConsumerEdges(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	source := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "source"}, retirementSource, nil)
	consumer := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "consumer"}, retirementConsumer, nil)
	root := e.RequestRoot(consumer)
	for i := 0; i < 10 && !consumer.Current; i++ {
		e.Step()
	}
	if !consumer.Current || e.Retire(source) || e.Retire(consumer) {
		t.Error("retired observed execution")
	}
	e.Release(root)
	if e.Retire(source) {
		t.Error("retired a node still named by a consumer edge")
	}
	if !e.Retire(consumer) || !e.Retire(source) {
		t.Error("could not retire released terminal execution")
	}
	nodes := e.TrackedNodes()
	if len(nodes) != 0 || e.Lookup(core.ResourceKey{Kind: core.ResourceDefinition, Name: "source"}) != nil {
		t.Error("retired node remains indexed")
	}
	slices.Free(a, nodes)
	e.Free()
}
