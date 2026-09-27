package examples

import (
	"kame/core"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
)

// logState drives a context source that emits a batch, blocks on host work,
// then emits a nested batch from the host completion.
type logState struct {
	Alloc     mem.Allocator
	Phase     int
	Linked    core.Value
	HasLinked bool
}

func newLogState(a mem.Allocator) *logState {
	s := mem.Alloc[logState](a)
	s.Alloc = a
	return s
}

func freeLogState(source *core.Source) {
	s := source.State.(*logState)
	if s.HasLinked {
		s.Linked.Free(s.Alloc)
	}
	mem.Free(s.Alloc, s)
}

func logPoll(c *core.EngineContext, source *core.Source, out *core.Atom) core.PollResult {
	s := source.State.(*logState)
	switch s.Phase {
	case 0:
		s.Phase++
		*out = core.Atom{Kind: core.AtomChunk, Value: core.NewString(s.Alloc, "compiling a.km")}
	case 1:
		s.Phase++
		*out = core.Atom{Kind: core.AtomChunk, Value: core.NewString(s.Alloc, "compiling b.km")}
	case 2:
		s.Phase++
		*out = core.Atom{Kind: core.AtomEndBatch}
	case 3:
		completion := c.Completion()
		if !completion.HasValue {
			c.Submit(1)
			return core.PollWaiting
		}
		s.Linked, s.HasLinked = completion.Value, true
		s.Phase++
		*out = core.Atom{Kind: core.AtomStartCollection}
	case 4:
		s.Phase++
		*out = core.Atom{Kind: core.AtomChunk, Value: s.Linked}
		s.HasLinked = false
	case 5:
		s.Phase++
		*out = core.Atom{Kind: core.AtomEndCollection}
	case 6:
		s.Phase++
		*out = core.Atom{Kind: core.AtomEndBatch}
	default:
		return core.PollCompleted
	}
	return core.PollEmitted
}

// burstState drives a hot source that publishes more values than a subscriber
// queue can hold.
type burstState struct {
	Alloc mem.Allocator
	Next  int64
}

func newBurstState(a mem.Allocator) *burstState {
	s := mem.Alloc[burstState](a)
	s.Alloc = a
	return s
}

func freeBurstState(source *core.Source) {
	s := source.State.(*burstState)
	mem.Free(s.Alloc, s)
}

func burstPoll(c *core.EngineContext, source *core.Source, out *core.Atom) core.PollResult {
	_ = c
	s := source.State.(*burstState)
	if s.Next == 12 {
		return core.PollCompleted
	}
	s.Next++
	*out = core.Atom{Kind: core.AtomValue, Value: core.Value{Kind: core.Int, Int: s.Next}}
	return core.PollEmitted
}

// fixture is the producer context that attaches one source to a node.
type fixture struct {
	Alloc  mem.Allocator
	Source *core.Source
	Runs   int64
}

func attachProducer(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	f := c.Context().(*fixture)
	f.Runs++
	if f.Source == nil || !c.AttachSource(f.Source) {
		return core.ProducerFailed
	}
	f.Source = nil
	return core.ProducerActive
}

func freeFixture(a mem.Allocator, context any) {
	f := context.(*fixture)
	if f.Source != nil {
		core.FreeSource(a, f.Source)
	}
	mem.Free(a, f)
}

type Stream struct {
	Engine *core.Engine
	Logs   *core.Node
	Burst  *core.Node
}

func NewStream(a mem.Allocator) *Stream {
	e := core.NewEngine(a)
	s := mem.Alloc[Stream](a)
	s.Engine = e
	logs := mem.Alloc[fixture](a)
	logs.Alloc = a
	logs.Source = core.NewSource(a, logPoll, freeLogState, newLogState(a))
	s.Logs = e.AddOwned(core.ResourceKey{Kind: core.ResourceService, Name: "build-log"}, attachProducer, logs, freeFixture)
	burst := mem.Alloc[fixture](a)
	burst.Alloc = a
	burst.Source = core.NewSource(a, burstPoll, freeBurstState, newBurstState(a))
	s.Burst = e.AddOwned(core.ResourceKey{Kind: core.ResourceDefinition, Name: "burst"}, attachProducer, burst, freeFixture)
	return s
}

func (s *Stream) Free() {
	a := s.Engine.Alloc
	s.Engine.Free()
	mem.Free(a, s)
}

type StreamReport struct {
	Batches     bool
	Terminal    bool
	HostResumed bool
	Coalesced   bool
	Steps       int64
}

func (r StreamReport) OK() bool {
	return r.Batches && r.Terminal && r.HostResumed && r.Coalesced
}

func freeEvent(a mem.Allocator, event core.Event) {
	if event.HasValue() {
		event.Value.Free(a)
	}
}

func valueIsTwoStrings(event core.Event, first, second string) bool {
	if !event.HasValue() || event.Value.Kind != core.List || len(event.Value.List) != 2 {
		return false
	}
	return event.Value.List[0].Kind == core.String && event.Value.List[0].Text == first &&
		event.Value.List[1].Kind == core.String && event.Value.List[1].Text == second
}

func valueIsNestedList(event core.Event, text string) bool {
	if !event.HasValue() || event.Value.Kind != core.List || len(event.Value.List) != 1 {
		return false
	}
	inner := event.Value.List[0]
	return inner.Kind == core.List && len(inner.List) == 1 &&
		inner.List[0].Kind == core.String && inner.List[0].Text == text
}

// Run drives an asynchronous batch source and a hot source through one engine.
func (s *Stream) Run(out io.Writer) StreamReport {
	a := s.Engine.Alloc
	e := s.Engine
	r := StreamReport{}
	var steps int64

	fmt.Fprintf(out, "== engine: streaming source ==\n")

	sub := e.Subscribe(s.Logs)
	root := e.RequestRoot(s.Logs)
	for i := 0; i < 128; i++ {
		if s.Logs.State == core.NodeWaiting && s.Logs.Submitted {
			r.HostResumed = true
			e.Complete(core.Completion{
				NodeID:     s.Logs.ID,
				Generation: s.Logs.Generation,
				Attempt:    s.Logs.Attempt,
				RequestID:  s.Logs.HostRequestID,
				Value:      core.NewString(a, "linked"),
				HasValue:   true,
			})
		}
		node := e.Step()
		if node == nil {
			break
		}
		steps++
	}

	first := sub.Next()
	second := sub.Next()
	r.Batches = valueIsTwoStrings(first, "compiling a.km", "compiling b.km") && valueIsNestedList(second, "linked")
	freeEvent(a, first)
	freeEvent(a, second)
	terminal := sub.Next()
	r.Terminal = terminal.Kind == core.UpdateCompleted && terminal.Terminal()
	fmt.Fprintf(out, "two batches then completion: %t\n", r.Batches && r.Terminal)

	e.Unsubscribe(sub)
	e.Release(root)

	// Hot source: twelve values overflow the eight-slot queue; the oldest and
	// newest survive, and the terminal event is never dropped.
	burstSub := e.Subscribe(s.Burst)
	burstRoot := e.RequestRoot(s.Burst)
	for i := 0; i < 128; i++ {
		node := e.Step()
		if node == nil {
			break
		}
		steps++
	}
	oldest := burstSub.Next()
	last := oldest
	terminal = core.Event{}
	for {
		event := burstSub.Next()
		if !event.HasValue() {
			terminal = event
			break
		}
		freeEvent(a, last)
		last = event
	}
	r.Coalesced = oldest.HasValue() && oldest.Revision == 1 && last.Revision == 12 &&
		terminal.Kind == core.UpdateCompleted && terminal.Terminal()
	freeEvent(a, last)
	fmt.Fprintf(out, "burst coalesced to rev %d with terminal: %t\n", int(last.Revision), r.Coalesced)

	e.Unsubscribe(burstSub)
	e.Release(burstRoot)
	r.Steps = steps
	return r
}
