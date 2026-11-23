package core

import (
	"kame/internal/mem"
	"path"
	"slices"
)

type Root struct {
	node      *Node
	live      bool
	suspended bool
}

type Engine struct {
	Alloc mem.Allocator
	// CanRun optionally limits dispatch without preventing dependency validation.
	CanRun        func(*Node) bool
	nodes         []*Node
	nodeIndex     []nodeIndexEntry
	roots         []*Root
	nextID        int64
	cancellations []Cancellation
	suspensions   []Suspension
	completions   []Completion
	nextUpdate    int64
	lastRun       *Node
	// changes counts mutations that can falsify an accepted fact: a current
	// node republished, failed or invalidated, a node removed, or an edge
	// dropped. A first publication does not count; a fact needs a current node.
	changes       int64
	checks        checkQueue
	dirtyHeights  []*Node
	runnable      []*Node // structurally ready, in resource-key order
	fileObservers map[string]map[*Node]struct{}
	// TrackTouched records the nodes published or ended (complete, failed or
	// cancelled) for TakeTouched, so an observer need not scan every node.
	TrackTouched bool
	touched      []*Node
}

func (e *Engine) touch(n *Node) {
	if e.TrackTouched && !n.touchQueued {
		n.touchQueued = true
		e.touched = append(e.touched, n)
	}
}

// TakeTouched returns the nodes touched since the last call, each once, and
// transfers the slice to the caller.
func (e *Engine) TakeTouched() []*Node {
	touched := e.touched
	e.touched = nil
	for _, n := range touched {
		n.touchQueued = false
	}
	return touched
}

// Changes returns the engine's change stamp. While it is unchanged, facts
// validated against current nodes and edges remain valid.
func (e *Engine) Changes() int64 { return e.changes }

type nodeIndexEntry struct {
	kind ResourceKind
	name string
	node *Node
}

func NewEngine(a mem.Allocator) *Engine {
	e := mem.Alloc[Engine](a)
	e.Alloc = a
	return e
}

func canonicalName(a mem.Allocator, key ResourceKey) (string, bool) {
	if key.Kind == ResourceFile {
		if IsResourceURIName(key.Name) {
			return cloneResourceURIText(a, key.Name), true
		}
		return path.Clean(key.Name), true
	}
	return key.Name, false
}

func (e *Engine) find(key ResourceKey) *Node {
	name, owned := canonicalName(e.Alloc, key)
	lo, hi := 0, len(e.nodeIndex)
	for lo < hi {
		mid := lo + (hi-lo)/2
		entry := e.nodeIndex[mid]
		if entry.kind < key.Kind || (entry.kind == key.Kind && entry.name < name) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	var n *Node
	if lo < len(e.nodeIndex) && e.nodeIndex[lo].kind == key.Kind && e.nodeIndex[lo].name == name {
		n = e.nodeIndex[lo].node
	}
	if owned {
		mem.FreeString(e.Alloc, name)
	}
	return n
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
	if owned {
		mem.FreeString(e.Alloc, name)
	}
	n.State = NodeIdle
	e.nodes = append(e.nodes, n)
	lo, hi := 0, len(e.nodeIndex)
	for lo < hi {
		mid := lo + (hi-lo)/2
		entry := e.nodeIndex[mid]
		if entry.kind < n.Key.Kind || (entry.kind == n.Key.Kind && entry.name < n.Key.Name) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	e.nodeIndex = append(e.nodeIndex, nodeIndexEntry{kind: n.Key.Kind, name: n.Key.Name, node: n})
	copy(e.nodeIndex[lo+1:], e.nodeIndex[lo:])
	e.nodeIndex[lo] = nodeIndexEntry{kind: n.Key.Kind, name: n.Key.Name, node: n}
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

func (e *Engine) RequestRoot(n *Node) *Root {
	if n == nil {
		return nil
	}
	r := mem.Alloc[Root](e.Alloc)
	r.node, r.live = n, true
	e.roots = append(e.roots, r)
	e.interest(n, 1)
	e.request(n)
	return r
}

// Request is the idempotent convenience root for a node.
func (e *Engine) Request(n *Node) {
	if n != nil && n.RequestInterest == 0 {
		n.RequestInterest = 1
		e.interest(n, 1)
		e.request(n)
	}
}

func (e *Engine) demand(n *Node) {
	if !n.Requested {
		n.Requested = true
		e.enqueue(n)
	}
}

func (e *Engine) request(n *Node) {
	e.demand(n)
	for i := range n.Static {
		e.request(n.Static[i])
	}
	for i := range n.Dynamic {
		e.request(n.Dynamic[i])
	}
}

func (e *Engine) Release(r *Root) {
	if r == nil || !r.live {
		return
	}
	r.live = false
	suspended := int64(0)
	if r.suspended {
		suspended = -1
	}
	e.adjustInterest(r.node, -1, suspended)
	for i := range e.roots {
		if e.roots[i] == r {
			copy(e.roots[i:], e.roots[i+1:])
			e.roots = e.roots[:len(e.roots)-1]
			break
		}
	}
	mem.Free(e.Alloc, r)
}

func (e *Engine) interest(n *Node, delta int64) { e.adjustInterest(n, delta, 0) }

// adjustInterest changes a node's interest by delta, of which suspended comes
// from suspended roots, and propagates it to dependencies. A node whose
// suspension state flips while it holds host work queues a host request.
func (e *Engine) adjustInterest(n *Node, delta, suspended int64) {
	if delta < 0 && n.Interest+delta == 0 && !n.invalidating && !n.tearingDown && n.BeginTeardown != nil {
		e.beginTeardown(n, false)
	}
	was := n.suspended()
	previous := n.Interest
	n.Interest += delta
	n.SuspendedInterest += suspended
	if (previous == 0) != (n.Interest == 0) || was != n.suspended() {
		e.enqueue(n)
	}
	for i := range n.Static {
		e.adjustInterest(n.Static[i], delta, suspended)
	}
	for i := range n.Dynamic {
		e.adjustInterest(n.Dynamic[i], delta, suspended)
	}
	for i := range n.previousDynamic {
		e.adjustInterest(n.previousDynamic[i], delta, suspended)
	}
	if now := n.suspended(); now != was && n.Submitted && n.HostRequestID != 0 {
		e.suspensions = append(e.suspensions, Suspension{NodeID: n.ID, RequestID: n.HostRequestID, Suspend: now})
	}
	if delta != 0 && n.Interest == 0 && !n.invalidating && n.State != NodeComplete && n.State != NodeFailed && n.State != NodeCancelled {
		e.cancel(n)
	}
}

// Suspension asks the host to suspend or resume one request (002).
type Suspension struct {
	NodeID    int64
	RequestID int64
	Suspend   bool
}

// Suspend pauses a live root without abandoning its work: nodes only it
// (and other suspended roots) are interested in are no longer offered.
func (e *Engine) Suspend(r *Root) {
	if r != nil && r.live && !r.suspended {
		r.suspended = true
		e.adjustInterest(r.node, 0, 1)
	}
}

// Resume makes a suspended root's work runnable again.
func (e *Engine) Resume(r *Root) {
	if r != nil && r.live && r.suspended {
		r.suspended = false
		e.adjustInterest(r.node, 0, -1)
	}
}

// Suspended reports whether every interest in n comes from suspended roots.
func (e *Engine) Suspended(n *Node) bool { return n != nil && n.suspended() }

// Reaches reports whether n is part of the root's graph.
func (r *Root) Reaches(n *Node) bool { return r != nil && r.live && n != nil && reaches(r.node, n) }

// Node returns the requested node of a root.
func (r *Root) Node() *Node { return r.node }

// NextSuspension returns the next queued host suspension or resume request.
func (e *Engine) NextSuspension() Suspension {
	if len(e.suspensions) == 0 {
		return Suspension{}
	}
	s := e.suspensions[0]
	e.suspensions = slices.Delete(e.suspensions, 0, 1)
	return s
}

func (e *Engine) Subscribe(n *Node) *Subscription {
	if n == nil {
		return nil
	}
	s := mem.Alloc[Subscription](e.Alloc)
	s.Alloc, s.Node = e.Alloc, n
	n.Subs = append(n.Subs, s)
	e.interest(n, 1)
	e.request(n)
	if n.Current {
		s.pushValue(Event{Kind: UpdateValue, NodeID: n.ID, Value: n.Latest.CloneOwned(e.Alloc), Revision: n.Revision})
	}
	if n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled {
		s.terminal = terminal(n)
	}
	return s
}

func (e *Engine) emit(n *Node, event Event) {
	e.nextUpdate++
	if event.NodeID == 0 {
		event.NodeID = n.ID
	}
	event.Order = e.nextUpdate
	for i := range n.Subs {
		s := n.Subs[i]
		if event.HasValue() {
			s.pushValue(Event{Kind: event.Kind, NodeID: event.NodeID, DependencyID: event.DependencyID, Order: event.Order, Value: event.Value.CloneOwned(e.Alloc), Revision: event.Revision})
		} else if event.Terminal() {
			s.terminal = event
		} else {
			if len(s.updates) == 8 {
				s.updates = s.updates[:1]
			}
			s.updates = append(s.updates, event)
		}
	}
}

func (e *Engine) publish(n *Node, value Value) {
	e.publishSigned(n, value, ValueSignature(value))
}

func (e *Engine) publishSigned(n *Node, value Value, signature Signature) {
	if value.HasTransientCallable() {
		value.Free(e.Alloc)
		n.complete(e, Diagnostic{Code: DiagnosticExprValue})
		return
	}
	if n.Current {
		n.Latest.Free(e.Alloc)
		e.changes++
	}
	n.Latest, n.Current = value.Clone(e.Alloc), true
	n.Latest.Share(e.Alloc)
	n.Signature = signature
	value.Free(e.Alloc)
	n.Revision++
	e.touch(n)
	e.syncValidationInput(n)
	e.releasePreviousDependencies(n)
	e.emit(n, Event{Kind: UpdateValue, Value: n.Latest, Revision: n.Revision})
	dependents := slices.Clone(n.Dependents)
	for i := range dependents {
		dependent := dependents[i]
		// A host request was derived from an older dependency snapshot. Cancel it
		// and restart from the newest values instead of accepting its completion.
		if dependent.State == NodeWaiting && !dependent.ValidationPending {
			if dependent.Submitted {
				e.restartFromPublication(dependent)
				continue
			}
			dependent.State = NodeReady
		}
		e.enqueue(dependent)
	}
	mem.FreeSlice(nil, dependents)
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
		e.cancellations = append(e.cancellations, Cancellation{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: n.HostRequestID})
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

// Step handles one completion or executes one deterministic ready node.
func (e *Engine) Step() *Node {
	if len(e.completions) != 0 {
		c := e.completions[0]
		copy(e.completions, e.completions[1:])
		e.completions = e.completions[:len(e.completions)-1]
		return e.accept(c)
	}
	n := e.choose(e.lastRun)
	if n == nil {
		return nil
	}
	return e.run(n)
}

// DrainCompletions accepts queued host completions without starting ready work.
func (e *Engine) DrainCompletions() {
	for len(e.completions) != 0 {
		e.Step()
	}
}

func (e *Engine) run(n *Node) *Node {
	e.lastRun = n
	n.offered = false
	e.setRunnable(n, false)
	n.running = true
	defer func() {
		n.running = false
		e.enqueue(n)
	}()
	if n.materializer != nil && !n.tearingDown {
		n.Attempt++
		c := &EngineContext{engine: e, node: n}
		if n.HasCompletion {
			c.completion = n.Completion
			n.Completion = Completion{}
			n.HasCompletion = false
		}
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
		} else if result.Waiting {
			n.State = NodeWaiting
		} else {
			n.State = NodeReady
		}
		return n
	}
	if n.Producer == nil {
		n.complete(e, Diagnostic{Code: DiagnosticHostFailure})
		return n
	}
	n.Attempt++
	c := &EngineContext{engine: e, node: n}
	if n.HasCompletion {
		c.completion = n.Completion
		n.Completion = Completion{}
		n.HasCompletion = false
	}
	var result ProducerResult
	if n.tearingDown {
		result = n.Teardown(c, n.ID)
		if result == ProducerCompleted || result == ProducerFailed {
			e.finishTeardown(n)
			return n
		}
	} else {
		result = n.Producer(c, n.ID)
	}
	if n.State == NodeComplete || n.State == NodeFailed {
		return n
	}
	if n.materializer != nil && !n.tearingDown && result == ProducerActive {
		return e.run(n)
	}
	if result == ProducerRestart {
		e.Invalidate(n)
		return n
	}
	if result == ProducerCompleted {
		n.complete(e, Diagnostic{})
	} else if result == ProducerFailed {
		n.complete(e, Diagnostic{Code: DiagnosticHostFailure})
	} else if result == ProducerWaiting || result == ProducerSubmitted {
		n.State = NodeWaiting
		if result == ProducerSubmitted && !n.Submitted {
			n.complete(e, Diagnostic{Code: DiagnosticHostFailure})
		}
	} else {
		n.State = NodeReady
	}
	return n
}

// Complete consumes Value and Diagnostic, allocated with e.Alloc. The queue
// owns them even for stale requests; callers must not free or reuse them.
func (e *Engine) Complete(c Completion) {
	e.completions = append(e.completions, c)
}

func (e *Engine) accept(c Completion) *Node {
	if n := e.LookupID(c.NodeID); n != nil {
		if !n.Submitted || n.Generation != c.Generation || n.Attempt != c.Attempt || n.HostRequestID != c.RequestID || n.State != NodeWaiting {
			c.Value.Free(e.Alloc)
			c.Diagnostic.Free(e.Alloc)
			return nil
		}
		n.Submitted = false
		n.HostRequestID = 0
		n.Completion, n.HasCompletion, n.State = c, true, NodeReady
		e.enqueue(n)
		return n
	}
	c.Value.Free(e.Alloc)
	c.Diagnostic.Free(e.Alloc)
	return nil
}

func (e *Engine) Invalidate(n *Node) {
	e.invalidateTree(n, false)
}

func (e *Engine) invalidateTree(n *Node, retain bool) {
	seen := make(map[*Node]bool)
	e.collectInvalidationReasons(n, false, seen)
	clear(seen)
	e.invalidate(n, seen, retain)
}

// Collect reasons before edges are removed, so a normal diamond path dominates
// an order-only path regardless of traversal order.
func (e *Engine) collectInvalidationReasons(n *Node, ordered bool, seen map[*Node]bool) {
	if n == nil {
		return
	}
	if seen[n] {
		if !n.InvalidatedForOrderOnly || ordered {
			return
		}
	} else {
		seen[n] = true
	}
	n.InvalidatedForOrderOnly = ordered
	for i := range n.Dependents {
		dependent := n.Dependents[i]
		e.collectInvalidationReasons(dependent, ordered || slices.Contains(dependent.OrderOnly, n), seen)
	}
}

// A nil seen set refreshes this one node; its consumers retain their proofs.
func (e *Engine) invalidate(n *Node, seen map[*Node]bool, retain bool) {
	if n == nil || seen[n] {
		return
	}
	if seen != nil {
		seen[n] = true
	}
	n.invalidating = true
	e.changes++
	if e.beginTeardown(n, true) {
		n.observationEpoch++
		n.invalidating = false
		return
	}
	// Invalidating a child can remove its dynamic reverse edge from this node.
	// Traverse a stable copy so that mutation cannot skip a sibling.
	var dependents []*Node
	if seen != nil {
		dependents = slices.Clone(n.Dependents)
	}
	if n.Submitted {
		e.cancellations = append(e.cancellations, Cancellation{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: n.HostRequestID})
	}
	n.Generation++
	n.ValidationPending = false
	n.Submitted = false
	n.HostRequestID = 0
	if n.HasCompletion {
		n.Completion.Value.Free(e.Alloc)
		n.Completion.Diagnostic.Free(e.Alloc)
		n.Completion = Completion{}
		n.HasCompletion = false
	}
	// Terminal slots borrow this generation's diagnostic; obsolete terminals
	// must not survive its owner. Retain independently owned values and metadata.
	for i := range n.Subs {
		n.Subs[i].terminal = Event{}
	}
	n.Diagnostic.Free(e.Alloc)
	n.State, n.Requested, n.Diagnostic = NodeIdle, n.Interest != 0, Diagnostic{}
	n.offered = false
	if n.materializer != nil {
		n.materializer.Free()
		n.materializer = nil
	}
	if n.Current {
		n.Latest.Free(e.Alloc)
		n.Current = false
	}
	e.syncValidationInput(n)
	e.enqueue(n)
	e.dropDynamicDependencies(n, 0, retain)
	if !retain {
		e.releasePreviousDependencies(n)
	}
	mem.FreeSlice(nil, n.Observed)
	n.Observed = nil
	mem.FreeSlice(nil, n.OrderOnly)
	n.OrderOnly = nil
	e.replaceObservations(n, nil)
	e.emit(n, Event{Kind: UpdateInvalidated})
	for _, d := range dependents {
		e.invalidate(d, seen, retain)
	}
	mem.FreeSlice(nil, dependents)
	n.invalidating = false
}

// Preserve the interest of old edges until a restarted invocation has rebound
// its dependencies. Otherwise invalidating a sole consumer cancels the source
// that is currently publishing the update which caused the restart.
func (e *Engine) restartFromPublication(n *Node) {
	e.invalidateTree(n, true)
}

// Cancel removes the convenience Request interest. Roots use Release instead.
func (e *Engine) Cancel(n *Node) {
	if n != nil && n.RequestInterest != 0 {
		n.RequestInterest = 0
		e.interest(n, -1)
	}
}

// CancelOwned ends an independently owned node even when observers still
// hold interest in its result.
func (e *Engine) CancelOwned(n *Node) { e.cancel(n) }

// CancelRequest requests host cancellation while leaving the evaluator live
// so it can unwind after the host acknowledges the terminal result.
func (e *Engine) CancelRequest(n *Node) {
	if n != nil && n.Submitted {
		e.cancellations = append(e.cancellations, Cancellation{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: n.HostRequestID})
	}
}

func (e *Engine) cancel(n *Node) {
	if n.State == NodeComplete || n.State == NodeFailed || n.State == NodeCancelled {
		return
	}
	e.changes++
	if e.beginTeardown(n, false) {
		return
	}
	if n.materializer != nil {
		n.materializer.Free()
		n.materializer = nil
	}
	if n.HasCompletion {
		n.Completion.Value.Free(e.Alloc)
		n.Completion.Diagnostic.Free(e.Alloc)
		n.Completion = Completion{}
		n.HasCompletion = false
	}
	if n.Submitted {
		e.cancellations = append(e.cancellations, Cancellation{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: n.HostRequestID})
	}
	n.Submitted = false
	n.HostRequestID = 0
	e.releasePreviousDependencies(n)
	n.Generation++
	n.Requested = false
	n.State = NodeCancelled
	e.syncValidationInput(n)
	e.enqueue(n)
	e.touch(n)
	if n.teardownDiagnostic.Code != "" {
		n.Diagnostic = n.teardownDiagnostic
		n.teardownDiagnostic = Diagnostic{}
	} else {
		n.Diagnostic = Diagnostic{Code: DiagnosticCancelled}
	}
	e.emit(n, terminal(n))
	for i := range n.Dependents {
		n.Dependents[i].offered = false
		if n.Dependents[i].State == NodeWaiting {
			n.Dependents[i].State = NodeReady
		}
		e.enqueue(n.Dependents[i])
	}
}

func (e *Engine) NextCancellation() Cancellation {
	if len(e.cancellations) == 0 {
		return Cancellation{}
	}
	c := e.cancellations[0]
	copy(e.cancellations, e.cancellations[1:])
	e.cancellations = e.cancellations[:len(e.cancellations)-1]
	return c
}

func (e *Engine) Unsubscribe(s *Subscription) {
	if s == nil || s.unsubbed {
		return
	}
	n := s.Node
	for i := range n.Subs {
		if n.Subs[i] == s {
			copy(n.Subs[i:], n.Subs[i+1:])
			n.Subs = n.Subs[:len(n.Subs)-1]
			break
		}
	}
	e.interest(n, -1)
	s.free()
}

func (e *Engine) Free() {
	if e == nil {
		return
	}
	for i := range e.roots {
		mem.Free(e.Alloc, e.roots[i])
	}
	mem.FreeSlice(nil, e.roots)
	mem.FreeSlice(nil, e.touched)
	e.fileObservers = nil
	for i := range e.nodes {
		n := e.nodes[i]
		e.freeNode(n)
	}
	for i := range e.completions {
		e.completions[i].Value.Free(e.Alloc)
		e.completions[i].Diagnostic.Free(e.Alloc)
	}
	mem.FreeSlice(nil, e.nodes)
	mem.FreeSlice(nil, e.nodeIndex)
	mem.FreeSlice(nil, e.cancellations)
	mem.FreeSlice(nil, e.suspensions)
	mem.FreeSlice(nil, e.completions)
	mem.Free(e.Alloc, e)
}

// Retire discards an unobserved terminal execution node. Callers must first
// release every root and consumer edge, and must not retain the borrowed node.
// Resource/definition nodes with subscribers or dependents are never removed.
func (e *Engine) Retire(n *Node) bool {
	index := -1
	for i := range e.nodes {
		if e.nodes[i] == n {
			index = i
			break
		}
	}
	if index < 0 || n.Interest != 0 || len(n.Dependents) != 0 || len(n.Subs) != 0 || n.materializer != nil || n.Submitted || n.running || n.tearingDown || (n.State != NodeComplete && n.State != NodeFailed && n.State != NodeCancelled) {
		return false
	}
	// A restarting consumer keeps previous edges without a reverse edge.
	for _, consumer := range e.nodes {
		if slices.Contains(consumer.previousDynamic, n) {
			return false
		}
	}
	e.changes++
	if n.touchQueued {
		for i := range e.touched {
			if e.touched[i] == n {
				e.touched = slices.Delete(e.touched, i, i+1)
				break
			}
		}
	}
	for _, dependency := range n.Static {
		e.unlinkDependency(n, dependency, false)
	}
	e.dropDynamicDependencies(n, 0, false)
	e.unschedule(n)
	e.nodes = slices.Delete(e.nodes, index, index+1)
	for i := range e.nodeIndex {
		if e.nodeIndex[i].node == n {
			e.nodeIndex = slices.Delete(e.nodeIndex, i, i+1)
			break
		}
	}
	if e.lastRun == n {
		e.lastRun = nil
	}
	e.freeNode(n)
	return true
}

func (e *Engine) freeNode(n *Node) {
	for i := range n.Subs {
		n.Subs[i].free()
	}
	mem.FreeSlice(nil, n.Subs)
	mem.FreeSlice(nil, n.Static)
	mem.FreeSlice(nil, n.Dynamic)
	mem.FreeSlice(nil, n.observationOrder)
	mem.FreeSlice(nil, n.previousDynamic)
	mem.FreeSlice(nil, n.Dependents)
	mem.FreeSlice(nil, n.Observed)
	mem.FreeSlice(nil, n.OrderOnly)
	e.replaceObservations(n, nil)
	if n.Current {
		n.Latest.Free(e.Alloc)
	}
	if n.materializer != nil {
		n.materializer.Free()
	}
	if n.ContextFree != nil {
		n.ContextFree(e.Alloc, n.Context)
	}
	if n.HasCompletion {
		n.Completion.Value.Free(e.Alloc)
		n.Completion.Diagnostic.Free(e.Alloc)
	}
	n.Diagnostic.Free(e.Alloc)
	n.Key.Free(e.Alloc)
	n.teardownDiagnostic.Free(e.Alloc)
	mem.Free(e.Alloc, n)
}

// TrackedNodes returns a caller-owned slice of borrowed runtime nodes.
func (e *Engine) TrackedNodes() []*Node { return slices.Clone(e.nodes) }

func (e *Engine) Lookup(key ResourceKey) *Node { return e.find(key) }
