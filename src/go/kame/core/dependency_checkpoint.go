package core

import (
	"kame/internal/mem"
	"slices"
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
	checkpoint.OrderOnly = slices.Clone(c.node.OrderOnly)
	checkpoint.Observed = slices.Clone(c.node.Observed)
	for _, item := range c.node.observations {
		item.Key = item.Key.Clone(c.engine.Alloc)
		checkpoint.Observations = append(checkpoint.Observations, item)
	}
	return checkpoint
}

func (checkpoint *DependencyCheckpoint) Free(a mem.Allocator) {
	mem.FreeSlice(nil, checkpoint.OrderOnly)
	mem.FreeSlice(nil, checkpoint.Observed)
	FreeObservations(a, checkpoint.Observations)
	*checkpoint = DependencyCheckpoint{}
}

func (c *EngineContext) RestoreDependencies(checkpoint *DependencyCheckpoint) {
	n := c.node
	c.engine.changes++
	c.engine.dropDynamicDependencies(n, checkpoint.Dynamic, false)
	mem.FreeSlice(nil, n.OrderOnly)
	mem.FreeSlice(nil, n.Observed)
	n.OrderOnly, n.Observed = checkpoint.OrderOnly, checkpoint.Observed
	c.engine.replaceObservations(n, checkpoint.Observations)
	*checkpoint = DependencyCheckpoint{}
}
