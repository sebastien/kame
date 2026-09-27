package core_test

import (
	"kame/core"
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
	Polls int
	Frees int
}

type failingSequence struct {
	Atoms   []core.Atom
	Index   int
	Record  *releaseRecord
	Alloc   mem.Allocator
}

type sourceFixture struct {
	Source *core.Source
}

func sourceFixtureProducer(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*sourceFixture)
	source := state.Source
	if source == nil || !c.AttachSource(source) { return core.ProducerFailed }
	state.Source = nil
	return core.ProducerActive
}

func freeSourceFixture(a mem.Allocator, context any) {
	state := context.(*sourceFixture)
	if state.Source != nil { core.FreeSource(a, state.Source) }
	mem.Free(a, state)
}

func addSource(e *core.Engine, name string, source *core.Source) *core.Node {
	state := mem.Alloc[sourceFixture](e.Alloc)
	state.Source = source
	return e.AddOwned(core.ResourceKey{Kind: core.ResourceDefinition, Name: name}, sourceFixtureProducer, state, freeSourceFixture)
}

func poll(c *core.EngineContext, s *core.Source, out *core.Atom) core.PollResult {
	_ = c
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

func pollFailingSequence(c *core.EngineContext, s *core.Source, out *core.Atom) core.PollResult {
	_ = c
	state := s.State.(*failingSequence)
	if state.Index == len(state.Atoms) { return core.PollWaiting }
	*out = state.Atoms[state.Index]
	state.Index++
	return core.PollEmitted
}

func freeFailingSequence(s *core.Source) {
	state := s.State.(*failingSequence)
	state.Record.Freed = true
	state.Record.Frees++
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

	m.Next(nil)
	m.Next(nil)
	batch := m.Next(nil)
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
	m.Next(nil)
	m.Free()
}

func TestMaterializesEmptyBatch(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{{Kind: core.AtomEndBatch}}))
	batch := m.Next(nil)
	if !batch.Published || batch.Value.Kind != core.List || len(batch.Value.List) != 0 {
		t.Error("empty batch was not published as an empty list")
	}
	batch.Value.Free(a)
	m.Free()
}

func TestEndStreamCommitsNonemptyBatch(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{
		{Kind: core.AtomChunk, Value: core.NewString(a, "final")},
		{Kind: core.AtomEndStream},
	}))
	m.Next(nil)
	step := m.Next(nil)
	if !step.Published || !step.Terminal || step.Value.Kind != core.List || len(step.Value.List) != 1 || step.Value.List[0].Text != "final" {
		t.Error("EndStream did not commit the pending batch")
	}
	step.Value.Free(a)
	m.Free()
}

func TestPollCompletedDiscardsUncommittedBatch(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{{Kind: core.AtomChunk, Value: core.NewString(a, "discard")}}))
	m.Next(nil)
	step := m.Next(nil)
	if step.Published || !step.Terminal { t.Error("PollCompleted committed an implicit batch") }
	m.Free()
}

func TestPollCompletedWithOpenCollectionFails(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{{Kind: core.AtomStartCollection}}))
	m.Next(nil)
	step := m.Next(nil)
	if !step.Terminal || step.Diagnostic.Code != core.DiagnosticExprValue { t.Error("PollCompleted accepted an open collection") }
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
	m.Next(nil)
	m.Next(nil)
	m.Next(nil)
	batch := m.Next(nil)
	if !batch.Published || batch.Value.Kind != core.List || len(batch.Value.List) != 1 || batch.Value.List[0].Kind != core.List || len(batch.Value.List[0].List) != 1 || batch.Value.List[0].List[0].Text != "nested" {
		t.Error("nested collection was not materialized as a nested list")
	}
	batch.Value.Free(a)
	m.Free()
}

func TestMaterializesEmptyNestedCollection(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{
		{Kind: core.AtomStartCollection},
		{Kind: core.AtomEndCollection},
		{Kind: core.AtomEndBatch},
	}))
	m.Next(nil); m.Next(nil)
	step := m.Next(nil)
	if !step.Published || step.Value.Kind != core.List || len(step.Value.List) != 1 || step.Value.List[0].Kind != core.List || len(step.Value.List[0].List) != 0 {
		t.Error("empty nested collection was not materialized")
	}
	step.Value.Free(a)
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
	m.Next(nil)
	m.Next(nil)
	innerValue := m.Next(nil)
	m.Next(nil)
	outerBatch := m.Next(nil)
	if !innerValue.Published || innerValue.Value.Text != "inner" || !outerBatch.Published || outerBatch.Value.Kind != core.List || len(outerBatch.Value.List) != 1 || outerBatch.Value.List[0].Text != "outer" {
		t.Error("nested source did not retain independent batch state")
	}
	innerValue.Value.Free(a)
	outerBatch.Value.Free(a)
	m.Next(nil)
	m.Free()
}

func TestEndStreamReleasesCollectionBuilder(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{
		{Kind: core.AtomStartCollection},
		{Kind: core.AtomChunk, Value: core.NewString(a, "nested")},
		{Kind: core.AtomEndCollection},
		{Kind: core.AtomEndBatch},
		{Kind: core.AtomEndStream},
	}))
	m.Next(nil)
	m.Next(nil)
	m.Next(nil)
	batch := m.Next(nil)
	if !batch.Published || batch.Value.Kind != core.List || len(batch.Value.List) != 1 {
		t.Error("collection batch was not committed before EndStream")
	}
	batch.Value.Free(a)
	m.Next(nil)
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
	m.Next(nil)
	failed := m.Next(nil)
	if !failed.Terminal || !record.Freed || record.Frees != 1 {
		t.Error("failure did not release the active source and its chunks")
	}
	m.Free()
	mem.Free(a, record)
}

func TestNormalCompletionReleasesSourceOnce(t *testing.T) {
	a := t.Allocator()
	record := mem.Alloc[releaseRecord](a)
	e := core.NewEngine(a)
	n := addSource(e, "normal", newFailingSequence(a, record, []core.Atom{{Kind: core.AtomEndStream}}))
	e.Request(n)
	e.Step()
	if n.State != core.NodeComplete || record.Frees != 1 { t.Error("normal completion did not release source exactly once") }
	e.Free()
	if record.Frees != 1 { t.Error("engine teardown released completed source twice") }
	mem.Free(a, record)
}

func TestProtocolViolationsReportExprValue(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{
		{Kind: core.AtomStartCollection},
		{Kind: core.AtomEndStream},
	}))
	m.Next(nil)
	failed := m.Next(nil)
	if !failed.Terminal || failed.Diagnostic.Code != core.DiagnosticExprValue {
		t.Error("unclosed collection did not report EXPR_INVALID")
	}
	m.Free()
}

func TestAtomInsideBatchReportsExprValue(t *testing.T) {
	a := t.Allocator()
	m := core.NewMaterializer(a, newSequence(a, []core.Atom{
		{Kind: core.AtomChunk, Value: core.NewString(a, "chunk")},
		{Kind: core.AtomValue, Value: core.NewString(a, "atom")},
	}))
	m.Next(nil)
	failed := m.Next(nil)
	if !failed.Terminal || failed.Diagnostic.Code != core.DiagnosticExprValue { t.Error("atom inside batch did not report EXPR_INVALID") }
	m.Free()
}

func TestInvalidateAndCancelReleaseSources(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	invalidated := mem.Alloc[releaseRecord](a)
	n := addSource(e, "source", newFailingSequence(a, invalidated, nil))
	e.Request(n)
	e.Step()
	e.Invalidate(n)
	if !invalidated.Freed || invalidated.Frees != 1 || n.HasActiveSource() || n.Current || n.Generation != 1 {
		t.Error("invalidation did not release and reset the source generation")
	}
	e.Cancel(n)
	cancelled := mem.Alloc[releaseRecord](a)
	pending := addSource(e, "waiting", newFailingSequence(a, cancelled, nil))
	s := e.Subscribe(pending)
	e.Request(pending)
	e.Step()
	e.Cancel(pending)
	if cancelled.Freed { t.Error("cancellation ignored active subscriber interest") }
	e.Unsubscribe(s)
	if !cancelled.Freed || cancelled.Frees != 1 { t.Error("last interest did not cancel and release source exactly once") }
	e.Free()
	if invalidated.Frees != 1 || cancelled.Frees != 1 { t.Error("engine teardown released cancelled sources twice") }
	mem.Free(a, invalidated)
	mem.Free(a, cancelled)
}

func TestEngineStartsRequestedSourcesInNameOrder(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	first := addSource(e, "a", newSequence(a, []core.Atom{
		{Kind: core.AtomValue, Value: core.NewString(a, "first")},
	}))
	second := addSource(e, "b", newSequence(a, []core.Atom{
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
	n := addSource(e, "stream", newSequence(a, []core.Atom{
		{Kind: core.AtomValue, Value: core.NewString(a, "value")},
	}))
	first := e.Subscribe(n)
	second := e.Subscribe(n)
	e.Request(n)
	e.Step()
	one := first.Next()
	two := second.Next()
	if !one.HasValue() || !two.HasValue() || one.Value.Text != "value" || two.Value.Text != "value" {
		t.Error("subscribers did not receive the published value")
		one.Value.Free(a)
		two.Value.Free(a)
		e.Free()
		return
	}
	one.Value.Free(a)
	two.Value.Free(a)
	e.Step()
	if !first.Next().Terminal() || !second.Next().Terminal() {
		t.Error("subscribers did not receive independent terminal events")
	}
	e.Free()
}
