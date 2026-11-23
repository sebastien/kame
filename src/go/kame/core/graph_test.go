package core

import (
	"fmt"
	"testing"
)

// Use the same scalar producer everywhere: changing the source changes exactly
// three publications regardless of how many unrelated roots have settled.
func graphProduce(c *EngineContext, _ int64) ProducerResult {
	var v int64
	switch x := c.Context().(type) {
	case *int64:
		v = *x
	case ResourceKey:
		v = c.Value(x).Value.Int + 1
	}
	c.Publish(Value{Kind: Int, Int: v})
	return ProducerCompleted
}

func sumInputs(c *EngineContext, _ int64) ProducerResult {
	var total int64
	for _, key := range c.Context().([]ResourceKey) {
		v := c.Value(key)
		if !v.OK {
			c.Fail(Diagnostic{Code: "STALE_INPUT"})
			return ProducerFailed
		}
		total += v.Value.Int
	}
	c.Publish(Value{Kind: Int, Int: total})
	return ProducerCompleted
}

func graphSettle(t testing.TB, e *Engine) {
	for steps := 0; e.Step() != nil; steps++ {
		if steps > len(e.nodes)*3 {
			t.Fatal("graph did not settle")
		}
	}
}

func graphFixture(n int) (*Engine, *int64) {
	e, value := NewEngine(nil), new(int64)
	for i := range n {
		var ctx any
		if i == 0 {
			ctx = value
		} else if i < 3 {
			ctx = e.nodes[i-1].Key
		}
		node := e.Add(ResourceKey{Kind: ResourceDefinition, Name: fmt.Sprintf("n%06d", i)}, graphProduce, ctx)
		if i == 1 || i == 2 {
			e.AddStatic(node, e.nodes[i-1])
		}
	}
	for _, node := range e.nodes {
		e.Request(node)
	}
	return e, value
}

func TestGraphLocalEdit(t *testing.T) {
	e, value := graphFixture(128)
	defer e.Free()
	graphSettle(t, e)
	for edit := range 32 {
		*value = int64(edit + 1)
		e.Revalidate(e.nodes[0])
		if len(e.checks) != 1 || e.checks[0] != e.nodes[0] || len(e.dirtyHeights) != 0 || len(e.runnable) != 0 {
			t.Fatal("a value edit should queue only the source and leave cached heights intact")
		}
		graphSettle(t, e)
		for i, node := range e.nodes {
			want := int64(1)
			if i < 3 {
				want += *value
			}
			if node.Revision != want || !node.Current || node.ValidationPending {
				t.Fatalf("edit %d node %d: revision %d, want %d", edit, i, node.Revision, want)
			}
		}
		if e.nodes[2].Latest.Int != *value+2 {
			t.Fatal("stale root value")
		}
	}
}
