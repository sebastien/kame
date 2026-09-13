package core_test

import (
	"littlemake/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type sequence struct {
	Alloc mem.Allocator
	Atoms []core.Atom
	Index int
}

type releaseRecord struct {
	Freed bool
}

type failingSequence struct {
	Atoms   []core.Atom
	Index   int
	Record  *releaseRecord
	Alloc   mem.Allocator
}

func poll(s *core.Source, out *core.Atom) core.PollResult {
	state := s.State.(*sequence)
	if state.Index == len(state.Atoms) {
		return core.PollCompleted
	}
	*out = state.Atoms[state.Index]
	state.Index++
	return core.PollEmitted
}

func free(s *core.Source) {
	state := s.State.(*sequence)
	for i := state.Index; i < len(state.Atoms); i++ {
		state.Atoms[i].Value.Free(state.Alloc)
	}
	slices.Free(state.Alloc, state.Atoms)
	mem.Free(state.Alloc, state)
}

func newSequence(a mem.Allocator, atoms []core.Atom) *core.Source {
	state := mem.Alloc[sequence](a)
	state.Alloc = a
	state.Atoms = slices.Make[core.Atom](a, len(atoms))
	copy(state.Atoms, atoms)
	return core.NewSource(a, poll, free, state)
}

func pollFailingSequence(s *core.Source, out *core.Atom) core.PollResult {
	state := s.State.(*failingSequence)
	*out = state.Atoms[state.Index]
	state.Index++
	return core.PollEmitted
}

func freeFailingSequence(s *core.Source) {
	state := s.State.(*failingSequence)
	state.Record.Freed = true
	mem.Free(state.Alloc, state)
}

func newFailingSequence(a mem.Allocator, record *releaseRecord, atoms []core.Atom) *core.Source {
	state := mem.Alloc[failingSequence](a)
	state.Alloc = a
	state.Record = record
	state.Atoms = atoms
	return core.NewSource(a, pollFailingSequence, freeFailingSequence, state)
}

func TestMaterializesBatch(t *testing.T) {
	a := t.Allocator()
	first := core.NewString(a, "one")
	second := core.NewString(a, "two")
	source := newSequence(a, []core.Atom{
		{Kind: core.AtomChunk, Value: first},
		{Kind: core.AtomChunk, Value: second},
		{Kind: core.AtomEndBatch},
		{Kind: core.AtomEndStream},
	})
	m := core.NewMaterializer(a, source)

	m.Next()
	m.Next()
	batch := m.Next()
	if !batch.Published || batch.Value.Kind != core.List || len(batch.Value.List) != 2 {
		t.Error("batch was not published as a two-item list")
		m.Free()
		return
	}
	if batch.Value.List[0].Text != "one" || batch.Value.List[1].Text != "two" {
		t.Error("batch values were corrupted")
		batch.Value.Free(a)
		m.Free()
		return
	}
	batch.Value.Free(a)
	m.Next()
	m.Free()
}

func TestMaterializesEmptyBatch(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{{Kind: core.AtomEndBatch}}))
	batch := m.Next()
	if !batch.Published || batch.Value.Kind != core.List || len(batch.Value.List) != 0 {
		t.Error("empty batch was not published as an empty list")
	}
	batch.Value.Free(a)
	m.Free()
}

func TestMaterializesNestedCollections(t *testing.T) {
	a := t.Allocator()
	source := newSequence(a, []core.Atom{
		{Kind: core.AtomStartCollection},
		{Kind: core.AtomChunk, Value: core.NewString(a, "nested")},
		{Kind: core.AtomEndCollection},
		{Kind: core.AtomEndBatch},
	})
	m := core.NewMaterializer(a, source)
	m.Next()
	m.Next()
	m.Next()
	batch := m.Next()
	if !batch.Published || batch.Value.Kind != core.List || len(batch.Value.List) != 1 || batch.Value.List[0].Kind != core.List || len(batch.Value.List[0].List) != 1 || batch.Value.List[0].List[0].Text != "nested" {
		t.Error("nested collection was not materialized as a nested list")
	}
	batch.Value.Free(a)
	m.Free()
}

func TestNestedSourceUsesItsOwnBatch(t *testing.T) {
	a := t.Allocator()
	inner := newSequence(a, []core.Atom{
		{Kind: core.AtomValue, Value: core.NewString(a, "inner")},
	})
	outer := newSequence(a, []core.Atom{
		{Kind: core.AtomChunk, Value: core.NewString(a, "outer")},
		{Kind: core.AtomNested, Nested: inner},
		{Kind: core.AtomEndBatch},
		{Kind: core.AtomEndStream},
	})
	m := core.NewMaterializer(a, outer)
	m.Next()
	m.Next()
	innerValue := m.Next()
	m.Next()
	outerBatch := m.Next()
	if !innerValue.Published || innerValue.Value.Text != "inner" || !outerBatch.Published || outerBatch.Value.Kind != core.List || len(outerBatch.Value.List) != 1 || outerBatch.Value.List[0].Text != "outer" {
		t.Error("nested source did not retain independent batch state")
	}
	innerValue.Value.Free(a)
	outerBatch.Value.Free(a)
	m.Next()
	m.Free()
}

func TestFailureReleasesActiveSourceAndChunks(t *testing.T) {
	a := t.Allocator()
	record := mem.Alloc[releaseRecord](a)
	source := newFailingSequence(a, record, []core.Atom{
		{Kind: core.AtomChunk, Value: core.NewString(a, "discard")},
		{Kind: core.AtomFailed},
	})
	m := core.NewMaterializer(a, source)
	m.Next()
	failed := m.Next()
	if !failed.Terminal || !record.Freed {
		t.Error("failure did not release the active source and its chunks")
	}
	m.Free()
	mem.Free(a, record)
}

func TestProtocolViolationsReportExprValue(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{
		{Kind: core.AtomStartCollection},
		{Kind: core.AtomEndStream},
	}))
	m.Next()
	failed := m.Next()
	if !failed.Terminal || failed.Diagnostic.Code != core.DiagnosticExprValue {
		t.Error("unclosed collection did not report LM-EXPRV")
	}
	m.Free()
}

func TestInvalidateAndCancelReleaseSources(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	invalidated := mem.Alloc[releaseRecord](a)
	n := e.AddSource("source", newFailingSequence(a, invalidated, nil))
	e.Request(n)
	e.Invalidate(n)
	if !invalidated.Freed || n.Flow != nil || n.Current || n.Completed || n.Generation != 1 {
		t.Error("invalidation did not release and reset the source generation")
	}
	e.ReplaceSource(n, newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "replacement")}}))
	e.Request(n)
	e.Step()
	if !n.Current || n.Latest.Text != "replacement" {
		t.Error("replacement source did not start after invalidation")
	}
	cancelled := mem.Alloc[releaseRecord](a)
	pending := e.AddSource("waiting", newFailingSequence(a, cancelled, nil))
	s := e.Subscribe(pending)
	e.Request(pending)
	e.Cancel(pending)
	event := s.Next()
	if !cancelled.Freed || !event.Terminal || event.Diagnostic.Code != core.DiagnosticCancelled {
		t.Error("cancellation did not release the source and notify subscribers")
	}
	e.Free()
	mem.Free(a, invalidated)
	mem.Free(a, cancelled)
}

func TestEngineStartsRequestedSourcesInNameOrder(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	first := e.AddSource("a", newSequence(a, []core.Atom{
		{Kind: core.AtomValue, Value: core.NewString(a, "first")},
	}))
	second := e.AddSource("b", newSequence(a, []core.Atom{
		{Kind: core.AtomValue, Value: core.NewString(a, "second")},
	}))
	e.Request(second)
	e.Request(first)
	changed := e.Step()
	if changed != first || !first.Current || first.Revision != 1 {
		t.Error("engine did not run the first requested node deterministically")
		e.Free()
		return
	}
	e.Free()
}

func TestSubscribersOwnIndependentValueCopies(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := e.AddSource("stream", newSequence(a, []core.Atom{
		{Kind: core.AtomValue, Value: core.NewString(a, "value")},
	}))
	first := e.Subscribe(n)
	second := e.Subscribe(n)
	e.Request(n)
	e.Step()
	one := first.Next()
	two := second.Next()
	if !one.HasValue || !two.HasValue || one.Value.Text != "value" || two.Value.Text != "value" {
		t.Error("subscribers did not receive the published value")
		one.Value.Free(a)
		two.Value.Free(a)
		e.Free()
		return
	}
	one.Value.Free(a)
	two.Value.Free(a)
	e.Step()
	if !first.Next().Terminal || !second.Next().Terminal {
		t.Error("subscribers did not receive independent terminal events")
	}
	e.Free()
}
