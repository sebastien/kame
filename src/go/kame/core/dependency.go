package core

import "slices"

func (e *Engine) AddStatic(n *Node, dependency *Node) bool {
	if n != nil && (n.Requested || n.State != NodeIdle) {
		return false
	}
	if n == nil || dependency == nil || reaches(dependency, n) {
		if n != nil {
			n.complete(e, Diagnostic{Code: DiagnosticDependencyCycle})
		}
		return false
	}
	if !slices.Contains(n.Static, dependency) {
		e.linkDependency(n, dependency, true)
	}
	return true
}

func reaches(from *Node, want *Node) bool {
	if from == want {
		return true
	}
	for _, prerequisites := range [2][]*Node{from.Static, from.Dynamic} {
		for _, dependency := range prerequisites {
			if reaches(dependency, want) {
				return true
			}
		}
	}
	return false
}

func (e *Engine) linkDependency(n, dependency *Node, static bool) {
	if static {
		n.Static = append(n.Static, dependency)
	} else {
		n.Dynamic = append(n.Dynamic, dependency)
	}
	dependency.Dependents = append(dependency.Dependents, n)
	if !dependency.validationSettled {
		n.validationBlockers++
	}
	if dependency.prerequisiteHeight >= n.prerequisiteHeight {
		e.markHeightDirty(n)
	}
	if !static {
		if i := slices.Index(n.previousDynamic, dependency); i >= 0 {
			n.previousDynamic = slices.Delete(n.previousDynamic, i, i+1)
			return // The previous edge already holds the interest.
		}
	}
	e.edgeInterest(n, dependency, 1)
}

func (e *Engine) unlinkDependency(n, dependency *Node, retainInterest bool) {
	i := slices.Index(dependency.Dependents, n)
	dependency.Dependents = slices.Delete(dependency.Dependents, i, i+1)
	if !dependency.validationSettled {
		n.validationBlockers--
		if n.ValidationPending {
			e.enqueue(n)
		}
	}
	if dependency.prerequisiteHeight+1 == n.prerequisiteHeight {
		e.markHeightDirty(n)
	}
	if retainInterest {
		n.previousDynamic = append(n.previousDynamic, dependency)
	} else {
		e.edgeInterest(n, dependency, -1)
	}
}

// dropDynamicDependencies removes the suffix left by a discarded generation
// or a speculative checkpoint. Retained interest lasts until rebinding.
func (e *Engine) dropDynamicDependencies(n *Node, keep int, retainInterest bool) {
	for _, dependency := range n.Dynamic[keep:] {
		e.unlinkDependency(n, dependency, retainInterest)
	}
	if keep == 0 {
		n.Dynamic = nil
	} else {
		n.Dynamic = slices.Delete(n.Dynamic, keep, len(n.Dynamic))
	}
}

func (e *Engine) releasePreviousDependencies(n *Node) {
	previous := n.previousDynamic
	n.previousDynamic = nil
	for _, dependency := range previous {
		e.edgeInterest(n, dependency, -1)
	}
}

// edgeInterest transfers both live and suspended demand along one edge.
func (e *Engine) edgeInterest(n, dependency *Node, sign int64) {
	if n.Interest != 0 {
		e.adjustInterest(dependency, sign*n.Interest, sign*n.SuspendedInterest)
	}
}
