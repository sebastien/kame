package core

import (
	"cmp"
	"kame/internal/mem"
	"maps"
	"slices"
	"strings"
)

// ObservationAspect distinguishes independent views of the same resource.
type ObservationAspect int

const (
	ObservationValue ObservationAspect = iota
	ObservationContent
	ObservationExistence
	ObservationMetadata
	ObservationLinkMetadata
	ObservationLinkTarget
)

// Observation is a generation's accepted resource identity. It is independent
// of scheduling edges: context-local reads and operation versions are facts too.
type Observation struct {
	Key       ResourceKey
	Aspect    ObservationAspect
	Signature Signature
}

// ObservationView is a read-only, borrowed view of a generation's facts.
// Do not retain the view across evaluation.
type ObservationView struct{ items []Observation }

func (v ObservationView) Len() int             { return len(v.items) }
func (v ObservationView) At(i int) Observation { return v.items[i] }

func (v ObservationView) All(yield func(Observation) bool) {
	for _, item := range v.items {
		if !yield(item) {
			return
		}
	}
}

// AcceptedObservations returns facts recorded by this node's current generation.
func (n *Node) AcceptedObservations() ObservationView { return ObservationView{n.observations} }

// AcceptedObservations returns the active producer's accepted facts.
func (c *EngineContext) AcceptedObservations() ObservationView { return c.node.AcceptedObservations() }

func FreeObservations(a mem.Allocator, values []Observation) {
	for i := range values {
		values[i].Key.Free(a)
	}
	mem.FreeSlice(nil, values)
}

func (e *Engine) indexObservation(n *Node, item Observation) {
	if item.Key.Kind != ResourceFile || item.Aspect != ObservationContent {
		return
	}
	if e.fileObservers == nil {
		e.fileObservers = make(map[string]map[*Node]struct{})
	}
	nodes := e.fileObservers[item.Key.Name]
	if nodes == nil {
		nodes = make(map[*Node]struct{})
		e.fileObservers[item.Key.Name] = nodes
	}
	nodes[n] = struct{}{}
}

// replaceObservations takes ownership of facts and keeps the reverse index
// consistent with the node and its per-generation observation order.
func (e *Engine) replaceObservations(n *Node, facts []Observation) {
	for _, item := range n.observations {
		if item.Key.Kind == ResourceFile && item.Aspect == ObservationContent {
			nodes := e.fileObservers[item.Key.Name]
			delete(nodes, n)
			if len(nodes) == 0 {
				delete(e.fileObservers, item.Key.Name)
			}
		}
	}
	FreeObservations(e.Alloc, n.observations)
	n.observations = facts
	n.observationEpoch++
	for _, item := range facts {
		e.indexObservation(n, item)
	}
}

// RevalidateResource refreshes a resource and any context-local content reads
// without graph edges. Revalidating one observer can discard another's facts.
func (e *Engine) RevalidateResource(key ResourceKey) {
	resource := e.Lookup(key)
	if resource != nil {
		e.Revalidate(resource)
	}
	if key.Kind != ResourceFile || len(e.fileObservers) == 0 {
		return
	}
	name, owned := canonicalName(e.Alloc, key)
	if owned {
		defer mem.FreeString(e.Alloc, name)
	}
	nodes := slices.SortedFunc(maps.Keys(e.fileObservers[name]), func(a, b *Node) int { return cmp.Compare(a.ID, b.ID) })
	for _, n := range nodes {
		if _, observed := e.fileObservers[name][n]; !observed || n == resource {
			continue
		}
		if resource != nil && (slices.Contains(n.Static, resource) || slices.Contains(n.Dynamic, resource)) {
			continue
		}
		e.Revalidate(n)
	}
}

// Observe records what the computation actually consumed, not a later live
// read. Conflicting observations make the generation non-reusable.
func (c *EngineContext) Observe(key ResourceKey, signature Signature) {
	c.ObserveAspect(key, ObservationValue, signature)
}

// ObserveAspect keeps intentional metadata and existence reads separate from
// content reads of the same resource. Neither proves the other unchanged.
func (c *EngineContext) ObserveAspect(key ResourceKey, aspect ObservationAspect, signature Signature) {
	if c.Failed() || c.node.State == NodeComplete {
		return
	}
	n := c.node
	// Stored keys are canonical, so an exact match is the same resource; this
	// skips canonicalization for repeated observations (every source-defined
	// function call re-observes its source file).
	if i, found := n.findObservation(key.Kind, key.Name, aspect); found {
		n.mergeObservation(i, signature)
		return
	}
	name, owned := canonicalName(c.engine.Alloc, key)
	key.Name = name
	i, found := n.findObservation(key.Kind, key.Name, aspect)
	if found {
		n.mergeObservation(i, signature)
	} else {
		item := Observation{Key: key.Clone(c.engine.Alloc), Aspect: aspect, Signature: signature}
		n.observationOrder = slices.Insert(n.observationOrder, i, len(n.observations))
		n.observations = append(n.observations, item)
		c.engine.indexObservation(n, item)
	}
	if owned {
		mem.FreeString(c.engine.Alloc, name)
	}
}

// mergeObservation records a repeated observation of order position i;
// conflicting observations make the generation non-reusable.
func (n *Node) mergeObservation(i int, signature Signature) {
	item := &n.observations[n.observationOrder[i]]
	// Only a fact becoming unavailable changes the epoch; once unavailable,
	// later observations of it change nothing.
	if item.Signature.Equal(item.Signature) && !item.Signature.Equal(signature) {
		item.Signature = Signature{}
		n.observationEpoch++
	}
}

func compareObservation(item *Observation, kind ResourceKind, name string, aspect ObservationAspect) int {
	switch {
	case item.Key.Kind != kind:
		if item.Key.Kind < kind {
			return -1
		}
		return 1
	case item.Key.Name != name:
		return strings.Compare(item.Key.Name, name)
	case item.Aspect < aspect:
		return -1
	case item.Aspect > aspect:
		return 1
	}
	return 0
}

// searchObservations binary searches order, indexes of items sorted by key
// and aspect, for the position of a fact.
func searchObservations(items []Observation, order []int, kind ResourceKind, name string, aspect ObservationAspect) (int, bool) {
	lo, hi := 0, len(order)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if compareObservation(&items[order[mid]], kind, name, aspect) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, lo < len(order) && compareObservation(&items[order[lo]], kind, name, aspect) == 0
}

// orderObservations appends the indexes of items to order sorted by key and
// aspect; ok is false when two items share a key and aspect.
func orderObservations(items []Observation, order []int) ([]int, bool) {
	ok := true
	for i := range items {
		position, found := searchObservations(items, order, items[i].Key.Kind, items[i].Key.Name, items[i].Aspect)
		ok = ok && !found
		order = slices.Insert(order, position, i)
	}
	return order, ok
}

// findObservation searches observationOrder, rebuilding it when observations
// was replaced rather than appended to. A rule reading N files observes N
// facts, so a linear scan per observation would be quadratic.
func (n *Node) findObservation(kind ResourceKind, name string, aspect ObservationAspect) (int, bool) {
	if len(n.observationOrder) != len(n.observations) || n.orderEpoch != n.observationEpoch {
		n.observationOrder, _ = orderObservations(n.observations, n.observationOrder[:0])
		n.orderEpoch = n.observationEpoch
	}
	return searchObservations(n.observations, n.observationOrder, kind, name, aspect)
}

func validObservation(item *Observation) bool {
	return item.Key.Kind >= ResourceDefinition && item.Key.Kind <= ResourceOperation && item.Key.Name != "" &&
		item.Aspect >= ObservationValue && item.Aspect <= ObservationLinkTarget
}

// sameObservations compares two observation sets in O(n log n). Persisted
// records are untrusted sets, not lists with multiplicity.
func sameObservations(left []Observation, right []Observation) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !validObservation(&left[i]) || !validObservation(&right[i]) {
			return false
		}
	}
	leftOrder, leftSet := orderObservations(left, nil)
	rightOrder, rightSet := orderObservations(right, nil)
	same := leftSet && rightSet
	for i := 0; same && i < len(leftOrder); i++ {
		l, r := &left[leftOrder[i]], &right[rightOrder[i]]
		same = compareObservation(l, r.Key.Kind, r.Key.Name, r.Aspect) == 0 && l.Signature.Equal(r.Signature)
	}
	mem.FreeSlice(nil, leftOrder)
	mem.FreeSlice(nil, rightOrder)
	return same
}

// SignatureRecord is the shared equality contract for computation inputs and
// artifacts. Hosts obtain observations; neither host chooses freshness policy.
type SignatureRecord struct {
	Implementation Signature
	// Guard proves that recorded derived values can be reused without evaluation.
	// It is a fast-path condition, not part of semantic result equality.
	Guard   Signature
	Inputs  []Observation
	Outputs []Observation
}

func (r *SignatureRecord) Matches(current *SignatureRecord) bool {
	return r.Implementation.Equal(current.Implementation) && sameObservations(r.Inputs, current.Inputs) && sameObservations(r.Outputs, current.Outputs)
}

func (r *SignatureRecord) Free(a mem.Allocator) {
	FreeObservations(a, r.Inputs)
	FreeObservations(a, r.Outputs)
	*r = SignatureRecord{}
}
