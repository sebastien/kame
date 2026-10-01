package eval

import (
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// SourcePart maps one include-expanded segment back to its authored file.
type SourcePart struct {
	Name   string
	Start  int
	End    int
	Offset int
}

type Location struct {
	Source string
	Span   source.Span
}

func (p *Program) AddSourcePart(name string, start int, end int, offset int) {
	p.SourceParts = slices.Append(p.Alloc, p.SourceParts, SourcePart{Name: owned(p.Alloc, name), Start: start, End: end, Offset: offset})
}

// SetSourceOrigin hides a driver's synthetic wrapper around authored input.
func (p *Program) SetSourceOrigin(name string, start int, end int) {
	p.freeSourceParts()
	p.SourceParts = nil
	p.AddSourcePart(name, start, end, 0)
}

// LocateSource leaves external template sources unchanged. Returned names are
// borrowed from the program; retained diagnostics must copy them.
func (p *Program) LocateSource(name string, span source.Span) Location {
	location := Location{Source: name, Span: span}
	if p == nil || p.Script == nil || name != p.Script.Source.Name {
		return location
	}
	if len(p.SourceParts) != 0 && span.Start < p.SourceParts[0].Start {
		return Location{}
	}
	for i := len(p.SourceParts) - 1; i >= 0; i-- {
		part := p.SourceParts[i]
		if span.Start < part.Start {
			continue
		}
		start := span.Start
		if start > part.End {
			start = part.End
		}
		end := span.End
		if end > part.End {
			end = part.End
		}
		if end < start {
			end = start
		}
		location.Source = part.Name
		location.Span = source.Span{Start: start - part.Start + part.Offset, End: end - part.Start + part.Offset}
		return location
	}
	return location
}

func (p *Program) freeSourceParts() {
	for i := range p.SourceParts {
		mem.FreeString(p.Alloc, p.SourceParts[i].Name)
	}
	slices.Free(p.Alloc, p.SourceParts)
}
