package core

import (
	"cmp"
	"container/heap"
	"iter"
	"slices"
)

// Only changed nodes need a readiness check. Check prerequisites before their
// consumers; resource-key rotation, not height, decides which ready job runs.
type checkQueue []*Node

func (q checkQueue) Len() int { return len(q) }
func (q checkQueue) Less(i, j int) bool {
	a, b := q[i], q[j]
	return a.prerequisiteHeight < b.prerequisiteHeight || a.prerequisiteHeight == b.prerequisiteHeight && a.ID < b.ID
}
func (q checkQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].checkIndex, q[j].checkIndex = i+1, j+1
}
func (q *checkQueue) Push(x any) {
	n := x.(*Node)
	*q = append(*q, n)
	n.checkIndex = len(*q)
}
func (q *checkQueue) Pop() any {
	i := len(*q) - 1
	n := (*q)[i]
	(*q)[i], n.checkIndex = nil, 0
	*q = (*q)[:i]
	return n
}

func (e *Engine) enqueue(n *Node) {
	if n.checkIndex != 0 || n.running {
		return
	}
	if !n.runnable {
		if n.Interest == 0 || (n.ValidationPending && n.validationBlockers != 0) ||
			(!n.ValidationPending && n.State != NodeIdle && n.State != NodeReady) {
			return
		}
	}
	heap.Push(&e.checks, n)
}

// Wake resumes a producer waiting on a local event rather than a host request.
func (e *Engine) Wake(n *Node) {
	if n != nil && !n.Submitted && n.State == NodeWaiting {
		n.State = NodeReady
		e.enqueue(n)
	}
}

func (e *Engine) markHeightDirty(n *Node) {
	if !n.heightDirty {
		n.heightDirty = true
		e.dirtyHeights = append(e.dirtyHeights, n)
	}
}

func (n *Node) heightFromInputs() int {
	height := 0
	for _, prerequisites := range [2][]*Node{n.Static, n.Dynamic} {
		for _, input := range prerequisites {
			height = max(height, input.prerequisiteHeight+1)
		}
	}
	return height
}

// Only a mismatch against the cached input heights can start a change.
// Mark its consumers before repairing any heights so even a newly added edge
// across the old height order is repaired prerequisite-first.
func (e *Engine) repairHeights() {
	dirty := e.dirtyHeights[:0]
	for _, n := range e.dirtyHeights {
		if n.heightFromInputs() == n.prerequisiteHeight {
			n.heightDirty = false
		} else {
			dirty = append(dirty, n)
		}
	}
	clear(e.dirtyHeights[len(dirty):])
	e.dirtyHeights = dirty
	for i := 0; i < len(e.dirtyHeights); i++ {
		for _, consumer := range e.dirtyHeights[i].Dependents {
			e.markHeightDirty(consumer)
		}
	}
	for _, n := range e.dirtyHeights {
		e.repairHeight(n)
	}
	clear(e.dirtyHeights)
	e.dirtyHeights = e.dirtyHeights[:0]
}

func (e *Engine) repairHeight(n *Node) {
	if !n.heightDirty {
		return
	}
	for _, prerequisites := range [2][]*Node{n.Static, n.Dynamic} {
		for _, input := range prerequisites {
			e.repairHeight(input)
		}
	}
	n.heightDirty = false
	if height := n.heightFromInputs(); height != n.prerequisiteHeight {
		n.prerequisiteHeight = height
		if n.checkIndex != 0 {
			heap.Fix(&e.checks, n.checkIndex-1)
		}
	}
}

// Count physical edges, including when the same prerequisite is both static
// and dynamic. Validation can finish even if a prerequisite produced no value.
func (e *Engine) syncValidationInput(n *Node) {
	settled := !n.ValidationPending && (n.Current || n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled)
	if settled == n.validationSettled {
		return
	}
	n.validationSettled = settled
	for _, c := range n.Dependents {
		if settled {
			c.validationBlockers--
		} else {
			c.validationBlockers++
			c.offered = false
		}
		if c.ValidationPending || c.runnable {
			e.enqueue(c)
		}
	}
}

func compareDispatchKey(a, b *Node) int {
	if c := cmp.Compare(a.Key.Name, b.Key.Name); c != 0 {
		return c
	}
	return cmp.Compare(a.Key.Kind, b.Key.Kind)
}

func (e *Engine) setRunnable(n *Node, ready bool) {
	if ready == n.runnable {
		return
	}
	i, _ := slices.BinarySearchFunc(e.runnable, n, compareDispatchKey)
	if ready {
		e.runnable = slices.Insert(e.runnable, i, n)
	} else {
		e.runnable = slices.Delete(e.runnable, i, i+1)
		n.offered = false
	}
	n.runnable = ready
}

func (e *Engine) checkReadiness(n *Node) bool {
	if n.ValidationPending {
		e.validatePending(n)
		if n.ValidationPending {
			return false
		}
	}
	if n.running || n.Interest == 0 || n.suspended() || n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled || n.State == NodeWaiting {
		return false
	}
	for _, dependency := range n.Static {
		if n.tearingDown {
			continue
		}
		if dependency.State == NodeFailed || dependency.State == NodeCancelled {
			n.complete(e, dependency.Diagnostic.Clone(e.Alloc))
			return false
		}
		if dependency.State != NodeComplete || dependency.ValidationPending {
			e.demand(dependency)
			return false
		}
	}
	// The settled prefix is safe until the graph-wide change stamp moves.
	if n.settledChanges != e.changes {
		n.settledChanges, n.settledDynamic = e.changes, 0
	}
	for i := n.settledDynamic; i < len(n.Dynamic); i++ {
		dependency := n.Dynamic[i]
		if dependency.State == NodeFailed || dependency.State == NodeCancelled {
			if slices.Contains(n.Observed, dependency) {
				continue
			}
			n.complete(e, dependency.Diagnostic.Clone(e.Alloc))
			return false
		}
		if !dependency.Current || dependency.ValidationPending {
			e.demand(dependency)
			return false
		}
		if i == n.settledDynamic {
			n.settledDynamic++
		}
	}
	return n.Requested
}

// UnsettledDynamic returns n's dynamic edges except the prefix last found valid.
func (e *Engine) UnsettledDynamic(n *Node) []*Node {
	if n.settledChanges != e.changes || n.settledDynamic > len(n.Dynamic) {
		return n.Dynamic
	}
	return n.Dynamic[n.settledDynamic:]
}

func (e *Engine) refreshRunnable() {
	for len(e.checks) != 0 {
		e.repairHeights()
		// Validation can invalidate this very node. Keep it queued until the
		// readiness check has inspected the replacement generation as well.
		n := e.checks[0]
		ready := e.checkReadiness(n)
		heap.Remove(&e.checks, n.checkIndex-1)
		e.setRunnable(n, ready)
	}
	// A ready producer can replace dropped edges before another check needs
	// their heights. At quiescence, settle any remaining topology.
	if len(e.runnable) == 0 {
		e.repairHeights()
	}
}

func (e *Engine) available(after *Node) iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		i := 0
		if after != nil {
			var found bool
			i, found = slices.BinarySearchFunc(e.runnable, after, compareDispatchKey)
			if found {
				i++
			}
		}
		for range e.runnable {
			if i == len(e.runnable) {
				i = 0
			}
			n := e.runnable[i]
			i++
			if !n.offered && (e.CanRun == nil || e.CanRun(n)) && !yield(n) {
				return
			}
		}
	}
}

func (e *Engine) choose(after *Node) *Node {
	e.refreshRunnable()
	for n := range e.available(after) {
		return n
	}
	return nil
}

// HasWork reports local work that must run before waiting for host events.
func (e *Engine) HasWork() bool {
	return len(e.completions) != 0 || e.choose(e.lastRun) != nil
}

// Ready returns up to capacity ready nodes, rotating through resource-key order.
func (e *Engine) Ready(capacity int) []*Node {
	if capacity <= 0 {
		return nil
	}
	e.refreshRunnable()
	var nodes []*Node
	for n := range e.available(e.lastRun) {
		n.offered = true
		nodes = append(nodes, n)
		if len(nodes) == capacity {
			break
		}
	}
	return nodes
}

// CanDispatch checks that a host-retained claim still belongs to live work.
// Unlike Ready, this does not validate dependencies or advance the engine.
func (e *Engine) CanDispatch(n *Node) bool {
	return n != nil && n.offered && !n.ValidationPending && n.Interest != 0 && !n.suspended() && n.Requested && (n.State == NodeIdle || n.State == NodeReady) && (e.CanRun == nil || e.CanRun(n))
}

// Dispatch starts one node returned by Ready. A claimed node starts once.
func (e *Engine) Dispatch(n *Node) *Node {
	if !e.CanDispatch(n) || (n.checkIndex != 0 && !e.checkReadiness(n)) {
		if n != nil {
			n.offered = false
		}
		return nil
	}
	return e.run(n)
}

// DispatchRoot starts a newly requested independent root without reentering
// the scheduler (and therefore without redispatching the current producer).
func (e *Engine) DispatchRoot(n *Node) *Node {
	if n == nil || !e.checkReadiness(n) || (e.CanRun != nil && !e.CanRun(n)) {
		return nil
	}
	return e.run(n)
}

func (e *Engine) unschedule(n *Node) {
	if n.checkIndex != 0 {
		heap.Remove(&e.checks, n.checkIndex-1)
	}
	if n.heightDirty {
		i := slices.Index(e.dirtyHeights, n)
		e.dirtyHeights = slices.Delete(e.dirtyHeights, i, i+1)
	}
	e.setRunnable(n, false)
}
