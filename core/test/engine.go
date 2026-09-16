package core_test

import (
	"littlemake/core"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type producerState struct {
	Alloc mem.Allocator
	Text string
	Dependency core.ResourceKey
	Runs int
}

type restartSourceState struct {
	First  *core.Source
	Second *core.Source
	Runs   int
}

type callableSourceState struct {
	Alloc  mem.Allocator
	Record *releaseRecord
}

type repeatingSourceState struct {
	Alloc mem.Allocator
	Text  string
}

func pollRepeating(c *core.EngineContext, s *core.Source, out *core.Atom) core.PollResult {
	_ = c
	state := s.State.(*repeatingSourceState)
	out.Kind = core.AtomValue
	out.Value = core.NewString(state.Alloc, state.Text)
	return core.PollEmitted
}

func freeRepeating(s *core.Source) {
	state := s.State.(*repeatingSourceState)
	mem.Free(state.Alloc, state)
}

func newRepeatingSource(a mem.Allocator, text string) *core.Source {
	state := mem.Alloc[repeatingSourceState](a)
	state.Alloc, state.Text = a, text
	return core.NewSource(a, pollRepeating, freeRepeating, state)
}

func pollCallable(c *core.EngineContext, s *core.Source, out *core.Atom) core.PollResult {
	_ = c
	state := s.State.(*callableSourceState)
	state.Record.Polls++
	out.Kind = core.AtomValue
	out.Value = core.Value{Kind: core.Callable}
	return core.PollEmitted
}

func freeCallable(s *core.Source) {
	state := s.State.(*callableSourceState)
	mem.Free(state.Alloc, state)
}

func newCallableSource(a mem.Allocator, record *releaseRecord) *core.Source {
	state := mem.Alloc[callableSourceState](a)
	state.Alloc, state.Record = a, record
	return core.NewSource(a, pollCallable, freeCallable, state)
}

type deriveState struct {
	Alloc       mem.Allocator
	Dependency  core.ResourceKey
	Unknown     core.ResourceKey
	Runs        int
	Read        bool
	UnknownRead bool
}

func deriveFromDependency(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	s := c.Context().(*deriveState)
	s.Runs++
	value := c.Value(s.Dependency)
	if !value.OK { return core.ProducerFailed }
	s.Read = true
	if c.Value(s.Unknown).OK { s.UnknownRead = true }
	c.Publish(core.NewString(s.Alloc, value.Value.Text+"!"))
	return core.ProducerCompleted
}

func restartSource(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*restartSourceState)
	var source *core.Source
	if state.Runs == 0 { source = state.First; state.First = nil
	} else if state.Runs == 1 { source = state.Second; state.Second = nil
	}
	state.Runs++
	if source == nil || !c.AttachSource(source) { return core.ProducerFailed }
	return core.ProducerActive
}

func TestRecordAndResourceValuesClone(t *testing.T) {
	a := t.Allocator()
	resource := core.NewResource(a, core.ResourceTask, "build")
	value := core.NewRecord(a, []core.RecordField{{Key: "resource", Value: resource}})
	resource.Free(a)
	copy := value.Clone(a)
	value.Free(a)
	field := copy.Record[0]
	if field.Key != "resource" || field.Value.Resource.Kind != core.ResourceTask || field.Value.Resource.Name != "build" {
		t.Error("record clone did not retain its owned resource value")
	}
	copy.Free(a)
}

func TestLazyDuplicateRequestAndReadyCapacity(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc, state.Text = a, "once"
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "node"}, publishOnce, state)
	if e.Step() != nil || state.Runs != 0 { t.Error("lazy node started before request") }
	e.Request(n); e.Request(n)
	e.Step()
	if state.Runs != 1 { t.Error("duplicate request started producer twice") }
	first := addSource(e, "a", newSequence(a, nil))
	second := addSource(e, "b", newSequence(a, nil))
	e.Request(first); e.Request(second)
	ready := e.Ready(2)
	if len(ready) != 2 || ready[0] != first || ready[1] != second { t.Error("independent ready nodes were not offered in key order") }
	if e.Step() != nil { t.Error("Step dispatched host-claimed ready work") }
	slices.Free(a, ready)
	if len(e.Ready(2)) != 0 { t.Error("ready node was offered twice before dispatch") }
	e.Free()
	mem.Free(a, state)
}

func TestInvalidationKeepsLiveRootScheduled(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc, state.Text = a, "value"
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "node"}, publishOnce, state)
	r := e.RequestRoot(n)
	e.Step()
	e.Invalidate(n)
	e.Step()
	if state.Runs != 2 { t.Error("invalidation abandoned a live root") }
	e.Release(r)
	e.Free()
	mem.Free(a, state)
}

func TestInvalidationRestartsProducerGeneration(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[restartSourceState](a)
	state.First = newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "first")}})
	state.Second = newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "second")}})
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "node"}, restartSource, state)
	e.Request(n)
	e.Step()
	e.Invalidate(n)
	e.Step()
	if n.Generation != 1 || !n.Current || n.Latest.Text != "second" || state.Runs != 2 {
		t.Error("invalidation did not start a replacement source generation")
	}
	e.Free()
	mem.Free(a, state)
}

func TestEngineFreeReleasesLiveRoots(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "root", newSequence(a, nil))
	r := e.RequestRoot(n)
	_ = r
	e.Free()
}

func TestSubscriptionCoalescesMetadataUpdates(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "node", newSequence(a, nil))
	s := e.Subscribe(n)
	for i := 0; i < 10; i++ { e.Invalidate(n) }
	first := s.Next()
	last := first
	for {
		update := s.Next()
		if update.Kind == core.UpdateNone { break }
		last = update
	}
	if first.Kind != core.UpdateInvalidated || last.Kind != core.UpdateInvalidated || first.Order >= last.Order {
		t.Error("subscription did not retain ordered coalesced metadata updates")
	}
	e.Free()
}

func TestLateSubscriberGetsLatestAndCoalesces(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	atoms := slices.Make[core.Atom](a, 12)
	for i := 0; i < 11; i++ { atoms[i] = core.Atom{Kind: core.AtomValue, Value: core.NewString(a, "value")} }
	atoms[11] = core.Atom{Kind: core.AtomEndStream}
	n := addSource(e, "stream", newSequence(a, atoms))
	slices.Free(a, atoms)
	e.Request(n); e.Step(); e.Step()
	s := e.Subscribe(n)
	for i := 0; i < 9; i++ { e.Step() }
	first := s.Next()
	last := s.Next()
	for {
		event := s.Next()
		if !event.HasValue() { break }
		last.Value.Free(a)
		last = event
	}
	if !first.HasValue() || !last.HasValue() || first.Revision != 2 || last.Revision != 11 { t.Error("late subscriber did not retain latest and coalesce values") }
	first.Value.Free(a)
	last.Value.Free(a)
	e.Step()
	if !s.Next().Terminal() { t.Error("coalescing lost terminal event") }
	e.Free()
}

func TestEngineMaterializesBatch(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "batch", newSequence(a, []core.Atom{
		{Kind: core.AtomChunk, Value: core.NewString(a, "one")},
		{Kind: core.AtomChunk, Value: core.NewString(a, "two")},
		{Kind: core.AtomEndBatch},
	}))
	e.Request(n)
	for i := 0; i < 3; i++ { e.Step() }
	if !n.Current || n.Latest.Kind != core.List || len(n.Latest.List) != 2 || n.Latest.List[0].Text != "one" || n.Latest.List[1].Text != "two" {
		t.Error("engine stalled before materializing a batch")
	}
	e.Free()
}

func TestTerminalSourceReleasesMaterializer(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "source", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "value")}}))
	e.Request(n)
	e.Step()
	e.Step()
	if n.State != core.NodeComplete || n.HasActiveSource() { t.Error("terminal source retained its materializer") }
	e.Free()
}

func TestPublishedAtomCanWaitForDependencyWake(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "waiting", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "value"), Wait: true}}))
	e.Request(n)
	e.Step()
	if !n.Current || n.State != core.NodeWaiting { t.Error("published waiting atom left a reactive node ready") }
	e.Free()
}

func TestEngineResumesOuterSourceAfterNestedStream(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	inner := newSequence(a, []core.Atom{
		{Kind: core.AtomValue, Value: core.NewString(a, "inner")},
		{Kind: core.AtomEndStream},
	})
	n := addSource(e, "nested", newSequence(a, []core.Atom{
		{Kind: core.AtomChunk, Value: core.NewString(a, "outer")},
		{Kind: core.AtomNested, Nested: inner},
		{Kind: core.AtomEndBatch},
	}))
	e.Request(n)
	for i := 0; i < 5; i++ { e.Step() }
	if !n.Current || n.Latest.Kind != core.List || len(n.Latest.List) != 1 || n.Latest.List[0].Text != "outer" {
		t.Error("engine did not resume outer source after nested stream")
	}
	e.Free()
}

func TestReadyStreamDoesNotStarveOtherReadyNode(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	first := addSource(e, "a", newRepeatingSource(a, "first"))
	second := addSource(e, "b", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "second")}}))
	e.Request(first); e.Request(second)
	e.Step()
	e.Step()
	if !second.Current || second.Latest.Text != "second" { t.Error("ready stream starved another ready node") }
	e.Free()
}

func TestReadyStreamDoesNotStarveDependencyOrCompletion(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	stream := addSource(e, "a", newRepeatingSource(a, "stream"))
	dependency := addSource(e, "b", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "dependency")}}))
	dependent := addSource(e, "c", newSequence(a, nil))
	if !e.AddStatic(dependent, dependency) { t.Error("dependency rejected") }
	e.Request(stream); e.Request(dependent)
	e.Step()
	e.Step()
	if !dependency.Current { t.Error("ready stream starved a dependency") }

	waiting := addSource(e, "d", core.NewSource(a, contextPoll, freeContext, nil))
	e.Request(waiting)
	for i := 0; i < 3 && waiting.State != core.NodeWaiting; i++ { e.Step() }
	e.Complete(core.Completion{NodeID: waiting.ID, Generation: waiting.Generation, Attempt: waiting.Attempt, RequestID: 7, HasValue: true, Value: core.NewString(a, "done")})
	e.Step()
	if waiting.State != core.NodeReady { t.Error("ready stream starved a queued completion") }
	e.Free()
}

func TestCallableSourceValueFailsWithoutRepolling(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	record := mem.Alloc[releaseRecord](a)
	n := addSource(e, "callable", newCallableSource(a, record))
	s := e.Subscribe(n)
	e.Step()
	if n.State != core.NodeFailed || record.Polls != 1 { t.Error("callable source value did not fail once") }
	e.Step()
	event := s.Next()
	if record.Polls != 1 || !event.Terminal() || event.Diagnostic.Code != core.DiagnosticExprValue { t.Error("failed callable source was polled again") }
	e.Free()
	mem.Free(a, record)
}

func publishAfterFailure(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	c.Fail(core.Diagnostic{Code: core.DiagnosticHostFailure})
	c.Publish(core.NewString(c.Allocator(), "late"))
	return core.ProducerActive
}

func TestTerminalProducerCannotPublishAgain(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "node"}, publishAfterFailure, nil)
	e.Request(n)
	e.Step()
	if n.State != core.NodeFailed || n.Current { t.Error("terminal producer published a value") }
	e.Free()
}

func TestSubscribersOwnCoalescedBatchLists(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	atoms := slices.Make[core.Atom](a, 18)
	for i := 0; i < 9; i++ {
		atoms[i*2] = core.Atom{Kind: core.AtomChunk, Value: core.NewString(a, "value")}
		atoms[i*2+1] = core.Atom{Kind: core.AtomEndBatch}
	}
	n := addSource(e, "batches", newSequence(a, atoms))
	slices.Free(a, atoms)
	first := e.Subscribe(n)
	second := e.Subscribe(n)
	for i := 0; i < 18; i++ { e.Step() }
	one := first.Next()
	two := second.Next()
	if !one.HasValue() || !two.HasValue() || one.Value.Kind != core.List || two.Value.Kind != core.List || len(one.Value.List) != 1 || len(two.Value.List) != 1 {
		t.Error("subscribers did not receive batch lists")
	} else {
		original := one.Value.List[0].Text
		one.Value.List[0].Text = "changed"
		if two.Value.List[0].Text != "value" { t.Error("subscribers shared a materialized batch list") }
		one.Value.List[0].Text = original
	}
	one.Value.Free(a)
	two.Value.Free(a)
	last := first.Next()
	if !last.HasValue() || last.Revision != 9 || last.Value.Kind != core.List { t.Error("batch queue did not coalesce to latest value") }
	last.Value.Free(a)
	e.Free()
}

func TestLiveSubscribersReceiveEachPublication(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "stream", newSequence(a, []core.Atom{
		{Kind: core.AtomValue, Value: core.NewString(a, "one")},
		{Kind: core.AtomValue, Value: core.NewString(a, "two")},
		{Kind: core.AtomEndStream},
	}))
	first, second := e.Subscribe(n), e.Subscribe(n)
	e.Step()
	one, two := first.Next(), second.Next()
	if !one.HasValue() || !two.HasValue() || one.Value.Text != "one" || two.Value.Text != "one" { t.Error("subscribers missed the first live value") }
	one.Value.Free(a); two.Value.Free(a)
	e.Step()
	one, two = first.Next(), second.Next()
	if !one.HasValue() || !two.HasValue() || one.Value.Text != "two" || two.Value.Text != "two" { t.Error("subscribers missed a future live value") }
	one.Value.Free(a); two.Value.Free(a)
	e.Free()
}

func publishOnce(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	s := c.Context().(*producerState)
	s.Runs++
	if s.Dependency.Name != "" && !c.Dependency(s.Dependency) {
		return core.ProducerWaiting
	}
	c.Publish(core.NewString(s.Alloc, s.Text))
	return core.ProducerCompleted
}

func waitForCompletion(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	s := c.Context().(*producerState)
	s.Runs++
	completion := c.Completion()
	if completion.HasValue { c.Publish(completion.Value); return core.ProducerCompleted }
	c.Submit(1)
	return core.ProducerSubmitted
}

func contextPoll(c *core.EngineContext, s *core.Source, out *core.Atom) core.PollResult {
	_ = s
	completion := c.Completion()
	if !completion.HasValue { c.Submit(7); return core.PollWaiting }
	out.Kind, out.Value = core.AtomValue, completion.Value
	return core.PollEmitted
}

func failProducer(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = c
	_ = nodeID
	return core.ProducerFailed
}

func freeContext(s *core.Source) { _ = s }

func TestStaticDependenciesRunFirst(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	first := addSource(e, "a", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "a")}}))
	second := addSource(e, "b", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "b")}}))
	if !e.AddStatic(second, first) { t.Error("static dependency rejected") }
	e.Request(second)
	for i := 0; i < 2; i++ { e.Step() }
	if first.State != core.NodeComplete || e.Step() != second { t.Error("dependency did not complete before dependent") }
	e.Free()
}

func TestFailedDependencyTerminatesDependent(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	failed := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "failed"}, failProducer, nil)
	dependent := addSource(e, "dependent", newSequence(a, nil))
	e.AddStatic(dependent, failed)
	s := e.Subscribe(dependent)
	e.Request(dependent)
	e.Step()
	e.Step()
	event := s.Next()
	if !event.Terminal() || event.Kind != core.UpdateFailed { t.Error("failed dependency left dependent waiting") }
	e.Free()
}

func TestFailedDynamicDependencyTerminatesRequester(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "dependency"}, failProducer, nil)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	state.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "dependency"}
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "requester"}, publishOnce, state)
	e.Request(n)
	e.Step()
	e.Step()
	e.Step()
	if n.State != core.NodeFailed || n.Diagnostic.Code != core.DiagnosticHostFailure { t.Error("failed dynamic dependency left requester waiting") }
	e.Free()
	mem.Free(a, state)
}

func TestDynamicDependencyResumesProducer(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	addSource(e, "dep", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "dep")}}))
	state := mem.Alloc[producerState](a)
	state.Alloc, state.Text = a, "result"
	state.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "dep"}
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "result"}, publishOnce, state)
	e.Request(n)
	e.Step()
	if n.State != core.NodeWaiting || state.Runs != 1 { t.Error("producer did not wait for dynamic dependency") }
	for i := 0; i < 4 && !n.Current; i++ { e.Step() }
	if !n.Current || n.Latest.Text != "result" || state.Runs != 2 { t.Error("producer did not resume after dynamic dependency") }
	e.Free()
	mem.Free(a, state)
}

func TestDynamicDependencyEmitsUpdate(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	dep := addSource(e, "dep", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "dep")}}))
	state := mem.Alloc[producerState](a)
	state.Alloc, state.Text = a, "result"
	state.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "dep"}
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "result"}, publishOnce, state)
	s := e.Subscribe(n)
	e.Step()
	update := s.Next()
	if update.Kind != core.UpdateDependency || update.NodeID != n.ID || update.DependencyID != dep.ID {
		t.Error("subscriber did not observe the accepted dynamic dependency")
	}
	e.Free()
	mem.Free(a, state)
}

func TestDependencyWaitRejectsForgedZeroIDCompletion(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	state.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "dependency"}
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "requester"}, publishOnce, state)
	e.Request(n)
	e.Step()
	e.Complete(core.Completion{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt})
	e.Step()
	if n.State != core.NodeWaiting || n.HasCompletion { t.Error("dependency wait accepted forged zero-ID completion") }
	e.Free()
	mem.Free(a, state)
}

func TestDynamicDependencyRequestsStaticPrerequisites(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	prerequisite := addSource(e, "pre", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "pre")}}))
	dependency := addSource(e, "dep", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "dep")}}))
	e.AddStatic(dependency, prerequisite)
	state := mem.Alloc[producerState](a)
	state.Alloc, state.Text = a, "result"
	state.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "dep"}
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "result"}, publishOnce, state)
	e.Request(n)
	for i := 0; i < 8 && !n.Current; i++ { e.Step() }
	if prerequisite.State != core.NodeComplete || !dependency.Current || !n.Current || n.Latest.Text != "result" {
		t.Error("dynamic dependency did not request its static prerequisites")
	}
	e.Free()
	mem.Free(a, state)
}

func TestProducerReadsDependencyValue(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	dep := addSource(e, "dep", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "dep")}}))
	state := mem.Alloc[deriveState](a)
	state.Alloc = a
	state.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "dep"}
	state.Unknown = core.ResourceKey{Kind: core.ResourceDefinition, Name: "missing"}
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "result"}, deriveFromDependency, state)
	e.AddStatic(n, dep)
	e.Request(n)
	for i := 0; i < 4 && !n.Current; i++ { e.Step() }
	if !n.Current || n.Latest.Text != "dep!" || !state.Read || state.UnknownRead {
		t.Error("producer did not read its current dependency value")
	}
	e.Free()

	// A dependency with no current value reports no value rather than blocking.
	idle := core.NewEngine(a)
	addSource(idle, "dep", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "dep")}}))
	pending := mem.Alloc[deriveState](a)
	pending.Alloc = a
	pending.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "dep"}
	pending.Unknown = core.ResourceKey{Kind: core.ResourceDefinition, Name: "missing"}
	waiting := idle.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "result"}, deriveFromDependency, pending)
	idle.Request(waiting)
	idle.Step()
	if waiting.State != core.NodeFailed || pending.Read { t.Error("Value reported a dependency with no current value") }
	idle.Free()
	mem.Free(a, state)
	mem.Free(a, pending)
}

func TestDependencyCyclesFail(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	first := addSource(e, "a", newSequence(a, nil))
	second := addSource(e, "b", newSequence(a, nil))
	if !e.AddStatic(first, second) { t.Error("first edge rejected") }
	if e.AddStatic(second, first) || second.Diagnostic.Code != core.DiagnosticDependencyCycle { t.Error("cycle did not report DEP_CYCLE") }
	e.Free()
}

func TestDynamicDependencyCyclesFail(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	directState := mem.Alloc[producerState](a)
	directState.Alloc = a
	directState.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "direct"}
	direct := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "direct"}, publishOnce, directState)
	e.Request(direct)
	e.Step()
	if direct.Diagnostic.Code != core.DiagnosticDependencyCycle { t.Error("direct dynamic cycle did not report DEP_CYCLE") }
	firstState := mem.Alloc[producerState](a)
	secondState := mem.Alloc[producerState](a)
	firstState.Alloc, secondState.Alloc = a, a
	firstState.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "second"}
	secondState.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "first"}
	first := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "first"}, publishOnce, firstState)
	second := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "second"}, publishOnce, secondState)
	e.Request(first)
	e.Step()
	e.Step()
	if second.Diagnostic.Code != core.DiagnosticDependencyCycle { t.Error("indirect dynamic cycle did not report DEP_CYCLE") }
	e.Free()
	mem.Free(a, directState); mem.Free(a, firstState); mem.Free(a, secondState)
}

func TestInvalidationVisitsDiamondOnce(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	root := addSource(e, "root", newSequence(a, nil))
	left := addSource(e, "left", newSequence(a, nil))
	right := addSource(e, "right", newSequence(a, nil))
	leaf := addSource(e, "leaf", newSequence(a, nil))
	e.AddStatic(left, root); e.AddStatic(right, root); e.AddStatic(leaf, left); e.AddStatic(leaf, right)
	rootSub := e.Subscribe(root)
	leftSub := e.Subscribe(left)
	rightSub := e.Subscribe(right)
	leafSub := e.Subscribe(leaf)
	e.Invalidate(root)
	if root.Generation != 1 || left.Generation != 1 || right.Generation != 1 || leaf.Generation != 1 { t.Error("diamond invalidation visited a node more than once") }
	if rootSub.Next().Kind != core.UpdateInvalidated || leftSub.Next().Kind != core.UpdateInvalidated || rightSub.Next().Kind != core.UpdateInvalidated || leafSub.Next().Kind != core.UpdateInvalidated {
		t.Error("diamond invalidation did not notify every node")
	}
	if rootSub.Next().Kind != core.UpdateNone || leftSub.Next().Kind != core.UpdateNone || rightSub.Next().Kind != core.UpdateNone || leafSub.Next().Kind != core.UpdateNone {
		t.Error("diamond invalidation emitted duplicate updates")
	}
	e.Free()
}

func TestInvalidationVisitsDynamicFanout(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	root := addSource(e, "root", newSequence(a, []core.Atom{{Kind: core.AtomValue, Value: core.NewString(a, "root")}}))
	firstState := mem.Alloc[producerState](a)
	secondState := mem.Alloc[producerState](a)
	thirdState := mem.Alloc[producerState](a)
	firstState.Alloc, secondState.Alloc, thirdState.Alloc = a, a, a
	firstState.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "root"}
	secondState.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "root"}
	thirdState.Dependency = core.ResourceKey{Kind: core.ResourceDefinition, Name: "root"}
	first := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "a"}, publishOnce, firstState)
	second := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "b"}, publishOnce, secondState)
	third := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "c"}, publishOnce, thirdState)
	e.Request(first); e.Request(second); e.Request(third)
	for i := 0; i < 8; i++ { e.Step() }
	e.Invalidate(root)
	if root.Generation != 1 { t.Error("root was not invalidated") }
	if first.Generation != 1 { t.Error("first dynamic dependent was skipped") }
	if second.Generation != 1 { t.Error("second dynamic dependent was skipped") }
	if third.Generation != 1 { t.Error("third dynamic dependent was skipped") }
	e.Free()
	mem.Free(a, firstState); mem.Free(a, secondState); mem.Free(a, thirdState)
}

func TestStaleCompletionIsDiscarded(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "job"}, waitForCompletion, state)
	e.Request(n)
	e.Step()
	e.Invalidate(n)
	e.Request(n)
	e.Step()
	e.Complete(core.Completion{NodeID: n.ID, Generation: 0, Attempt: 1, RequestID: 1, Value: core.NewString(a, "stale"), HasValue: true})
	if n.State != core.NodeWaiting || n.Generation != 1 { t.Error("stale completion changed active invocation") }
	e.Complete(core.Completion{NodeID: n.ID, Generation: 1, Attempt: 2, RequestID: 1, Value: core.NewString(a, "fresh"), HasValue: true})
	e.Step()
	e.Step()
	e.Step()
	if !n.Current || n.Latest.Text != "fresh" { t.Error("matching completion did not resume producer") }
	e.Free()
	mem.Free(a, state)
}

func TestOlderAttemptCompletionIsDiscarded(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "job"}, waitForCompletion, state)
	e.Request(n)
	e.Step()
	e.Complete(core.Completion{NodeID: n.ID, Generation: 0, Attempt: 1, RequestID: 1})
	e.Step()
	e.Step()
	e.Complete(core.Completion{NodeID: n.ID, Generation: 0, Attempt: 1, RequestID: 1, Value: core.NewString(a, "old"), HasValue: true})
	e.Step()
	if n.State != core.NodeWaiting || n.Attempt != 2 { t.Error("older attempt completion changed active invocation") }
	e.Free()
	mem.Free(a, state)
}

func TestDispatchesIndependentJobsAndHandlesReverseCompletions(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	firstState := mem.Alloc[producerState](a)
	secondState := mem.Alloc[producerState](a)
	firstState.Alloc, secondState.Alloc = a, a
	first := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "a"}, waitForCompletion, firstState)
	second := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "b"}, waitForCompletion, secondState)
	e.Request(first); e.Request(second)
	jobs := e.Ready(2)
	if len(jobs) != 2 || e.Dispatch(jobs[0]) != first || e.Dispatch(jobs[1]) != second { t.Error("claimed ready jobs did not dispatch once") }
	slices.Free(a, jobs)
	e.Complete(core.Completion{NodeID: second.ID, Generation: second.Generation, Attempt: second.Attempt, RequestID: 1, Value: core.NewString(a, "second"), HasValue: true})
	e.Complete(core.Completion{NodeID: first.ID, Generation: first.Generation, Attempt: first.Attempt, RequestID: 1, Value: core.NewString(a, "first"), HasValue: true})
	for i := 0; i < 4; i++ { e.Step() }
	if !first.Current || !second.Current || first.Revision != 1 || second.Revision != 1 || first.Latest.Text != "first" || second.Latest.Text != "second" {
		t.Error("reverse completion order changed per-node results")
	}
	e.Free()
	mem.Free(a, firstState); mem.Free(a, secondState)
}

func TestWaitingSourceConsumesMatchingCompletion(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "source", core.NewSource(a, contextPoll, freeContext, nil))
	e.Request(n)
	e.Step()
	e.Complete(core.Completion{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: 7, Value: core.NewString(a, "done"), HasValue: true})
	e.Step()
	e.Step()
	if !n.Current || n.Latest.Text != "done" { t.Error("waiting source did not receive copied completion") }
	e.Free()
}

func TestWaitingSourceRejectsWrongRequestID(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	n := addSource(e, "source", core.NewSource(a, contextPoll, freeContext, nil))
	e.Request(n)
	e.Step()
	e.Complete(core.Completion{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: 8, Value: core.NewString(a, "wrong"), HasValue: true})
	e.Step()
	if n.State != core.NodeWaiting || n.Current { t.Error("source accepted completion for another host request") }
	e.Complete(core.Completion{NodeID: n.ID, Generation: n.Generation, Attempt: n.Attempt, RequestID: 7, Value: core.NewString(a, "right"), HasValue: true})
	e.Step()
	e.Step()
	if !n.Current || n.Latest.Text != "right" { t.Error("source did not accept matching host request completion") }
	e.Free()
}

func TestLastRootRequestsHostCancellation(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "job"}, waitForCompletion, state)
	r := e.RequestRoot(n)
	e.Step()
	e.Release(r)
	cancel := e.NextCancellation()
	if cancel.NodeID != n.ID || cancel.Attempt != 1 { t.Error("last root did not request host cancellation") }
	e.Free()
	mem.Free(a, state)
}

func TestInvalidationCancelsSubmittedGenerationAndRestarts(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	state := mem.Alloc[producerState](a)
	state.Alloc = a
	n := e.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "job"}, waitForCompletion, state)
	r := e.RequestRoot(n)
	e.Step()
	e.Invalidate(n)
	cancel := e.NextCancellation()
	if cancel.NodeID != n.ID || cancel.Generation != 0 || cancel.Attempt != 1 { t.Error("invalidation did not cancel the abandoned host invocation") }
	e.Step()
	if n.Generation != 1 || n.Attempt != 2 || n.State != core.NodeWaiting { t.Error("invalidation did not start a replacement invocation") }
	e.Complete(core.Completion{NodeID: n.ID, Generation: 0, Attempt: 1, RequestID: 1, Value: core.NewString(a, "stale"), HasValue: true})
	e.Step()
	if n.State != core.NodeWaiting || n.Attempt != 2 { t.Error("stale completion changed replacement invocation") }
	e.Release(r)
	e.Free()
	mem.Free(a, state)
}

func TestSharedInterestCancelsOnlyLastRoot(t *testing.T) {
	a := t.Allocator()
	e := core.NewEngine(a)
	shared := addSource(e, "shared", newSequence(a, nil))
	left := addSource(e, "left", newSequence(a, nil))
	right := addSource(e, "right", newSequence(a, nil))
	e.AddStatic(left, shared); e.AddStatic(right, shared)
	first, second := e.RequestRoot(left), e.RequestRoot(right)
	e.Release(first)
	if shared.State == core.NodeCancelled { t.Error("first root cancelled shared dependency") }
	e.Release(second)
	if shared.State != core.NodeCancelled { t.Error("last root did not cancel shared dependency") }
	e.Free()
}
