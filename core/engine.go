package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Node is engine-owned. Latest is valid only while Current is true and is
// released by replacement, invalidation, or Engine.Free.
type Node struct {
	ID        int64
	Name      string
	Flow      *Materializer
	Latest    Value
	Current   bool
	Requested bool
	Completed bool
	Revision  int64
	Generation int64
	Diagnostic Diagnostic
	Subs      []*Subscription
}

// Event transfers Value ownership to the receiver when HasValue is true.
type Event struct {
	Value      Value
	HasValue   bool
	Terminal   bool
	Diagnostic Diagnostic
}

// Subscription is node-owned until Unsubscribe or Engine.Free. Its queue owns
// clones of node publications; terminal is out-of-band from value capacity.
type Subscription struct {
	Alloc     mem.Allocator
	Node      *Node
	values    []Value
	terminal  bool
	diagnostic Diagnostic
	unsubbed  bool
}

// Engine owns every registered node and its retained values. It deliberately
// uses a sorted scan: the engine proof is deterministic without a queue layer.
type Engine struct {
	Alloc  mem.Allocator
	nodes  []*Node
	nextID int64
}

func NewEngine(a mem.Allocator) *Engine {
	e := mem.Alloc[Engine](a)
	e.Alloc = a
	return e
}

// AddSource transfers source ownership to the engine on success.
func (e *Engine) AddSource(name string, source *Source) *Node {
	for i := range e.nodes {
		if e.nodes[i].Name == name {
			return nil
		}
	}
	n := mem.Alloc[Node](e.Alloc)
	e.nextID++
	n.ID = e.nextID
	n.Name = NewString(e.Alloc, name).Text
	n.Flow = NewMaterializer(e.Alloc, source)
	e.nodes = slices.Append(e.Alloc, e.nodes, n)
	return n
}

func (e *Engine) Request(n *Node) {
	_ = e
	if n != nil && !n.Completed {
		n.Requested = true
	}
}

func (e *Engine) Subscribe(n *Node) *Subscription {
	if n == nil {
		return nil
	}
	s := mem.Alloc[Subscription](e.Alloc)
	s.Alloc = e.Alloc
	s.Node = n
	n.Subs = slices.Append(e.Alloc, n.Subs, s)
	if n.Current {
		s.push(n.Latest.Clone(e.Alloc))
	}
	if n.Completed {
		s.terminal = true
		s.diagnostic = n.Diagnostic
	}
	return s
}

func (s *Subscription) push(value Value) {
	if len(s.values) == 8 {
		for i := 1; i < len(s.values); i++ {
			s.values[i].Free(s.Alloc)
		}
		s.values = s.values[:1]
	}
	s.values = slices.Append(s.Alloc, s.values, value)
}

func (s *Subscription) Next() Event {
	if len(s.values) != 0 {
		value := s.values[0]
		copy(s.values, s.values[1:])
		s.values = s.values[:len(s.values)-1]
		return Event{Value: value, HasValue: true}
	}
	if s.terminal {
		s.terminal = false
		return Event{Terminal: true, Diagnostic: s.diagnostic}
	}
	return Event{}
}

// Invalidate stops a source generation and clears its latest value. Call
// ReplaceSource before requesting the node again.
func (e *Engine) Invalidate(n *Node) {
	if n == nil {
		return
	}
	n.Generation++
	n.Requested = false
	n.Completed = false
	n.Diagnostic = Diagnostic{}
	if n.Flow != nil {
		n.Flow.Free()
		n.Flow = nil
	}
	if n.Current {
		n.Latest.Free(e.Alloc)
		n.Current = false
	}
}

// ReplaceSource installs the next source generation after invalidation.
func (e *Engine) ReplaceSource(n *Node, source *Source) {
	if n == nil || source == nil {
		return
	}
	if n.Flow != nil {
		n.Flow.Free()
	}
	n.Flow = NewMaterializer(e.Alloc, source)
	n.Completed = false
	n.Diagnostic = Diagnostic{}
}

// Cancel releases active source state and sends one cancellation terminal event.
func (e *Engine) Cancel(n *Node) {
	_ = e
	if n == nil || n.Completed {
		return
	}
	if n.Flow != nil {
		n.Flow.Free()
		n.Flow = nil
	}
	n.Generation++
	n.Requested = false
	n.Completed = true
	n.Diagnostic = Diagnostic{Code: DiagnosticCancelled}
	for i := range n.Subs {
		n.Subs[i].terminal = true
		n.Subs[i].diagnostic = n.Diagnostic
	}
}

func (e *Engine) Unsubscribe(s *Subscription) {
	_ = e
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
	s.free()
}

func (s *Subscription) free() {
	for i := range s.values {
		s.values[i].Free(s.Alloc)
	}
	slices.Free(s.Alloc, s.values)
	s.unsubbed = true
	mem.Free(s.Alloc, s)
}

// Step processes at most one protocol atom from the lexically first ready node.
// It returns the node that changed, or nil when no requested node can progress.
func (e *Engine) Step() *Node {
	var selected *Node
	for i := range e.nodes {
		n := e.nodes[i]
		if !n.Requested || n.Completed || n.Flow == nil {
			continue
		}
		if selected == nil || n.Name < selected.Name {
			selected = n
		}
	}
	if selected == nil {
		return nil
	}

	step := selected.Flow.Next()
	if step.Published {
		if selected.Current {
			selected.Latest.Free(e.Alloc)
		}
		selected.Latest = step.Value
		selected.Current = true
		selected.Revision++
		for i := range selected.Subs {
			selected.Subs[i].push(selected.Latest.Clone(e.Alloc))
		}
	}
	if step.Terminal {
		selected.Completed = true
		selected.Diagnostic = step.Diagnostic
		for i := range selected.Subs {
			selected.Subs[i].terminal = true
			selected.Subs[i].diagnostic = step.Diagnostic
		}
	}
	if step.Published || step.Terminal {
		return selected
	}
	return nil
}

func (e *Engine) Free() {
	if e == nil {
		return
	}
	for i := range e.nodes {
		n := e.nodes[i]
		for j := range n.Subs {
			n.Subs[j].free()
		}
		slices.Free(e.Alloc, n.Subs)
		if n.Current {
			n.Latest.Free(e.Alloc)
		}
		if n.Flow != nil {
			n.Flow.Free()
		}
		mem.FreeString(e.Alloc, n.Name)
		mem.Free(e.Alloc, n)
	}
	slices.Free(e.Alloc, e.nodes)
	mem.Free(e.Alloc, e)
}
