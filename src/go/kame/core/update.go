package core

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type UpdateKind int

const (
	UpdateNone UpdateKind = iota
	UpdateValue
	UpdateDependency
	UpdateInvalidated
	UpdateCompleted
	UpdateFailed
	UpdateCancelled
)

// Event narrows an UpdateKind with its payload. A value event transfers Value
// ownership to the receiver. The zero event is the "no update" sentinel.
type Event struct {
	Kind         UpdateKind
	NodeID       int64
	DependencyID int64
	Order        int64
	Value        Value
	Revision     int64
	Diagnostic   Diagnostic
}

// HasValue reports whether the event carries an owned value.
func (e Event) HasValue() bool { return e.Kind == UpdateValue }

// Terminal reports whether the event ends the node generation.
func (e Event) Terminal() bool {
	return e.Kind == UpdateCompleted || e.Kind == UpdateFailed || e.Kind == UpdateCancelled
}

type Subscription struct {
	Alloc    mem.Allocator
	Node     *Node
	values   []Event
	updates  []Event
	terminal Event
	unsubbed bool
}

func (s *Subscription) pushValue(event Event) {
	if len(s.values) == 8 {
		for i := 1; i < len(s.values); i++ { s.values[i].Value.Free(s.Alloc) }
		s.values = s.values[:1]
	}
	s.values = slices.Append(s.Alloc, s.values, event)
}

// Next returns the next retained update in publication order, or the zero
// event when the queue is empty. A terminal event with a zero order was
// installed for a late subscriber and is delivered after all queued updates.
func (s *Subscription) Next() Event {
	if s.terminal.Kind != UpdateNone && s.terminal.Order != 0 &&
		(len(s.updates) == 0 || s.updates[0].Order >= s.terminal.Order) &&
		(len(s.values) == 0 || s.values[0].Order >= s.terminal.Order) {
		return takeEvent(&s.terminal)
	}
	if len(s.updates) != 0 && (len(s.values) == 0 || s.updates[0].Order < s.values[0].Order) {
		return s.popUpdate()
	}
	if len(s.values) != 0 { return s.popValue() }
	if s.terminal.Kind != UpdateNone { return takeEvent(&s.terminal) }
	return Event{}
}

func (s *Subscription) popUpdate() Event {
	e := s.updates[0]
	copy(s.updates, s.updates[1:])
	s.updates = s.updates[:len(s.updates)-1]
	return e
}

func (s *Subscription) popValue() Event {
	e := s.values[0]
	copy(s.values, s.values[1:])
	s.values = s.values[:len(s.values)-1]
	return e
}

// takeEvent moves the event out of the slot and resets the slot.
func takeEvent(event *Event) Event {
	e := *event
	*event = Event{}
	return e
}

func (s *Subscription) free() {
	for i := range s.values { s.values[i].Value.Free(s.Alloc) }
	slices.Free(s.Alloc, s.values); slices.Free(s.Alloc, s.updates)
	s.unsubbed = true; mem.Free(s.Alloc, s)
}

func terminal(n *Node) Event {
	kind := UpdateCompleted
	if n.State == NodeFailed { kind = UpdateFailed }
	if n.State == NodeCancelled { kind = UpdateCancelled }
	return Event{Kind: kind, Diagnostic: n.Diagnostic}
}
