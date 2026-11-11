package core

import "kame/internal/mem"

func (c *EngineContext) SetTeardown(begin func(any, int64, int64) bool, advance Producer, state any) {
	c.node.BeginTeardown, c.node.Teardown, c.node.TeardownContext = begin, advance, state
}

func (c *EngineContext) TeardownState() any { return c.node.TeardownContext }

func (c *EngineContext) TeardownDiagnostic(d Diagnostic) {
	c.node.teardownDiagnostic.Free(c.engine.Alloc)
	c.node.teardownDiagnostic = d
}

// Teardown starts graceful disposal even when public roots are still retained.
func (e *Engine) Teardown(n *Node) {
	if n != nil {
		e.beginTeardown(n, false)
	}
}

func (e *Engine) LookupID(id int64) *Node {
	for i := range e.nodes {
		if e.nodes[i].ID == id {
			return e.nodes[i]
		}
	}
	return nil
}

func (e *Engine) beginTeardown(n *Node, restart bool) bool {
	if n.tearingDown {
		n.teardownRestart = n.teardownRestart || restart
		return true
	}
	if n.BeginTeardown == nil || !n.BeginTeardown(n.TeardownContext, n.ID, n.Generation) {
		return false
	}
	n.tearingDown, n.teardownRestart = true, restart
	if !n.TeardownPreserveRequest {
		e.Interrupt(n)
	}
	n.teardownRoot = e.RequestRoot(n)
	if !n.Submitted {
		n.State = NodeReady
	}
	return true
}

// Interrupt withdraws one request without ending its generation. Hosts still
// owe cancellation/reaping; a completion for the withdrawn ID is stale.
func (e *Engine) Interrupt(n *Node) {
	if n == nil {
		return
	}
	if n.Submitted {
		e.cancellations = append(e.cancellations, Cancellation{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: n.HostRequestID})
	}
	n.Submitted, n.HostRequestID = false, 0
	if n.HasCompletion {
		n.Completion.Value.Free(e.Alloc)
		n.Completion.Diagnostic.Free(e.Alloc)
		n.Completion, n.HasCompletion = Completion{}, false
	}
	n.State = NodeReady
	for i := range n.Dynamic {
		d := n.Dynamic[i]
		removeDependent(d, n)
		if n.Interest != 0 {
			e.interest(d, -n.Interest)
		}
	}
	mem.FreeSlice(nil, n.Dynamic)
	mem.FreeSlice(nil, n.Observed)
	mem.FreeSlice(nil, n.OrderOnly)
	n.Dynamic, n.Observed, n.OrderOnly = nil, nil, nil
	e.releasePreviousDependencies(n)
}

func (e *Engine) finishTeardown(n *Node) {
	restart := n.teardownRestart
	n.BeginTeardown, n.Teardown, n.TeardownContext = nil, nil, nil
	n.tearingDown = false
	root := n.teardownRoot
	n.teardownRoot = nil
	n.invalidating = true
	e.Release(root)
	n.invalidating = false
	d := n.teardownDiagnostic
	n.teardownDiagnostic = Diagnostic{}
	if restart {
		d.Free(e.Alloc)
		e.Invalidate(n)
	} else {
		n.teardownDiagnostic = d
		e.cancel(n)
	}
}
