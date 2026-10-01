// Package diagnostic defines portable user-facing Kame failures.
package diagnostic

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type Severity int

const (
	Warning Severity = iota
	Error
	Fatal
)

// Span is source-neutral so portable core code need not import language ASTs.
type Span struct {
	Start int
	End   int
}

type Frame struct {
	Kind   string
	Label  string
	Source string
	Span   Span
}

// Related is a separately located fact which helps explain a diagnostic.
// Unlike a frame, it is not propagation context.
type Related struct {
	Message string
	Source  string
	Span    Span
}

// Cause records host or process metadata, never captured output. Process output
// can contain arbitrary secrets; it belongs only to the explicit live streams
// and collected operation values. Presence flags distinguish omitted values.
type Cause struct {
	Kind              string
	Message           string
	Program           string
	Status            int
	HasStatus         bool
	Signal            int
	HasSignal         bool
	StdoutTruncated   bool
	StderrTruncated   bool
	StdoutLimit       int
	StderrLimit       int
	OutputWasStreamed bool
}

type Diagnostic struct {
	Source      string
	Code        string
	Severity    Severity
	Message     string
	Span        Span
	Target      string
	Notes       []string
	Related     []Related
	Frames      []Frame
	TargetStack []string
	Tips        []string
	Cause       Cause
	Owned       bool
}

func (d *Diagnostic) Clone(a mem.Allocator) Diagnostic {
	copy := *d
	copy.Source = clone(a, d.Source)
	copy.Code = clone(a, d.Code)
	copy.Message = clone(a, d.Message)
	copy.Target = clone(a, d.Target)
	copy.Notes = slices.Make[string](a, len(d.Notes))
	for i := range d.Notes {
		copy.Notes[i] = clone(a, d.Notes[i])
	}
	copy.Related = slices.Make[Related](a, len(d.Related))
	for i := range d.Related {
		copy.Related[i] = d.Related[i]
		copy.Related[i].Message = clone(a, d.Related[i].Message)
		copy.Related[i].Source = clone(a, d.Related[i].Source)
	}
	copy.Frames = slices.Make[Frame](a, len(d.Frames))
	for i := range d.Frames {
		copy.Frames[i] = d.Frames[i]
		copy.Frames[i].Kind = clone(a, d.Frames[i].Kind)
		copy.Frames[i].Label = clone(a, d.Frames[i].Label)
		copy.Frames[i].Source = clone(a, d.Frames[i].Source)
	}
	copy.TargetStack = slices.Make[string](a, len(d.TargetStack))
	for i := range d.TargetStack {
		copy.TargetStack[i] = clone(a, d.TargetStack[i])
	}
	copy.Tips = slices.Make[string](a, len(d.Tips))
	for i := range d.Tips {
		copy.Tips[i] = clone(a, d.Tips[i])
	}
	copy.Cause = d.Cause
	copy.Cause.Kind = clone(a, d.Cause.Kind)
	copy.Cause.Message = clone(a, d.Cause.Message)
	copy.Cause.Program = clone(a, d.Cause.Program)
	copy.Owned = true
	return copy
}

func (d *Diagnostic) Free(a mem.Allocator) {
	if d.Owned {
		mem.FreeString(a, d.Code)
		mem.FreeString(a, d.Source)
		mem.FreeString(a, d.Message)
		mem.FreeString(a, d.Target)
		for i := range d.Notes {
			mem.FreeString(a, d.Notes[i])
		}
		for i := range d.Related {
			mem.FreeString(a, d.Related[i].Message)
			mem.FreeString(a, d.Related[i].Source)
		}
		for i := range d.Frames {
			mem.FreeString(a, d.Frames[i].Kind)
			mem.FreeString(a, d.Frames[i].Label)
			mem.FreeString(a, d.Frames[i].Source)
		}
		for i := range d.TargetStack {
			mem.FreeString(a, d.TargetStack[i])
		}
		for i := range d.Tips {
			mem.FreeString(a, d.Tips[i])
		}
		mem.FreeString(a, d.Cause.Kind)
		mem.FreeString(a, d.Cause.Message)
		mem.FreeString(a, d.Cause.Program)
	}
	slices.Free(a, d.Frames)
	slices.Free(a, d.Notes)
	slices.Free(a, d.Related)
	slices.Free(a, d.TargetStack)
	slices.Free(a, d.Tips)
	*d = Diagnostic{}
}

func clone(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
