package core_test

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type orderState struct {
	Dependency core.ResourceKey
	Normal     bool
	Reverse    bool
}

func orderProducer(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	s := c.Context().(*orderState)
	if s.Reverse && !c.Dependency(s.Dependency) {
		return core.ProducerWaiting
	}
	if !c.OrderDependency(s.Dependency) {
		return core.ProducerWaiting
	}
	if s.Normal && !c.Dependency(s.Dependency) {
		return core.ProducerWaiting
	}
	c.Publish(core.Value{Kind: core.Nil})
	return core.ProducerCompleted
}
func orderLeaf(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	c.Publish(core.Value{Kind: core.Nil})
	return core.ProducerCompleted
}
func TestOrderDependencySchedulingAndInvalidation(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	dep := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "before"}, orderLeaf, nil)
	s := mem.Alloc[orderState](a)
	s.Dependency = dep.Key
	parent := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "after"}, orderProducer, s)
	e.Request(parent)
	e.Step()
	if parent.State != core.NodeWaiting || dep.Interest == 0 {
		t.Error("order-only prerequisite did not block and retain interest")
	}
	for i := 0; i < 8 && parent.State != core.NodeComplete; i++ {
		e.Step()
	}
	if parent.State != core.NodeComplete || !slices.Contains(parent.OrderOnly, dep) {
		t.Error("order-only prerequisite did not resume its consumer")
	}
	e.Invalidate(dep)
	if !parent.InvalidatedForOrderOnly || parent.State != core.NodeIdle {
		t.Error("order-only invalidation lost its scheduling-only reason")
	}
	e.Free()
	mem.Free(a, s)
}
func TestNormalDependencyDominatesOrderOnly(t *testing.T) {
	a := t.Allocator()
	for mode := 0; mode < 2; mode++ {
		e := core.NewEngine(a)
		dep := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "before"}, orderLeaf, nil)
		s := mem.Alloc[orderState](a)
		s.Dependency, s.Normal, s.Reverse = dep.Key, true, mode == 1
		parent := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "after"}, orderProducer, s)
		e.Request(parent)
		for i := 0; i < 8 && parent.State != core.NodeComplete; i++ {
			e.Step()
		}
		if parent.State != core.NodeComplete || len(parent.OrderOnly) != 0 {
			t.Error("normal edge was downgraded to order-only")
		}
		e.Invalidate(dep)
		if parent.InvalidatedForOrderOnly {
			t.Error("normal dependency was marked scheduling-only")
		}
		e.Free()
		mem.Free(a, s)
	}
}

func TestOrderInvalidationDiamondUsesNormalPath(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	leaf := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "leaf"}, orderLeaf, nil)
	orderedState := mem.Alloc[orderState](a)
	orderedState.Dependency = leaf.Key
	ordered := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "ordered"}, orderProducer, orderedState)
	normalState := mem.Alloc[orderState](a)
	normalState.Dependency, normalState.Normal = leaf.Key, true
	normal := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "normal"}, orderProducer, normalState)
	top := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "top"}, orderLeaf, nil)
	e.AddStatic(top, ordered)
	e.AddStatic(top, normal)
	e.Request(top)
	for i := 0; i < 16 && top.State != core.NodeComplete; i++ {
		e.Step()
	}
	if top.State != core.NodeComplete {
		t.Error("diamond did not complete")
	}
	e.Invalidate(leaf)
	if !ordered.InvalidatedForOrderOnly || normal.InvalidatedForOrderOnly || top.InvalidatedForOrderOnly {
		t.Error("normal diamond path did not dominate the ordering path")
	}
	e.Free()
	mem.Free(a, orderedState)
	mem.Free(a, normalState)
}

func TestOrderDependencyStillPropagatesFailure(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	dep := e.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "before"}, failProducer, nil)
	s := mem.Alloc[orderState](a)
	s.Dependency = dep.Key
	parent := e.Add(core.ResourceKey{Kind: core.ResourceFile, Name: "after"}, orderProducer, s)
	e.Request(parent)
	for i := 0; i < 8 && parent.State != core.NodeFailed; i++ {
		e.Step()
	}
	if parent.State != core.NodeFailed {
		t.Error("order-only prerequisite failure did not block its consumer")
	}
	e.Free()
	mem.Free(a, s)
}
