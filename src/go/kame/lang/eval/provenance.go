package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

// SourcePart maps one include-expanded segment back to its authored file.
type SourcePart struct {
	Name      string
	Start     int
	End       int
	Offset    int
	Directory string
	Includes  []diagnostic.Frame
}

type Location struct {
	Source string
	Span   source.Span
}

func (p *Program) AddSourcePart(name string, start int, end int, offset int) {
	p.SourceParts = slices.Append(p.Alloc, p.SourceParts, SourcePart{Name: owned(p.Alloc, name), Start: start, End: end, Offset: offset})
}

func (p *Program) SetSourcePartIncludes(includes []diagnostic.Frame) {
	if len(p.SourceParts) == 0 {
		return
	}
	part := &p.SourceParts[len(p.SourceParts)-1]
	for i := range includes {
		frame := includes[i]
		part.Includes = slices.Append(p.Alloc, part.Includes, diagnostic.Frame{Kind: owned(p.Alloc, frame.Kind), Label: owned(p.Alloc, frame.Label), Source: owned(p.Alloc, frame.Source), Span: frame.Span})
	}
}

// SetSourcePartDirectory attaches trusted loading metadata to the newest part.
// Keeping this separate preserves source-label-only embedding APIs.
func (p *Program) SetSourcePartDirectory(directory string) {
	if len(p.SourceParts) != 0 && path.IsAbs(directory) {
		part := &p.SourceParts[len(p.SourceParts)-1]
		mem.FreeString(p.Alloc, part.Directory)
		part.Directory = path.Clean(p.Alloc, directory)
	}
}

// SetScriptSourceOrigin records a compiler-assigned origin on authored ASTs.
// Cloning retains it; parsing generated text deliberately starts without it.
func SetScriptSourceOrigin(parsed *script.Script, start int, end int, origin int) {
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Span.Start < start || item.Span.End > end {
			continue
		}
		setExpressionOrigin(item.Expression, origin)
		if item.Definition != nil {
			setExpressionOrigin(item.Definition.Expression, origin)
		}
		if item.Rule != nil {
			setExpressionOrigin(item.Rule.Metadata, origin)
			for j := range item.Rule.Outputs {
				setTemplateSourceOrigin(item.Rule.Outputs[j].Expansion, origin)
			}
			for j := range item.Rule.Inputs {
				setTemplateSourceOrigin(item.Rule.Inputs[j].Template, origin)
			}
			if item.Rule.BodyDoc != nil {
				setExpressionOrigin(item.Rule.BodyDoc.Root, origin)
			}
			for j := range item.Rule.Body {
				setTemplateSourceOrigin(item.Rule.Body[j].Template, origin)
			}
		}
	}
}

func setTemplateSourceOrigin(value *template.String, origin int) {
	if value != nil {
		for i := range value.Parts {
			setExpressionOrigin(value.Parts[i].Expr, origin)
		}
	}
}

func setExpressionOrigin(e *expr.Expr, origin int) {
	if e == nil {
		return
	}
	e.SourceOrigin = origin
	for i := range e.Items {
		setExpressionOrigin(e.Items[i], origin)
	}
	for i := range e.Body {
		setExpressionOrigin(e.Body[i], origin)
	}
	for i := range e.Fields {
		setExpressionOrigin(e.Fields[i].Value, origin)
	}
	for i := range e.Parts {
		setExpressionOrigin(e.Parts[i].Expr, origin)
	}
	setExpressionOrigin(e.AuthoredPipe, origin)
}

func (c *Context) SourceDirectory() Result {
	if c.Program != nil && c.SourceOrigin > 0 && c.SourceOrigin <= len(c.Program.SourceParts) {
		part := c.Program.SourceParts[c.SourceOrigin-1]
		if part.Directory != "" {
			return Result{Value: core.NewString(c.Run, part.Directory)}
		}
	}
	return failure(c.Run, "SEL_NO_CONTEXT", c.Span, "source-dir requires file-backed source context")
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
		mem.FreeString(p.Alloc, p.SourceParts[i].Directory)
		for j := range p.SourceParts[i].Includes {
			frame := p.SourceParts[i].Includes[j]
			mem.FreeString(p.Alloc, frame.Kind)
			mem.FreeString(p.Alloc, frame.Label)
			mem.FreeString(p.Alloc, frame.Source)
		}
		slices.Free(p.Alloc, p.SourceParts[i].Includes)
	}
	slices.Free(p.Alloc, p.SourceParts)
}
