package core

import (
	"kame/diagnostic"
	"solod.dev/so/mem"
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
	DiagnosticExprValue       = "EXPR_INVALID"
	DiagnosticHostFailure     = "HOST_FAIL"
	DiagnosticCancelled       = "EXEC_CANCELLED"
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
	Wait bool
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
