package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/source"
	"solod.dev/so/slices"
)

func (p *Program) ruleDiagnostic(d diagnostic.Diagnostic, entry *instance) diagnostic.Diagnostic {
	if d.Code == "" || entry == nil {
		return d
	}
	if !d.Owned {
		owned := d.Clone(p.Alloc)
		d.Free(p.Alloc)
		d = owned
	}
	if d.Target == "" {
		d.Target = cloneText(p.Alloc, entry.Plan.Target)
	}
	if len(d.TargetStack) == 0 {
		d.TargetStack = p.targetStack(entry)
	}
	if d.Source == "" && (d.Span.Start != 0 || d.Span.End != 0) {
		p.locateDiagnostic(&d, d.Span)
	}
	location := p.Eval.LocateSource(p.Parsed.Source.Name, entry.Rule.Span)
	frame := diagnostic.Frame{Kind: "rule", Label: entry.Plan.Target, Source: location.Source, Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}}
	for i := range d.Frames {
		if d.Frames[i].Kind == frame.Kind && d.Frames[i].Label == frame.Label && d.Frames[i].Source == frame.Source && d.Frames[i].Span.Start == frame.Span.Start && d.Frames[i].Span.End == frame.Span.End {
			return d
		}
	}
	frame.Kind, frame.Label, frame.Source = cloneText(p.Alloc, frame.Kind), cloneText(p.Alloc, frame.Label), cloneText(p.Alloc, frame.Source)
	d.Frames = slices.Append(p.Alloc, d.Frames, frame)
	return d
}

func (p *Program) failRule(c *core.EngineContext, index int, d diagnostic.Diagnostic) {
	c.Fail(p.ruleDiagnostic(d, &p.Instances[index]))
}

func (p *Program) locateDiagnostic(d *diagnostic.Diagnostic, span diagnostic.Span) {
	location := p.Eval.LocateSource(p.Parsed.Source.Name, source.Span{Start: span.Start, End: span.End})
	d.Source = cloneText(p.Alloc, location.Source)
	d.Span = diagnostic.Span{Start: location.Span.Start, End: location.Span.End}
}
