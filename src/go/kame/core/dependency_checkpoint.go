package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// DependencyCheckpoint isolates speculative reuse validation from execution.
// A rejected cache candidate must not leave obsolete reads or graph edges.
type DependencyCheckpoint struct {
	Dynamic      int
	OrderOnly    []*Node
	Observed     []*Node
	Observations []Observation
}

func (c *EngineContext) CheckpointDependencies() DependencyCheckpoint {
	checkpoint := DependencyCheckpoint{Dynamic: len(c.node.Dynamic)}
	checkpoint.OrderOnly = slices.Clone(c.engine.Alloc, c.node.OrderOnly)
	checkpoint.Observed = slices.Clone(c.engine.Alloc, c.node.Observed)
	for i := range c.node.Observations {
		item := c.node.Observations[i]
		item.Key = item.Key.Clone(c.engine.Alloc)
		checkpoint.Observations = slices.Append(c.engine.Alloc, checkpoint.Observations, item)
	}
	return checkpoint
}

func (checkpoint *DependencyCheckpoint) Free(a mem.Allocator) {
	slices.Free(a, checkpoint.OrderOnly)
	slices.Free(a, checkpoint.Observed)
	FreeObservations(a, checkpoint.Observations)
	*checkpoint = DependencyCheckpoint{}
}

func (c *EngineContext) RestoreDependencies(checkpoint *DependencyCheckpoint) {
	n := c.node
	for i := checkpoint.Dynamic; i < len(n.Dynamic); i++ {
		dependency := n.Dynamic[i]
		removeDependent(dependency, n)
		if n.Interest != 0 {
			c.engine.interest(dependency, -n.Interest)
		}
	}
	n.Dynamic = n.Dynamic[:checkpoint.Dynamic]
	slices.Free(c.engine.Alloc, n.OrderOnly)
	slices.Free(c.engine.Alloc, n.Observed)
	FreeObservations(c.engine.Alloc, n.Observations)
	n.OrderOnly, n.Observed, n.Observations = checkpoint.OrderOnly, checkpoint.Observed, checkpoint.Observations
	*checkpoint = DependencyCheckpoint{}
}
