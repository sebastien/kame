package core

import (
	"slices"
	"testing"
)

// The shortcut from source directly to join must not bypass the long branch;
// the long branch must not wait for the independent host request.
func TestJoinWaitsOnlyForItsInputs(t *testing.T) {
	e := NewEngine(nil)
	defer e.Free()
	key := func(name string) ResourceKey { return ResourceKey{Kind: ResourceDefinition, Name: name} }
	var value int64
	source := e.Add(key("source"), graphProduce, &value)
	fast := e.Add(key("b-fast"), graphProduce, source.Key)
	long := e.Add(key("c-long"), graphProduce, fast.Key)
	slow := e.Add(key("a-slow"), func(c *EngineContext, _ int64) ProducerResult {
		if c.Value(source.Key).Value.Int != 0 && c.Completion().RequestID == 0 {
			c.Submit(42)
			return ProducerSubmitted
		}
		c.Publish(c.Value(source.Key).Value)
		return ProducerCompleted
	}, nil)
	join := e.Add(key("join"), sumInputs, []ResourceKey{source.Key, slow.Key, long.Key})
	e.AddStatic(fast, source)
	e.AddStatic(long, fast)
	e.AddStatic(slow, source)
	for _, d := range []*Node{source, slow, long} {
		e.AddStatic(join, d)
	}
	e.Request(join)
	graphSettle(t, e)
	if join.prerequisiteHeight != 3 || join.Latest.Int != 2 {
		t.Fatalf("initial join: height %d, value %v", join.prerequisiteHeight, join.Latest)
	}
	value++
	e.Revalidate(source)
	graphSettle(t, e)
	if !slow.Submitted || long.Revision != 2 || !join.ValidationPending || join.validationBlockers != 1 || join.Revision != 1 {
		t.Fatalf("waiting: slow submitted=%t, long rev=%d, join pending=%t blockers=%d rev=%d", slow.Submitted, long.Revision, join.ValidationPending, join.validationBlockers, join.Revision)
	}
	// An overlapping edit cancels the old request. An old completion cannot
	// release the join, even when the new request reuses the host's ID.
	stale := Completion{NodeID: slow.ID, Generation: slow.Generation, Attempt: slow.Attempt, RequestID: 42}
	value++
	e.Revalidate(source)
	graphSettle(t, e)
	e.Complete(stale)
	e.DrainCompletions()
	if e.HasWork() || !slow.Submitted || join.Revision != 1 {
		t.Fatal("a stale completion released the join")
	}
	e.Complete(Completion{NodeID: slow.ID, Generation: slow.Generation, Attempt: slow.Attempt, RequestID: 42})
	graphSettle(t, e)
	if join.Revision != 2 || join.Latest.Int != 8 || join.validationBlockers != 0 {
		t.Fatalf("completed join: rev=%d value=%v blockers=%d", join.Revision, join.Latest, join.validationBlockers)
	}
}

func TestHeightRepairOnDynamicRebind(t *testing.T) {
	e, _ := graphFixture(128)
	defer e.Free()
	peer := e.Add(ResourceKey{Name: "peer"}, graphProduce, e.nodes[1].Key)
	e.AddStatic(peer, e.nodes[1])
	e.Request(peer)
	choices := []*Node{e.nodes[0], e.nodes[2], peer}
	var selected int64
	control := e.Add(ResourceKey{Name: "control"}, graphProduce, &selected)
	var heightAtRebind int
	selector := e.Add(ResourceKey{Name: "selector"}, func(c *EngineContext, _ int64) ProducerResult {
		if !c.Dependency(control.Key) {
			return ProducerWaiting
		}
		heightAtRebind = c.node.prerequisiteHeight
		choice := choices[c.Value(control.Key).Value.Int]
		if !c.Dependency(choice.Key) {
			return ProducerWaiting
		}
		c.Publish(c.Value(choice.Key).Value)
		return ProducerCompleted
	}, nil)
	consumer := e.Add(ResourceKey{Name: "consumer"}, graphProduce, selector.Key)
	e.AddStatic(consumer, selector)
	e.Request(consumer)
	graphSettle(t, e)
	for _, i := range []int64{1, 2, 0, 1} {
		old, next := choices[selected], choices[i]
		selected = i
		e.Revalidate(control)
		graphSettle(t, e)
		if heightAtRebind != old.prerequisiteHeight+1 {
			t.Fatal("repair propagated the temporary removal of immediately rebound edges")
		}
		if selector.prerequisiteHeight != next.prerequisiteHeight+1 || consumer.prerequisiteHeight != next.prerequisiteHeight+2 || consumer.Latest.Int != next.Latest.Int+1 || slices.Contains(old.Dependents, selector) {
			t.Fatalf("rebound to %s: heights %d/%d, value %d", next.Key.Name, selector.prerequisiteHeight, consumer.prerequisiteHeight, consumer.Latest.Int)
		}
	}
}

func TestHeightRepairCrossesTheOldOrder(t *testing.T) {
	e := NewEngine(nil)
	defer e.Free()
	left := e.Add(ResourceKey{Name: "left"}, graphProduce, nil)
	middle := e.Add(ResourceKey{Name: "middle"}, graphProduce, nil)
	right := e.Add(ResourceKey{Name: "right"}, graphProduce, nil)
	leaf := e.Add(ResourceKey{Name: "leaf"}, graphProduce, nil)
	e.AddStatic(middle, left)
	e.repairHeights()

	// Right gains an input taller than right's cached height. Left's new
	// dependency then changes middle's height without directly marking it.
	e.AddStatic(right, middle)
	e.AddStatic(left, leaf)
	e.Request(right)
	graphSettle(t, e)
	for want, n := range []*Node{leaf, left, middle, right} {
		if n.prerequisiteHeight != want || n.State != NodeComplete {
			t.Fatalf("%s: height %d, want %d; state %d", n.Key.Name, n.prerequisiteHeight, want, n.State)
		}
	}
}

func TestStaticJoinAfterInputCompletesWithoutValue(t *testing.T) {
	e := NewEngine(nil)
	defer e.Free()
	empty := false
	input := e.Add(ResourceKey{Name: "input"}, func(c *EngineContext, _ int64) ProducerResult {
		if !empty {
			c.Publish(Value{Kind: Int, Int: 42})
		}
		return ProducerCompleted
	}, nil)
	reader := e.Add(ResourceKey{Name: "reader"}, func(c *EngineContext, _ int64) ProducerResult {
		c.Publish(Value{Kind: Bool, Bool: c.Value(input.Key).OK})
		return ProducerCompleted
	}, nil)
	e.AddStatic(reader, input)
	e.Request(reader)
	graphSettle(t, e)
	empty = true
	e.Revalidate(input)
	graphSettle(t, e)
	if reader.ValidationPending || reader.Revision != 2 || reader.Latest.Bool {
		t.Fatal("a completed input with no value left a stale join")
	}
}

func TestUnchangedValidationWakesNewConsumer(t *testing.T) {
	e := NewEngine(nil)
	defer e.Free()
	source := e.Add(ResourceKey{Name: "source"}, graphProduce, nil)
	cached := e.Add(ResourceKey{Name: "cached"}, graphProduce, source.Key)
	e.AddStatic(cached, source)
	e.Request(cached)
	graphSettle(t, e)
	e.Revalidate(source)

	late := e.Add(ResourceKey{Name: "late"}, func(c *EngineContext, _ int64) ProducerResult {
		if !c.Dependency(cached.Key) {
			return ProducerWaiting
		}
		c.Publish(c.Value(cached.Key).Value)
		return ProducerCompleted
	}, nil)
	e.Request(late)
	e.DispatchRoot(late)
	if !cached.ValidationPending || late.State != NodeWaiting || late.Current {
		t.Fatal("the new consumer did not wait for validation")
	}
	graphSettle(t, e)
	if cached.Revision != 1 || late.State != NodeComplete || late.Latest.Int != 1 {
		t.Fatalf("unchanged result: cached revision %d, new consumer state %d value %v", cached.Revision, late.State, late.Latest)
	}
}

func TestInterruptWithdrawsStaticConsumerClaim(t *testing.T) {
	e := NewEngine(nil)
	defer e.Free()
	input := e.Add(ResourceKey{Name: "input"}, graphProduce, nil)
	reader := e.Add(ResourceKey{Name: "reader"}, graphProduce, input.Key)
	e.AddStatic(reader, input)
	e.Request(reader)
	if e.Step() != input || !slices.Equal(e.Ready(1), []*Node{reader}) {
		t.Fatal("static consumer was not offered after its input completed")
	}
	e.Interrupt(input)
	if !input.Current || e.CanDispatch(reader) || e.Dispatch(reader) != nil {
		t.Fatal("reopening a static prerequisite did not withdraw the outstanding claim")
	}
	if e.Step() != input || !slices.Equal(e.Ready(1), []*Node{reader}) || e.Dispatch(reader) != reader {
		t.Fatal("the reader did not dispatch after its prerequisite completed again")
	}
}
