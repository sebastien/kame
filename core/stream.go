package core

import (
	"littlemake/diagnostic"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type AtomKind int

const (
	AtomNone AtomKind = iota
	AtomValue
	AtomChunk
	AtomEndBatch
	AtomStartCollection
	AtomEndCollection
	AtomEndStream
	AtomNested
	AtomFailed
)

type DiagnosticCode = string

const (
	DiagnosticExprValue = "EXPR_INVALID"
	DiagnosticHostFailure = "HOST_FAIL"
	DiagnosticCancelled  = "EXEC_CANCELLED"
	DiagnosticDependencyCycle = "DEP_CYCLE"
)

type Diagnostic = diagnostic.Diagnostic

type Atom struct {
	Kind       AtomKind
	Value      Value
	Nested     *Source
	Diagnostic Diagnostic
	// Wait keeps the node dependency-waiting after publishing this value.
	// It lets a reactive source publish one snapshot without a second poll
	// window in which a dependency update could be lost.
	Wait       bool
}

type PollResult int

const (
	PollEmitted PollResult = iota
	PollWaiting
	PollCompleted
	PollFailed
)

type SourcePoll func(*EngineContext, *Source, *Atom) PollResult
type SourceFree func(*Source)

// Source is an allocator-owned resumable generator. State is interpreted only
// by the callbacks that created it and must outlive every PollWaiting result.
type Source struct {
	Poll  SourcePoll
	Free  SourceFree
	State any
}

func NewSource(a mem.Allocator, poll SourcePoll, free SourceFree, state any) *Source {
	s := mem.Alloc[Source](a)
	s.Poll = poll
	s.Free = free
	s.State = state
	return s
}

func (s *Source) release(a mem.Allocator) {
	if s == nil {
		return
	}
	if s.Free != nil {
		s.Free(s)
	}
	mem.Free(a, s)
}

// FreeSource releases an untransferred source.
func FreeSource(a mem.Allocator, s *Source) { s.release(a) }

// Materializer owns active sources and incomplete batches. Next processes no
// more than one generator atom, so an unbounded source cannot starve the stepper.
type Materializer struct {
	Alloc    mem.Allocator
	frames   []sourceFrame
	failed   bool
	finished bool
	diagnostic Diagnostic
}

type sourceFrame struct {
	source      *Source
	chunks      []Value
	collections [][]Value
}

// AtomResult is the outcome of consuming at most one protocol atom. The
// materializer progresses one atom per call; only Waiting blocks the source.
type AtomResult struct {
	Value      Value
	Published  bool
	Terminal   bool
	Waiting    bool
	Diagnostic Diagnostic
}

func NewMaterializer(a mem.Allocator, source *Source) *Materializer {
	m := mem.Alloc[Materializer](a)
	m.Alloc = a
	m.frames = slices.Append(a, m.frames, sourceFrame{source: source})
	return m
}

// Next returns a materialized publication, whether one was published, and
// whether the source has reached a terminal state. The engine context is the
// active invocation's context; a caller that only drives the materializer
// directly passes nil.
func (m *Materializer) Next(c *EngineContext) AtomResult {
	if m.finished || m.failed {
		return AtomResult{Terminal: true, Diagnostic: m.diagnostic}
	}
	if len(m.frames) == 0 {
		m.finished = true
		return AtomResult{Terminal: true}
	}

	frame := &m.frames[len(m.frames)-1]
	s := frame.source
	atom := Atom{}
	result := s.Poll(c, s, &atom)
	if result == PollWaiting {
		return AtomResult{Waiting: true}
	}
	if result == PollFailed {
		return m.fail(Diagnostic{Code: DiagnosticHostFailure})
	}
	if result == PollCompleted { return m.completeFrame() }

	switch atom.Kind {
	case AtomValue:
		if len(frame.chunks) != 0 || len(frame.collections) != 0 {
			atom.Value.Free(m.Alloc)
			return m.fail(Diagnostic{Code: DiagnosticExprValue})
		}
		return AtomResult{Value: atom.Value, Published: true, Waiting: atom.Wait}
	case AtomChunk:
		if len(frame.collections) == 0 {
			frame.chunks = slices.Append(m.Alloc, frame.chunks, atom.Value)
		} else {
			last := len(frame.collections) - 1
			frame.collections[last] = slices.Append(m.Alloc, frame.collections[last], atom.Value)
		}
	case AtomEndBatch:
		if len(frame.collections) != 0 {
			return m.fail(Diagnostic{Code: DiagnosticExprValue})
		}
		return AtomResult{Value: commit(frame), Published: true}
	case AtomStartCollection:
		frame.collections = slices.Append(m.Alloc, frame.collections, nil)
	case AtomEndCollection:
		if len(frame.collections) == 0 {
			return m.fail(Diagnostic{Code: DiagnosticExprValue})
		}
		last := len(frame.collections) - 1
		list := Value{Kind: List, List: frame.collections[last]}
		frame.collections = frame.collections[:last]
		if len(frame.collections) == 0 {
			frame.chunks = slices.Append(m.Alloc, frame.chunks, list)
		} else {
			last = len(frame.collections) - 1
			frame.collections[last] = slices.Append(m.Alloc, frame.collections[last], list)
		}
	case AtomNested:
		if atom.Nested == nil {
			return m.fail(Diagnostic{Code: DiagnosticExprValue})
		}
		m.frames = slices.Append(m.Alloc, m.frames, sourceFrame{source: atom.Nested})
	case AtomEndStream:
		if len(frame.collections) != 0 {
			return m.fail(Diagnostic{Code: DiagnosticExprValue})
		}
		value := Value{}
		published := len(frame.chunks) != 0
		if published {
			value = commit(frame)
		}
		// The frame leaves the stack here; committed chunks moved into value,
		// so release the collection builder storage the frame still owns.
		slices.Free(m.Alloc, frame.collections)
		frame.collections = nil
		m.frames = m.frames[:len(m.frames)-1]
		s.release(m.Alloc)
		if len(m.frames) == 0 {
			m.finished = true
			if published {
				return AtomResult{Value: value, Published: true, Terminal: true}
			}
			return AtomResult{Terminal: true}
		}
		if published {
			return AtomResult{Value: value, Published: true}
		}
	case AtomFailed:
		diagnostic := atom.Diagnostic
		if diagnostic.Code == "" {
			diagnostic.Code = DiagnosticHostFailure
		}
		return m.fail(diagnostic)
	default:
		atom.Value.Free(m.Alloc)
		return m.fail(Diagnostic{Code: DiagnosticExprValue})
	}
	return AtomResult{}
}

func commit(frame *sourceFrame) Value {
	list := frame.chunks
	frame.chunks = nil
	return Value{Kind: List, List: list}
}

func (m *Materializer) fail(diagnostic Diagnostic) AtomResult {
	m.discard()
	m.failed = true
	m.diagnostic = diagnostic
	return AtomResult{Terminal: true, Diagnostic: diagnostic}
}

func (m *Materializer) completeFrame() AtomResult {
	last := len(m.frames) - 1
	if len(m.frames[last].collections) != 0 { return m.fail(Diagnostic{Code: DiagnosticExprValue}) }
	m.freeFrame(&m.frames[last])
	m.frames = m.frames[:last]
	if len(m.frames) == 0 {
		m.finished = true
		return AtomResult{Terminal: true}
	}
	return AtomResult{}
}

func (m *Materializer) freeFrame(frame *sourceFrame) {
	for i := range frame.chunks { frame.chunks[i].Free(m.Alloc) }
	slices.Free(m.Alloc, frame.chunks)
	for i := range frame.collections {
		for j := range frame.collections[i] { frame.collections[i][j].Free(m.Alloc) }
		slices.Free(m.Alloc, frame.collections[i])
	}
	slices.Free(m.Alloc, frame.collections)
	frame.source.release(m.Alloc)
}

func (m *Materializer) discard() {
	for i := range m.frames {
		m.freeFrame(&m.frames[i])
	}
	slices.Free(m.Alloc, m.frames)
	m.frames = nil
}

func (m *Materializer) Free() {
	if m == nil {
		return
	}
	m.discard()
	mem.Free(m.Alloc, m)
}
