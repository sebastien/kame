// Package examples demonstrates the reactive engine end to end. Each scenario
// is an ordinary function so tests and the demo run the exact same code. These
// packages are hosted-only: they print through fmt and are deliberately outside
// the portable boundary described in 001-architecture.md.
package examples

import (
	"kame/core"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

type deriveKind int

const (
	deriveFiles deriveKind = iota
	deriveCount
	deriveBundle
	deriveDefault
)

var sourceNames = [4]string{"file1.km", "file2.km", "file3.km", "file4.km"}

func definition(name string) core.ResourceKey {
	return core.ResourceKey{Kind: core.ResourceDefinition, Name: name}
}

func inputFile(name string) core.ResourceKey {
	return core.ResourceKey{Kind: core.ResourceFile, Name: name}
}

// deriveState is the opaque producer context: a run counter and the transform
// each node applies to the current values of its dependencies.
type deriveState struct {
	Alloc mem.Allocator
	Kind  deriveKind
	Runs  int64
}

func newDerive(a mem.Allocator, kind deriveKind) *deriveState {
	s := mem.Alloc[deriveState](a)
	s.Alloc, s.Kind = a, kind
	return s
}

func freeDerive(a mem.Allocator, context any) {
	mem.Free(a, context.(*deriveState))
}

// stringList owns a fresh immutable list of strings.
func stringList(a mem.Allocator, texts []string) core.Value {
	items := slices.Make[core.Value](a, len(texts))
	for i := range texts {
		items[i] = core.NewString(a, texts[i])
	}
	value := core.NewList(a, items)
	for i := range items {
		items[i].Free(a)
	}
	slices.Free(a, items)
	return value
}

// sourceProducer is the leaf of the graph. Its second run simulates an edit.
func sourceProducer(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	s := c.Context().(*deriveState)
	s.Runs++
	count := 3
	if s.Runs > 1 {
		count = 4
	}
	c.Publish(stringList(s.Alloc, sourceNames[:count]))
	return core.ProducerCompleted
}

// deriveProducer reads the current value of a declared dependency through the
// engine context and publishes a value derived from it.
func deriveProducer(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	s := c.Context().(*deriveState)
	s.Runs++
	switch s.Kind {
	case deriveFiles:
		source := c.Value(inputFile("src"))
		if !source.OK {
			return core.ProducerWaiting
		}
		// Clone: the engine owns the dependency value, Publish consumes ours.
		c.Publish(source.Value.Clone(s.Alloc))
	case deriveCount:
		files := c.Value(definition("SOURCE_FILES"))
		if !files.OK {
			return core.ProducerWaiting
		}
		c.Publish(core.Value{Kind: core.Int, Int: int64(len(files.Value.List))})
	case deriveBundle:
		files := c.Value(definition("SOURCE_FILES"))
		if !files.OK {
			return core.ProducerWaiting
		}
		var scratch [20]byte
		text := "Bundle of " + strconv.Itoa(scratch[:], len(files.Value.List)) + " files"
		c.Publish(core.NewString(s.Alloc, text))
	case deriveDefault:
		bundle := c.Value(definition("bundle"))
		count := c.Value(definition("FILE_COUNT"))
		if !bundle.OK || !count.OK {
			return core.ProducerWaiting
		}
		c.Publish(core.NewRecord(s.Alloc, []core.RecordField{
			{Key: "bundle", Value: bundle.Value},
			{Key: "count", Value: count.Value},
		}))
	default:
		return core.ProducerFailed
	}
	return core.ProducerCompleted
}

// Build is the classic core-make graph: a file input flows through derived
// definitions into a target. It demonstrates backward demand propagation from
// the target and forward value propagation to the result.
type Build struct {
	Engine  *core.Engine
	Source  *core.Node
	Files   *core.Node
	Count   *core.Node
	Bundle  *core.Node
	Default *core.Node
}

func NewBuild(a mem.Allocator) *Build {
	e := core.NewEngine(a)
	b := mem.Alloc[Build](a)
	b.Engine = e
	b.Source = e.AddOwned(inputFile("src"), sourceProducer, newDerive(a, 0), freeDerive)
	b.Files = e.AddOwned(definition("SOURCE_FILES"), deriveProducer, newDerive(a, deriveFiles), freeDerive)
	b.Count = e.AddOwned(definition("FILE_COUNT"), deriveProducer, newDerive(a, deriveCount), freeDerive)
	b.Bundle = e.AddOwned(definition("bundle"), deriveProducer, newDerive(a, deriveBundle), freeDerive)
	b.Default = e.AddOwned(core.ResourceKey{Kind: core.ResourceTarget, Name: "default"}, deriveProducer, newDerive(a, deriveDefault), freeDerive)
	e.AddStatic(b.Files, b.Source)
	e.AddStatic(b.Count, b.Files)
	e.AddStatic(b.Bundle, b.Files)
	e.AddStatic(b.Default, b.Bundle)
	e.AddStatic(b.Default, b.Count)
	return b
}

func (b *Build) Free() {
	a := b.Engine.Alloc
	b.Engine.Free()
	mem.Free(a, b)
}

func recordField(record core.Value, key string) core.Value {
	if record.Kind != core.Record {
		return core.Value{}
	}
	for i := range record.Record {
		if record.Record[i].Key == key {
			return record.Record[i].Value
		}
	}
	return core.Value{}
}

func recordString(record core.Value, key string) string {
	field := recordField(record, key)
	if field.Kind != core.String {
		return ""
	}
	return field.Text
}

func recordCount(record core.Value, key string) int64 {
	field := recordField(record, key)
	if field.Kind != core.Int {
		return -1
	}
	return field.Int
}

// Report carries the invariants the scenario verified, so a caller can check
// the run without reaching into the graph.
type Report struct {
	Lazy       bool
	Backward   bool
	Forward    bool
	Result     bool
	Subscriber bool
	Rebuilt    bool
	Steps      int64
	SourceRuns int64
}

func (r Report) OK() bool {
	return r.Lazy && r.Backward && r.Forward && r.Result && r.Subscriber && r.Rebuilt
}

// Run requests the target, runs to quiescence, then invalidates the input to
// show a rebuild. The engine stays alive so the caller can still inspect nodes.
func (b *Build) Run(out io.Writer) Report {
	a := b.Engine.Alloc
	e := b.Engine
	r := Report{}
	source := b.Source.Context.(*deriveState)

	fmt.Fprintf(out, "== engine: core-make build graph ==\n")

	// Lazy: a node does not start before it is requested.
	r.Lazy = e.Step() == nil && source.Runs == 0 && !b.Default.Requested
	fmt.Fprintf(out, "lazy before request: %t\n", r.Lazy)

	// Backward propagation: requesting the target marks every ancestor root.
	root := e.RequestRoot(b.Default)
	r.Backward = b.Source.Requested && b.Files.Requested && b.Count.Requested && b.Bundle.Requested && b.Default.Requested
	fmt.Fprintf(out, "demand reached every root: %t\n", r.Backward)

	sub := e.Subscribe(b.Default)

	// Forward propagation: static dependencies complete before dependents.
	want := []*core.Node{b.Source, b.Files, b.Bundle, b.Count, b.Default}
	r.Forward = true
	index := 0
	var steps int64
	for {
		node := e.Step()
		if node == nil {
			break
		}
		if index < len(want) && node != want[index] {
			r.Forward = false
		}
		index++
		steps++
	}
	if index != len(want) {
		r.Forward = false
	}
	fmt.Fprintf(out, "forward order ran %d nodes: %t\n", index, r.Forward)

	// Result: the target aggregated its dependencies into a record.
	r.Result = recordString(b.Default.Latest, "bundle") == "Bundle of 3 files" && recordCount(b.Default.Latest, "count") == 3
	fmt.Fprintf(out, "target bundle: %s (%d files)\n", recordString(b.Default.Latest, "bundle"), int(recordCount(b.Default.Latest, "count")))

	// Subscriber: current value, then terminal.
	event := sub.Next()
	r.Subscriber = event.Kind == core.UpdateValue && event.Revision == 1 && event.HasValue()
	if event.HasValue() {
		event.Value.Free(a)
	}
	r.Subscriber = r.Subscriber && sub.Next().Kind == core.UpdateCompleted
	fmt.Fprintf(out, "subscriber saw value rev 1 then completed: %t\n", r.Subscriber)

	// Rebuild: invalidating the input recursively invalidates every dependent.
	before := source.Runs
	e.Invalidate(b.Source)
	index = 0
	var rebuilt int64
	for {
		node := e.Step()
		if node == nil {
			break
		}
		index++
		rebuilt++
	}
	invalidated := sub.Next()
	rebuiltValue := sub.Next()
	r.Rebuilt = index == len(want) &&
		source.Runs == before+1 &&
		b.Default.Generation == 1 &&
		b.Default.Revision == 2 &&
		recordCount(b.Default.Latest, "count") == 4 &&
		invalidated.Kind == core.UpdateInvalidated &&
		rebuiltValue.Kind == core.UpdateValue && rebuiltValue.Revision == 2
	if rebuiltValue.HasValue() {
		rebuiltValue.Value.Free(a)
	}
	r.Rebuilt = r.Rebuilt && sub.Next().Kind == core.UpdateCompleted
	fmt.Fprintf(out, "rebuild after input edit: %t (bundle now %s)\n", r.Rebuilt, recordString(b.Default.Latest, "bundle"))

	r.Steps = steps + rebuilt
	r.SourceRuns = source.Runs

	e.Unsubscribe(sub)
	e.Release(root)
	return r
}
