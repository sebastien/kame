// Package diagnostic defines portable user-facing LittleMake failures.
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
	Label string
	Span  Span
}

type Diagnostic struct {
	Code     string
	Severity Severity
	Message  string
	Span     Span
	Target   string
	Notes    []string
	Frames   []Frame
	Owned    bool
}

func (d *Diagnostic) Clone(a mem.Allocator) Diagnostic {
	copy := *d
	copy.Code = clone(a, d.Code)
	copy.Message = clone(a, d.Message)
	copy.Target = clone(a, d.Target)
	copy.Notes = slices.Make[string](a, len(d.Notes))
	for i := range d.Notes { copy.Notes[i] = clone(a, d.Notes[i]) }
	copy.Frames = slices.Make[Frame](a, len(d.Frames))
	for i := range d.Frames {
		copy.Frames[i] = d.Frames[i]
		copy.Frames[i].Label = clone(a, d.Frames[i].Label)
	}
	copy.Owned = true
	return copy
}

func (d *Diagnostic) Free(a mem.Allocator) {
	if d.Owned {
		if d.Code != "" { mem.FreeString(a, d.Code) }
		if d.Message != "" { mem.FreeString(a, d.Message) }
		if d.Target != "" { mem.FreeString(a, d.Target) }
		for i := range d.Notes { if d.Notes[i] != "" { mem.FreeString(a, d.Notes[i]) } }
		for i := range d.Frames { if d.Frames[i].Label != "" { mem.FreeString(a, d.Frames[i].Label) } }
	}
	slices.Free(a, d.Frames)
	slices.Free(a, d.Notes)
	*d = Diagnostic{}
}

func clone(a mem.Allocator, text string) string {
	if len(text) == 0 { return "" }
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
