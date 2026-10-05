package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

type Root struct {
	node *Node
	live bool
}

type Engine struct {
	Alloc         mem.Allocator
	nodes         []*Node
	roots         []*Root
	nextID        int64
	cancellations []Cancellation
	completions   []Completion
	nextUpdate    int64
	lastRun       *Node
}

func NewEngine(a mem.Allocator) *Engine {
	e := mem.Alloc[Engine](a)
	e.Alloc = a
	return e
}

func canonicalName(a mem.Allocator, key ResourceKey) (string, bool) {
	if key.Kind == ResourceFile {
		return path.Clean(a, key.Name), true
	}
	return key.Name, false
}

func (e *Engine) find(key ResourceKey) *Node {
	name, owned := canonicalName(e.Alloc, key)
	for i := range e.nodes {
		n := e.nodes[i]
		if n.Key.Kind == key.Kind && n.Key.Name == name {
			if owned { mem.FreeString(e.Alloc, name) }
			return n
		}
	}
	if owned { mem.FreeString(e.Alloc, name) }
	return nil
}

func (e *Engine) node(key ResourceKey) *Node {
	if existing := e.find(key); existing != nil {
		return existing
	}
	name, owned := canonicalName(e.Alloc, key)
	n := mem.Alloc[Node](e.Alloc)
	e.nextID++
	n.ID = e.nextID
	n.Key = NewResourceKey(e.Alloc, key.Kind, name)
	if owned { mem.FreeString(e.Alloc, name) }
	n.State = NodeIdle
	e.nodes = slices.Append(e.Alloc, e.nodes, n)
	return n
}

func (e *Engine) Add(key ResourceKey, producer Producer, context any) *Node {
	return e.AddOwned(key, producer, context, nil)
}

// AddOwned registers a producer context released with the engine.
func (e *Engine) AddOwned(key ResourceKey, producer Producer, context any, free ContextFree) *Node {
	n := e.node(key)
	if n.Producer != nil || n.materializer != nil {
		return nil
	}
	n.Producer = producer
	n.Context = context
	n.ContextFree = free
	return n
}

func (e *Engine) AddStatic(n *Node, dependency *Node) bool {
	if n != nil && (n.Requested || n.State != NodeIdle) { return false }
	if n == nil || dependency == nil || n == dependency || reaches(dependency, n) {
		if n != nil {
			n.complete(e, Diagnostic{Code: DiagnosticDependencyCycle})
		}
		return false
	}
	if slices.Contains(n.Static, dependency) { return true }
	n.Static = slices.Append(e.Alloc, n.Static, dependency)
	dependency.Dependents = slices.Append(e.Alloc, dependency.Dependents, n)
	if n.Interest != 0 { e.interest(dependency, n.Interest) }
	return true
}

func reaches(from *Node, want *Node) bool {
	if from == want { return true }
	for i := range from.Static { if reaches(from.Static[i], want) { return true } }
	for i := range from.Dynamic { if reaches(from.Dynamic[i], want) { return true } }
	return false
}

func (e *Engine) RequestRoot(n *Node) *Root {
	if n == nil { return nil }
	r := mem.Alloc[Root](e.Alloc)
	r.node, r.live = n, true
	e.roots = slices.Append(e.Alloc, e.roots, r)
	e.interest(n, 1)
	request(n)
	return r
}

// Request is the idempotent convenience root for a node.
func (e *Engine) Request(n *Node) {
	if n != nil && n.RequestInterest == 0 {
		n.RequestInterest = 1
		e.interest(n, 1)
		request(n)
	}
}

func request(n *Node) {
	n.Requested = true
	for i := range n.Static { request(n.Static[i]) }
	for i := range n.Dynamic { request(n.Dynamic[i]) }
}

func (e *Engine) Release(r *Root) {
	if r == nil || !r.live { return }
	r.live = false
	e.interest(r.node, -1)
	for i := range e.roots {
		if e.roots[i] == r {
			copy(e.roots[i:], e.roots[i+1:])
			e.roots = e.roots[:len(e.roots)-1]
			break
		}
	}
	mem.Free(e.Alloc, r)
}

func (e *Engine) interest(n *Node, delta int64) {
	n.Interest += delta
	for i := range n.Static { e.interest(n.Static[i], delta) }
	for i := range n.Dynamic { e.interest(n.Dynamic[i], delta) }
	for i := range n.previousDynamic { e.interest(n.previousDynamic[i], delta) }
	if n.Interest == 0 && !n.invalidating && n.State != NodeComplete && n.State != NodeFailed && n.State != NodeCancelled {
		e.cancel(n)
	}
}

func (e *Engine) Subscribe(n *Node) *Subscription {
	if n == nil { return nil }
	s := mem.Alloc[Subscription](e.Alloc)
	s.Alloc, s.Node = e.Alloc, n
	n.Subs = slices.Append(e.Alloc, n.Subs, s)
	e.interest(n, 1)
	request(n)
	if n.Current { s.pushValue(Event{Kind: UpdateValue, NodeID: n.ID, Value: n.Latest.Clone(e.Alloc), Revision: n.Revision}) }
	if n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled {
		s.terminal = terminal(n)
	}
	return s
}

func (e *Engine) emit(n *Node, event Event) {
	e.nextUpdate++
	if event.NodeID == 0 { event.NodeID = n.ID }
	event.Order = e.nextUpdate
	for i := range n.Subs {
		s := n.Subs[i]
		if event.HasValue() {
			s.pushValue(Event{Kind: event.Kind, NodeID: event.NodeID, DependencyID: event.DependencyID, Order: event.Order, Value: event.Value.Clone(e.Alloc), Revision: event.Revision})
		} else if event.Terminal() {
			s.terminal = event
		} else {
			if len(s.updates) == 8 {
				s.updates = s.updates[:1]
			}
			s.updates = slices.Append(e.Alloc, s.updates, event)
		}
	}
}

func (e *Engine) publish(n *Node, value Value) {
	if value.HasTransientCallable() {
		value.Free(e.Alloc)
		n.complete(e, Diagnostic{Code: DiagnosticExprValue})
		return
	}
	if n.Current { n.Latest.Free(e.Alloc) }
	n.Latest, n.Current = value.Clone(e.Alloc), true
	value.Free(e.Alloc)
	n.Revision++
	e.releasePreviousDependencies(n)
	e.emit(n, Event{Kind: UpdateValue, Value: n.Latest, Revision: n.Revision})
	dependents := slices.Clone(e.Alloc, n.Dependents)
	for i := range dependents {
		dependent := dependents[i]
		if dependent.State != NodeWaiting { continue }
		// A host request was derived from an older dependency snapshot. Cancel it
		// and restart from the newest values instead of accepting its completion.
		if dependent.Submitted { e.restartFromPublication(dependent); continue }
		dependent.State = NodeReady
	}
	slices.Free(e.Alloc, dependents)
}

// Publish makes a new current value visible for a live node without completing
// its producer. Runtime adapters use this for readiness signals whose process
// continues running after consumers may begin.
func (e *Engine) Publish(n *Node, value Value) {
	if n == nil || n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled {
		value.Free(e.Alloc)
		return
	}
	e.publish(n, value)
}

// Fail terminates a live node and requests cancellation of its outstanding
// host operation, if any.
func (e *Engine) Fail(n *Node, d Diagnostic) {
	if n == nil || n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled {
		d.Free(e.Alloc)
		return
	}
	if n.Submitted {
		e.cancellations = slices.Append(e.Alloc, e.cancellations, Cancellation{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: n.HostRequestID})
		n.Submitted, n.HostRequestID = false, 0
	}
	if n.materializer != nil {
		n.materializer.Free()
		n.materializer = nil
	}
	if n.HasCompletion {
		n.Completion.Value.Free(e.Alloc)
		n.Completion.Diagnostic.Free(e.Alloc)
		n.Completion, n.HasCompletion = Completion{}, false
	}
	n.complete(e, d)
}

func (e *Engine) ready(n *Node) bool {
	if n.Interest == 0 || n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled || n.State == NodeWaiting { return false }
	for i := range n.Static {
		dependency := n.Static[i]
		if dependency.State == NodeFailed || dependency.State == NodeCancelled {
			n.complete(e, dependency.Diagnostic.Clone(e.Alloc))
			return false
		}
		if dependency.State != NodeComplete { dependency.Requested = true; return false }
	}
	for i := range n.Dynamic {
		dependency := n.Dynamic[i]
		if dependency.State == NodeFailed || dependency.State == NodeCancelled {
			if slices.Contains(n.Observed, dependency) { continue }
			n.complete(e, dependency.Diagnostic.Clone(e.Alloc))
			return false
		}
		if !dependency.Current { dependency.Requested = true; return false }
	}
	return n.Requested
}

func (e *Engine) choose() *Node {
	var first *Node
	var selected *Node
	for i := range e.nodes {
		n := e.nodes[i]
		if !e.ready(n) || n.offered { continue }
		if first == nil || lessNode(n, first) { first = n }
		if e.lastRun != nil && lessNode(e.lastRun, n) && (selected == nil || lessNode(n, selected)) { selected = n }
	}
	if selected != nil { return selected }
	return first
}

func lessNode(left *Node, right *Node) bool {
	return left.Key.Name < right.Key.Name || (left.Key.Name == right.Key.Name && left.Key.Kind < right.Key.Kind)
}

// Ready returns up to capacity ready nodes in deterministic resource-key order.
func (e *Engine) Ready(capacity int) []*Node {
	if capacity <= 0 { return nil }
	var nodes []*Node
	for len(nodes) < capacity {
		var selected *Node
		for i := range e.nodes {
			n := e.nodes[i]
			if !e.ready(n) || n.offered || slices.Contains(nodes, n) { continue }
			if selected == nil || lessNode(n, selected) { selected = n }
		}
		if selected == nil { break }
		selected.offered = true
		nodes = slices.Append(e.Alloc, nodes, selected)
	}
	return nodes
}

// Dispatch starts one node returned by Ready. A claimed node starts once.
func (e *Engine) Dispatch(n *Node) *Node {
	if n == nil || !n.offered { return nil }
	return e.run(n)
}

// DispatchRoot starts a newly requested independent root without reentering
// the scheduler (and therefore without redispatching the current producer).
func (e *Engine) DispatchRoot(n *Node) *Node {
	if n == nil || !e.ready(n) { return nil }
	return e.run(n)
}

// Step handles one completion or executes one deterministic ready node.
func (e *Engine) Step() *Node {
	if len(e.completions) != 0 {
		c := e.completions[0]
		copy(e.completions, e.completions[1:])
		e.completions = e.completions[:len(e.completions)-1]
		return e.accept(c)
	}
	n := e.choose()
	if n == nil { return nil }
	return e.run(n)
}

// DrainCompletions accepts queued host completions without starting ready work.
func (e *Engine) DrainCompletions() {
	for len(e.completions) != 0 { e.Step() }
}

func (e *Engine) run(n *Node) *Node {
	e.lastRun = n
	n.offered = false
	if n.materializer != nil {
		n.Attempt++
		c := &EngineContext{engine: e, node: n}
		if n.HasCompletion { c.completion = n.Completion; n.Completion = Completion{}; n.HasCompletion = false }
		result := n.materializer.Next(c)
		// A dependency discovered while polling can fail this node immediately.
		// Its terminal state and diagnostic already belong to the engine.
		if n.State == NodeFailed || n.State == NodeCancelled {
			result.Value.Free(e.Alloc)
			result.Diagnostic.Free(e.Alloc)
			n.materializer.Free()
			n.materializer = nil
			return n
		}
		if result.Published {
			e.publish(n, result.Value)
			if n.State == NodeFailed {
				n.materializer.Free()
				n.materializer = nil
				return n
			}
		}
		if result.Terminal {
			n.materializer.Free()
			n.materializer = nil
			n.complete(e, result.Diagnostic)
		} else if result.Waiting { n.State = NodeWaiting } else { n.State = NodeReady }
		return n
	}
	if n.Producer == nil { n.complete(e, Diagnostic{Code: DiagnosticHostFailure}); return n }
	n.Attempt++
	c := &EngineContext{engine: e, node: n}
	if n.HasCompletion { c.completion = n.Completion; n.Completion = Completion{}; n.HasCompletion = false }
	result := n.Producer(c, n.ID)
	if n.State == NodeComplete || n.State == NodeFailed { return n }
	if n.materializer != nil && result == ProducerActive { return e.run(n) }
	if result == ProducerCompleted { n.complete(e, Diagnostic{})
	} else if result == ProducerFailed { n.complete(e, Diagnostic{Code: DiagnosticHostFailure})
	} else if result == ProducerWaiting || result == ProducerSubmitted {
		n.State = NodeWaiting
		if result == ProducerSubmitted && !n.Submitted { n.complete(e, Diagnostic{Code: DiagnosticHostFailure}) }
	} else { n.State = NodeReady }
	return n
}

func (e *Engine) Complete(c Completion) {
	queued := c
	if c.HasValue { queued.Value = c.Value.Clone(e.Alloc); c.Value.Free(e.Alloc) }
	e.completions = slices.Append(e.Alloc, e.completions, queued)
}

func (e *Engine) accept(c Completion) *Node {
	for i := range e.nodes {
		n := e.nodes[i]
		if n.ID != c.NodeID { continue }
		if !n.Submitted || n.Generation != c.Generation || n.Attempt != c.Attempt || n.HostRequestID != c.RequestID || n.State != NodeWaiting { c.Value.Free(e.Alloc); c.Diagnostic.Free(e.Alloc); return nil }
		n.Submitted = false
		n.HostRequestID = 0
		n.Completion, n.HasCompletion, n.State = c, true, NodeReady
		return n
	}
	c.Value.Free(e.Alloc)
	return nil
}

func (e *Engine) Invalidate(n *Node) {
 var reasons []*Node
 e.collectInvalidationReasons(n, false, &reasons)
 slices.Free(e.Alloc, reasons)
	var seen []*Node
	e.invalidate(n, &seen, false)
	slices.Free(e.Alloc, seen)
}

// Collect reasons before edges are removed, so a normal diamond path dominates
// an order-only path regardless of traversal order.
func (e *Engine) collectInvalidationReasons(n *Node, ordered bool, seen *[]*Node) {
	if n == nil {
		return
	}
	if slices.Contains(*seen, n) {
		if !n.InvalidatedForOrderOnly || ordered {
			return
		}
	} else {
		*seen = slices.Append(e.Alloc, *seen, n)
	}
	n.InvalidatedForOrderOnly = ordered
	for i := range n.Dependents {
		dependent := n.Dependents[i]
		e.collectInvalidationReasons(dependent, ordered || slices.Contains(dependent.OrderOnly, n), seen)
	}
}

func (e *Engine) invalidate(n *Node, seen *[]*Node, retain bool) {
	if n == nil { return }
	if slices.Contains(*seen, n) { return }
	*seen = slices.Append(e.Alloc, *seen, n)
	n.invalidating = true
	// Invalidating a child can remove its dynamic reverse edge from this node.
	// Traverse a stable copy so that mutation cannot skip a sibling.
	dependents := slices.Clone(e.Alloc, n.Dependents)
	if n.Submitted {
		e.cancellations = slices.Append(e.Alloc, e.cancellations, Cancellation{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: n.HostRequestID})
	}
	n.Generation++
	n.Submitted = false
	n.HostRequestID = 0
	if n.HasCompletion { n.Completion.Value.Free(e.Alloc); n.Completion = Completion{}; n.HasCompletion = false }
	n.Diagnostic.Free(e.Alloc)
	n.State, n.Requested, n.Diagnostic = NodeIdle, n.Interest != 0, Diagnostic{}
	n.offered = false
	if n.materializer != nil { n.materializer.Free(); n.materializer = nil }
	if n.Current { n.Latest.Free(e.Alloc); n.Current = false }
	for i := range n.Dynamic {
		d := n.Dynamic[i]
		removeDependent(d, n)
		if retain {
			n.previousDynamic = slices.Append(e.Alloc, n.previousDynamic, d)
		} else if n.Interest != 0 { e.interest(d, -n.Interest) }
	}
	if !retain { e.releasePreviousDependencies(n) }
	slices.Free(e.Alloc, n.Dynamic); n.Dynamic = nil
	slices.Free(e.Alloc, n.Observed); n.Observed = nil
 slices.Free(e.Alloc, n.OrderOnly); n.OrderOnly = nil
	e.emit(n, Event{Kind: UpdateInvalidated})
	for i := range dependents { e.invalidate(dependents[i], seen, retain) }
	slices.Free(e.Alloc, dependents)
	n.invalidating = false
}

// Preserve the interest of old edges until a restarted invocation has rebound
// its dependencies. Otherwise invalidating a sole consumer cancels the source
// that is currently publishing the update which caused the restart.
func (e *Engine) restartFromPublication(n *Node) {
 var seen []*Node
 e.collectInvalidationReasons(n, false, &seen)
 slices.Free(e.Alloc, seen)
 seen = nil
 e.invalidate(n, &seen, true)
 slices.Free(e.Alloc, seen)
}

func (e *Engine) releasePreviousDependencies(n *Node) {
 previous := n.previousDynamic
 n.previousDynamic = nil
 for i := range previous {
  if n.Interest != 0 { e.interest(previous[i], -n.Interest) }
 }
 slices.Free(e.Alloc, previous)
}

func removeDependent(n, dependent *Node) {
	for i := range n.Dependents { if n.Dependents[i] == dependent { copy(n.Dependents[i:], n.Dependents[i+1:]); n.Dependents = n.Dependents[:len(n.Dependents)-1]; return } }
}

// Cancel removes the convenience Request interest. Roots use Release instead.
func (e *Engine) Cancel(n *Node) {
	if n != nil && n.RequestInterest != 0 {
		n.RequestInterest = 0
		e.interest(n, -1)
	}
}

func (e *Engine) cancel(n *Node) {
	if n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled { return }
	if n.materializer != nil { n.materializer.Free(); n.materializer = nil }
	if n.HasCompletion { n.Completion.Value.Free(e.Alloc); n.Completion = Completion{}; n.HasCompletion = false }
	if n.Submitted { e.cancellations = slices.Append(e.Alloc, e.cancellations, Cancellation{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: n.HostRequestID}) }
	n.Submitted = false
	n.HostRequestID = 0
	e.releasePreviousDependencies(n)
	n.Generation++; n.Requested = false; n.State = NodeCancelled; n.Diagnostic = Diagnostic{Code: DiagnosticCancelled}
	e.emit(n, terminal(n))
	for i := range n.Dependents {
		if n.Dependents[i].State == NodeWaiting { n.Dependents[i].State = NodeReady }
	}
}

func (e *Engine) NextCancellation() Cancellation {
	if len(e.cancellations) == 0 { return Cancellation{} }
	c := e.cancellations[0]
	copy(e.cancellations, e.cancellations[1:])
	e.cancellations = e.cancellations[:len(e.cancellations)-1]
	return c
}

func (e *Engine) Unsubscribe(s *Subscription) {
	if s == nil || s.unsubbed { return }
	n := s.Node
	for i := range n.Subs { if n.Subs[i] == s { copy(n.Subs[i:], n.Subs[i+1:]); n.Subs = n.Subs[:len(n.Subs)-1]; break } }
	e.interest(n, -1)
	s.free()
}

func (e *Engine) Free() {
	if e == nil { return }
	for i := range e.roots { mem.Free(e.Alloc, e.roots[i]) }
	slices.Free(e.Alloc, e.roots)
	for i := range e.nodes {
		n := e.nodes[i]
		for j := range n.Subs { n.Subs[j].free() }
		slices.Free(e.Alloc, n.Subs); slices.Free(e.Alloc, n.Static); slices.Free(e.Alloc, n.Dynamic); slices.Free(e.Alloc, n.previousDynamic); slices.Free(e.Alloc, n.Dependents)
		slices.Free(e.Alloc, n.Observed)
  slices.Free(e.Alloc, n.OrderOnly)
		if n.Current { n.Latest.Free(e.Alloc) }
		if n.materializer != nil { n.materializer.Free() }
		if n.ContextFree != nil { n.ContextFree(e.Alloc, n.Context) }
		if n.HasCompletion { n.Completion.Value.Free(e.Alloc) }
		n.Diagnostic.Free(e.Alloc)
		n.Key.Free(e.Alloc); mem.Free(e.Alloc, n)
	}
	for i := range e.completions { e.completions[i].Value.Free(e.Alloc); e.completions[i].Diagnostic.Free(e.Alloc) }
	slices.Free(e.Alloc, e.nodes); slices.Free(e.Alloc, e.cancellations); slices.Free(e.Alloc, e.completions); mem.Free(e.Alloc, e)
}

// TrackedNodes returns a caller-owned slice of borrowed runtime nodes.
func (e *Engine) TrackedNodes() []*Node { return slices.Clone(e.Alloc, e.nodes) }

func (e *Engine) Lookup(key ResourceKey) *Node { return e.find(key) }
