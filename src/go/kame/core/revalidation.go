package core

import "solod.dev/so/slices"

// Revalidate refreshes a resource before discarding consumers' accepted state.
// Active host work still uses cancellation-first invalidation; a settled graph
// can validate bottom-up and suppress propagation of unchanged results.
func (e *Engine) Revalidate(n *Node) {
	if n == nil {
		return
	}
	var nodes []*Node
	e.revalidationClosure(n, &nodes)
	stable := true
	for i := range nodes {
		item := nodes[i]
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
	if !stable {
		slices.Free(e.Alloc, nodes)
		e.Invalidate(n)
		return
	}
	for i := range nodes {
		if nodes[i] != n {
			nodes[i].ValidationPending = true
		}
	}
	slices.Free(e.Alloc, nodes)
	var seen []*Node
	e.invalidate(n, &seen, false, false)
	slices.Free(e.Alloc, seen)
}

func (e *Engine) revalidationClosure(n *Node, nodes *[]*Node) {
	if slices.Contains(*nodes, n) {
		return
	}
	*nodes = slices.Append(e.Alloc, *nodes, n)
	for i := range n.Dependents {
		e.revalidationClosure(n.Dependents[i], nodes)
	}
}

func dependencySettled(n *Node) bool {
	return !n.ValidationPending && (n.Current || n.State == NodeFailed || n.State == NodeCancelled)
}

func (e *Engine) validatePending(n *Node) {
	if n.Interest == 0 {
		return
	}
	for i := range n.Static {
		if !dependencySettled(n.Static[i]) {
			n.Static[i].Requested = true
			return
		}
	}
	for i := range n.Dynamic {
		if !dependencySettled(n.Dynamic[i]) {
			n.Dynamic[i].Requested = true
			return
		}
	}
	unchanged := !n.DisableReuse && n.Signature.Equal(n.Signature)
	// Named environment/operation observations need not own graph edges.
	// Conflicting or unsupported reads still make the computation non-reusable.
	for i := range n.Observations {
		if !n.Observations[i].Signature.Equal(n.Observations[i].Signature) {
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
		return
	}
	var seen []*Node
	// Keep old dependency interest while the producer discovers its new edges.
	e.invalidate(n, &seen, true, false)
	slices.Free(e.Alloc, seen)
}

func consumedDependencyUnchanged(consumer *Node, dependency *Node) bool {
	if !dependency.Current {
		return false
	}
	found := false
	for i := range consumer.Observations {
		item := consumer.Observations[i]
		if item.Key.Kind != dependency.Key.Kind || item.Key.Name != dependency.Key.Name {
			continue
		}
		found = true
		signature := dependency.Signature
		if item.Aspect == ObservationExistence {
			signature = ValueSignature(Value{Kind: Bool, Bool: dependency.Latest.Kind != Nil})
		} else if item.Aspect == ObservationMetadata {
			// A content result cannot prove an intentionally consumed stat view.
			return false
		}
		if !item.Signature.Equal(signature) {
			return false
		}
	}
	return found
}
