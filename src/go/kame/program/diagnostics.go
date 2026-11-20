package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/rule"
	"kame/lang/source"
	"strconv"
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
	if d.Cause.Kind == "exit" {
		return d
	}
	if len(d.TargetStack) == 0 {
		d.TargetStack = p.targetStack(entry)
	}
	if d.Source == "" && (d.Span.Start != 0 || d.Span.End != 0) {
		if name := p.declarationSourceName(entry.Rule.SourceOrigin); name != "" {
			d.Source = cloneText(p.Alloc, name)
		} else {
			p.locateDiagnostic(&d, d.Span)
		}
	}
	location := p.targetLocation(rule.Target{SourceOrigin: entry.Rule.SourceOrigin, Span: entry.Rule.Span})
	frame := diagnostic.Frame{Kind: "rule", Label: entry.Plan.Target, Source: location.Source, Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}}
	appendDiagnosticFrame(p.Alloc, &d, frame)
	p.appendDeclarationContext(&d, p.RuleDeclarations[entry.Rule])
	return d
}

func (p *Program) appendDeclarationContext(d *diagnostic.Diagnostic, scope declarationScope) {
	if generation := scope.Generation; generation != nil {
		for _, frame := range generation.Calls {
			appendDiagnosticFrame(p.Alloc, d, frame)
		}
		appendDiagnosticFrame(p.Alloc, d, generation.Site)
	}
	for iteration := scope.Iteration; iteration != nil; iteration = iteration.Parent {
		p.appendDeclarationIteration(d, iteration)
	}
}

func (p *Program) appendDeclarationIteration(d *diagnostic.Diagnostic, iteration *declarationIteration) {
	location := iteration.Location
	appendDiagnosticFrame(p.Alloc, d, diagnostic.Frame{Kind: "iteration", Label: iteration.Name + " #" + strconv.Itoa(iteration.Number), Source: location.Source, Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}})
}

func appendDiagnosticFrame(a mem.Allocator, d *diagnostic.Diagnostic, frame diagnostic.Frame) {
	for _, other := range d.Frames {
		if other.Kind == frame.Kind && other.Label == frame.Label && other.Source == frame.Source && other.Span == frame.Span {
			return
		}
	}
	frame.Kind, frame.Label, frame.Source = cloneText(a, frame.Kind), cloneText(a, frame.Label), cloneText(a, frame.Source)
	d.Frames = append(d.Frames, frame)
}

func (p *Program) failRule(c *core.EngineContext, index int, d diagnostic.Diagnostic) {
	p.releaseCacheLock(&p.Instances[index])
	c.Fail(p.ruleDiagnostic(d, &p.Instances[index]))
}

func (p *Program) locateDiagnostic(d *diagnostic.Diagnostic, span diagnostic.Span) {
	location := p.Eval.LocateSource(p.Parsed.Source.Name, source.Span{Start: span.Start, End: span.End})
	d.Source = cloneText(p.Alloc, location.Source)
	d.Span = diagnostic.Span{Start: location.Span.Start, End: location.Span.End}
}
