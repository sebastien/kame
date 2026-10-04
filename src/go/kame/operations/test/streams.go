package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

// A barrier after each publication lets consumers observe it before the next
// update. It uses real correlated completions rather than timing or sleeps.
type libraryStreamRecord struct {
	Mode  int
	Frees int
}
type libraryStreamState struct {
	Alloc   mem.Allocator
	Record  *libraryStreamRecord
	Atoms   []core.Atom
	Index   int
	Barrier bool
}

func libraryStreamOperation(c *eval.Context, value any, args []core.Value) eval.Result {
	_ = args
	return eval.Result{Stream: newLibraryStream(c.Run, value.(*libraryStreamRecord), false)}
}

func newLibraryStream(a mem.Allocator, record *libraryStreamRecord, inner bool) *core.Source {
	state := mem.Alloc[libraryStreamState](a)
	state.Alloc, state.Record = a, record
	if record.Mode == 0 {
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomValue, Value: core.NewString(a, "one")})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomValue, Value: core.NewString(a, "two")})
	} else if record.Mode == 2 && !inner {
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomNested, Nested: newLibraryStream(a, record, true)})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomChunk, Value: core.Value{Kind: core.Int, Int: 9}})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomEndBatch})
	} else {
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomChunk, Value: core.Value{Kind: core.Int, Int: 1}})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomChunk, Value: core.Value{Kind: core.Int, Int: 2}})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomEndBatch})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomEndBatch})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomChunk, Value: core.Value{Kind: core.Int, Int: 3}})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomChunk, Value: core.Value{Kind: core.Int, Int: 4}})
		state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomEndBatch})
	}
	state.Atoms = slices.Append(a, state.Atoms, core.Atom{Kind: core.AtomEndStream})
	return core.NewSource(a, pollLibraryStream, freeLibraryStream, state)
}

func pollLibraryStream(c *core.EngineContext, source *core.Source, out *core.Atom) core.PollResult {
	state := source.State.(*libraryStreamState)
	if state.Barrier {
		if c.Completion().RequestID == 0 {
			c.Submit(700 + int64(state.Index))
			return core.PollWaiting
		}
		state.Barrier = false
	}
	*out = state.Atoms[state.Index]
	state.Index++
	state.Barrier = out.Kind == core.AtomValue || out.Kind == core.AtomEndBatch
	return core.PollEmitted
}

func freeLibraryStream(source *core.Source) {
	state := source.State.(*libraryStreamState)
	state.Record.Frees++
	for i := state.Index; i < len(state.Atoms); i++ {
		state.Atoms[i].Value.Free(state.Alloc)
		if state.Atoms[i].Nested != nil {
			core.FreeSource(state.Alloc, state.Atoms[i].Nested)
		}
	}
	slices.Free(state.Alloc, state.Atoms)
	mem.Free(state.Alloc, state)
}

func resumeLibraryStream(e *core.Engine, feed *core.Node) {
	e.Complete(core.Completion{NodeID: feed.ID, Generation: feed.Generation, Attempt: feed.Attempt, RequestID: feed.HostRequestID})
}

func checkLibraryPublications(t *testing.T, mode int, expression string, expected []string) {
	a := t.Allocator()
	record := mem.Alloc[libraryStreamRecord](a)
	record.Mode = mode
	defer mem.Free(a, record)
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	registry.Add(eval.Operation{Name: "fixture-stream", Call: libraryStreamOperation, Context: record, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "stream.km", "feed = (fixture-stream)\nresult = "+expression+"\n")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 {
		t.Fatal("parse streaming fixture")
		engine.Free()
		return
	}
	program := eval.Compile(a, engine, parsed, registry)
	defer program.Free()
	node, feed := program.Definition("result"), program.Definition("feed")
	engine.Request(node)
	var held core.Value
	for publication := range expected {
		revision := node.Revision
		for step := 0; step < 64 && (!feed.Submitted || node.Revision == revision); step++ {
			engine.Step()
		}
		if node.State == core.NodeFailed || !node.Current || node.Revision == revision || node.Latest.Kind != core.String || node.Latest.Text != expected[publication] {
			t.Error("wrong lifted publication: " + expected[publication])
			break
		}
		if publication == 0 {
			held = node.Latest.Clone(a)
		}
		if publication != 0 && (held.Kind != core.String || held.Text != expected[0]) {
			t.Error("later publication mutated an owned earlier result")
		}
		resumeLibraryStream(engine, feed)
	}
	held.Free(a)
	for step := 0; step < 32 && feed.HasActiveSource(); step++ {
		engine.Step()
	}
	expectedFrees := 1
	if mode == 2 {
		expectedFrees = 2
	}
	if record.Frees != expectedFrees {
		t.Error("stream sources were not released exactly once")
	}
	engine.Free()
	if record.Frees != expectedFrees {
		t.Error("teardown freed a completed source again")
	}
	if program.Requests.Next().OK {
		t.Error("pure lifted library calls submitted host work")
	}
}

func TestLibraryLiftsScalarAtomPublications(t *testing.T) {
	checkLibraryPublications(t, 0, "(uppercase feed)", []string{"ONE", "TWO"})
}

func TestLibraryLiftsMapFilterFlatmapOverCompleteBatches(t *testing.T) {
	checkLibraryPublications(t, 1, "(join (map ([x] (str x)) (flatmap ([x] (list x x)) (filter ([x] (gt x 1)) feed))) \",\")", []string{"2,2", "", "3,3,4,4"})
}

func TestLibraryLiftsReduceAndResumesOuterNestedSource(t *testing.T) {
	checkLibraryPublications(t, 2, "(reduce ([a b] (cat a (str b))) feed \"\")", []string{"12", "", "34", "9"})
}

func TestStreamUpdateCancelsPendingLibraryCallback(t *testing.T) {
	a := t.Allocator()
	record := mem.Alloc[libraryStreamRecord](a)
	record.Mode = 1
	defer mem.Free(a, record)
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	registry.Add(eval.Operation{Name: "fixture-stream", Call: libraryStreamOperation, Context: record, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "wait-each", Call: waitEach, MinArity: 1, MaxArity: 1})
	parsed := script.Parse(a, "stream.km", "feed = (fixture-stream)\nresult = (map ([x] (wait-each x)) feed)\n")
	defer parsed.Free()
	program := eval.Compile(a, engine, parsed, registry)
	defer program.Free()
	defer engine.Free()
	node, feed := program.Definition("result"), program.Definition("feed")
	engine.Request(node)
	for step := 0; step < 32 && (!node.Submitted || !feed.Submitted); step++ {
		engine.Step()
	}
	first := program.Requests.Next()
	if !first.OK {
		t.Fatal("stream callback did not wait")
		return
	}
	// Accumulate the first callback result, then invalidate while the second
	// is outstanding. The replacement must discard that partial collection.
	engine.Complete(core.Completion{NodeID: first.Request.NodeID, Generation: first.Request.Generation, Attempt: first.Request.Attempt, RequestID: first.Request.ID})
	first.Request.Free(a)
	engine.Step()
	engine.Step()
	first = program.Requests.Next()
	if !first.OK {
		t.Fatal("second callback did not wait")
		return
	}
	firstID := first.Request.ID
	resumeLibraryStream(engine, feed)
	for step := 0; step < 32 && (!node.Current || !feed.Submitted); step++ {
		engine.Step()
	}
	cancellation := engine.NextCancellation()
	if cancellation.RequestID != first.Request.ID || !node.Current || node.Latest.Kind != core.List || len(node.Latest.List) != 0 {
		t.Error("empty replacement batch did not cancel the old callback")
	}
	engine.Complete(core.Completion{NodeID: first.Request.NodeID, Generation: first.Request.Generation, Attempt: first.Request.Attempt, RequestID: first.Request.ID})
	first.Request.Free(a)
	for step := 0; step < 8; step++ {
		engine.Step()
	}
	if !node.Current || node.Latest.Kind != core.List || len(node.Latest.List) != 0 {
		t.Error("stale callback completion changed the replacement batch")
	}
	resumeLibraryStream(engine, feed)
	for callback := 0; callback < 2; callback++ {
		for step := 0; step < 32 && !node.Submitted; step++ {
			engine.Step()
		}
		next := program.Requests.Next()
		if !next.OK {
			t.Fatal("replacement callback did not submit")
			return
		}
		if next.Request.ID == firstID {
			t.Error("replacement callback reused stale request identity")
		}
		engine.Complete(core.Completion{NodeID: next.Request.NodeID, Generation: next.Request.Generation, Attempt: next.Request.Attempt, RequestID: next.Request.ID})
		next.Request.Free(a)
		engine.Step()
		engine.Step()
	}
	for step := 0; step < 16; step++ {
		engine.Step()
	}
	if !node.Current || node.Latest.Kind != core.List || len(node.Latest.List) != 2 || node.Latest.List[0].Int != 3 || node.Latest.List[1].Int != 4 {
		t.Error("replacement callbacks lost order or reused discarded progress")
	}
}

func TestLibraryCoalescesTwoStreamInputsBeforeInvocation(t *testing.T) {
	a := t.Allocator()
	streamRecord := mem.Alloc[libraryStreamRecord](a)
	defer mem.Free(a, streamRecord)
	output := mem.Alloc[outputState](a)
	defer mem.Free(a, output)
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	registry.Add(eval.Operation{Name: "fixture-stream", Call: libraryStreamOperation, Context: streamRecord, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "coalesced.km", "left = (fixture-stream)\nright = (fixture-stream)\nresult = (out (cat (uppercase left) (uppercase right)))\n")
	defer parsed.Free()
	program := eval.Compile(a, engine, parsed, registry)
	defer program.Free()
	defer engine.Free()
	program.SetDefinitionEffectSink(captureOutput, output)
	node, left, right := program.Definition("result"), program.Definition("left"), program.Definition("right")
	engine.Request(node)
	for step := 0; step < 64 && (!node.Current || !left.Submitted || !right.Submitted); step++ {
		engine.Step()
	}
	if !node.Current || node.Latest.Text != "ONEONE" || output.Count != 1 {
		t.Error("initial invocation did not wait for both current arguments")
	}
	resumeLibraryStream(engine, left)
	resumeLibraryStream(engine, right)
	// Deliver both ready input publications before scheduling the consumer.
	// Coalescing applies to changes observed before its next invocation.
	engine.Step()
	engine.Step()
	engine.DispatchRoot(left)
	engine.DispatchRoot(right)
	for step := 0; step < 64 && (!left.Submitted || !right.Submitted || node.Latest.Text != "TWOTWO"); step++ {
		engine.Step()
	}
	if !node.Current || node.Latest.Text != "TWOTWO" || output.Count != 2 {
		t.Error("coalesced inputs repeated an invocation or mixed old and new values: " + node.Latest.Text)
		t.Errorf("effect count: %d", output.Count)
	}
}
