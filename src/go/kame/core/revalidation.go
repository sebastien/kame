package core

import "slices"

// Revalidate refreshes a resource before discarding consumers' accepted state.
// Active host work still uses cancellation-first invalidation; a settled graph
// can validate bottom-up and suppress propagation of unchanged results.
func (e *Engine) Revalidate(n *Node) {
	if n == nil {
		return
	}
	nodes := []*Node{n}
	n.inClosure = true
	stable := true
	for i := 0; i < len(nodes); i++ {
		item := nodes[i]
		for _, d := range item.Dependents {
			if !d.inClosure {
				d.inClosure = true
				nodes = append(nodes, d)
			}
		}
		// A repeated notification before dispatch only refreshes the already
		// queued resource; it does not invalidate accepted consumers again.
		if item == n && item.State == NodeIdle && item.Requested && !item.Current && !item.Submitted && !item.HasCompletion {
			continue
		}
		// Lazy value sources can be current without being terminal. Their
		// accepted observations are the same reuse proof as completed recipes.
		if item.Submitted || item.HasCompletion || !item.Current || (item.State != NodeComplete && item.State != NodeWaiting) {
			stable = false
			break
		}
	}
	// Release the traversal marks before invalidation can enter a teardown.
	for _, d := range nodes {
		d.inClosure = false
	}
	if !stable {
		e.Invalidate(n)
		return
	}
	for _, d := range nodes[1:] {
		d.ValidationPending = true
		e.syncValidationInput(d)
		e.changes++
	}
	e.invalidate(n, nil, false)
	for _, d := range nodes[1:] {
		e.enqueue(d)
	}
}

func (e *Engine) validatePending(n *Node) {
	if n.Interest == 0 || n.validationBlockers != 0 {
		return
	}
	unchanged := !n.DisableReuse && n.Signature.Equal(n.Signature)
	// Named environment/operation observations need not own graph edges.
	// Conflicting or unsupported reads still make the computation non-reusable.
	for _, item := range n.observations {
		if !item.Signature.Equal(item.Signature) {
			unchanged = false
		}
	}
	for i := range n.Static {
		if !consumedDependencyUnchanged(n, n.Static[i]) {
			unchanged = false
		}
	}
	for i := range n.Dynamic {
		dependency := n.Dynamic[i]
		if dependency.State == NodeFailed || dependency.State == NodeCancelled {
			unchanged = false
			continue
		}
		if !slices.Contains(n.OrderOnly, dependency) && !consumedDependencyUnchanged(n, dependency) {
			unchanged = false
		}
	}
	n.ValidationPending = false
	if unchanged {
		e.syncValidationInput(n)
		// A new consumer can join this dependency while validation is in flight.
		for _, d := range n.Dependents {
			if !d.ValidationPending && !d.Submitted {
				e.Wake(d)
				e.enqueue(d)
			}
		}
		return
	}
	// Keep old dependency interest while the producer discovers its new edges.
	e.invalidate(n, nil, true)
}

func consumedDependencyUnchanged(consumer *Node, dependency *Node) bool {
	if !dependency.Current {
		return false
	}
	found := false
	i, _ := consumer.findObservation(dependency.Key.Kind, dependency.Key.Name, ObservationValue)
	for _, index := range consumer.observationOrder[i:] {
		item := consumer.observations[index]
		if item.Key.Kind != dependency.Key.Kind || item.Key.Name != dependency.Key.Name {
			break
		}
		found = true
		signature := dependency.Signature
		if item.Aspect == ObservationExistence {
			signature = ValueSignature(Value{Kind: Bool, Bool: dependency.Latest.Kind != Nil})
		} else if item.Aspect >= ObservationMetadata {
			// A content result cannot prove an intentionally consumed stat view.
			return false
		}
		if !item.Signature.Equal(signature) {
			return false
		}
	}
	return found
}
