package core

import "testing"

func TestFileObserversFollowAcceptedGeneration(t *testing.T) {
	e := NewEngine(nil)
	defer e.Free()
	file := func(s string) ResourceKey { return ResourceKey{Kind: ResourceFile, Name: s} }
	path := "first"
	var runs int64
	n := e.Add(ResourceKey{Name: "reader"}, func(c *EngineContext, _ int64) ProducerResult {
		runs++
		sig := ValueSignature(Value{Kind: Int, Int: runs})
		c.ObserveAspect(file(path), ObservationContent, sig)
		checkpoint := c.CheckpointDependencies()
		c.ObserveAspect(file("speculative"), ObservationContent, sig)
		c.RestoreDependencies(&checkpoint)
		c.ObserveAspect(file("stat-only"), ObservationMetadata, sig)
		c.Publish(Value{Kind: Int, Int: runs})
		return ProducerCompleted
	}, nil)
	root := e.RequestRoot(n)
	graphSettle(t, e)
	path = "second"
	e.RevalidateResource(file("tmp/../first"))
	graphSettle(t, e)
	for _, stale := range []string{"first", "speculative", "stat-only"} {
		e.RevalidateResource(file(stale))
		graphSettle(t, e)
	}
	if runs != 2 || len(e.fileObservers) != 1 {
		t.Fatalf("after rebind and rollback: runs=%d, indexed paths=%d", runs, len(e.fileObservers))
	}
	e.RevalidateResource(file("./second"))
	graphSettle(t, e)
	e.Release(root)
	if runs != 3 || !e.Retire(n) || len(e.fileObservers) != 0 || e.HasWork() {
		t.Fatal("retired reader retained an observation or runnable work")
	}
}

func TestFileObserversWithAndWithoutEdges(t *testing.T) {
	e := NewEngine(nil)
	defer e.Free()
	key := ResourceKey{Kind: ResourceFile, Name: "shared"}
	var v int64
	resource := e.Add(key, graphProduce, &v)
	var readers []*Node
	for _, name := range []string{"graph-backed", "first-local", "second-local"} {
		n := e.Add(ResourceKey{Name: name}, func(c *EngineContext, _ int64) ProducerResult {
			value := Value{Kind: Int, Int: v}
			if name == "graph-backed" {
				value = c.Value(key).Value
			}
			c.ObserveAspect(key, ObservationContent, ValueSignature(value))
			c.Publish(value)
			return ProducerCompleted
		}, nil)
		if name == "graph-backed" {
			e.AddStatic(n, resource)
		}
		e.Request(n)
		readers = append(readers, n)
	}
	graphSettle(t, e)
	e.RevalidateResource(key)
	graphSettle(t, e)
	for i, n := range readers {
		if (i == 0 && n.Revision != 1) || (i != 0 && n.Revision != 2) {
			t.Fatalf("%s revision %d after equal content", n.Key.Name, n.Revision)
		}
	}
}
