package program

import (
	"kame/diagnostic"
	"kame/lang/rule"
	"solod.dev/so/slices"
)

// Keep declaration locations, including include-source mappings, in the shared
// selection diagnostic so builds and every inspection surface explain the same collision.
func (p *Program) producerAmbiguity(target string, first rule.Target, second rule.Target) diagnostic.Diagnostic {
	d := failure(p.Alloc, "TGT_AMBIG", "multiple producers for output: "+target)
	d.Target = cloneText(p.Alloc, target)
	p.locateDiagnostic(&d, diagnostic.Span{Start: second.Span.Start, End: second.Span.End})
	location := p.Eval.LocateSource(p.Parsed.Source.Name, first.Span)
	d.Related = slices.Append(p.Alloc, d.Related, diagnostic.Related{Message: cloneText(p.Alloc, "competing producer: "+first.Text), Source: cloneText(p.Alloc, location.Source), Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}})
	d.Notes = slices.Append(p.Alloc, d.Notes, cloneText(p.Alloc, "conflicting producer: "+second.Text))
	d.Tips = slices.Append(p.Alloc, d.Tips, cloneText(p.Alloc, "Use disjoint output patterns or one grouped producer; source-file existence never selects a producer."))
	return d
}

func (p *Program) registrationTip(d *diagnostic.Diagnostic) {
	d.Tips = slices.Append(p.Alloc, d.Tips, cloneText(p.Alloc, "Declare a source-independent output pattern or a pure configured output list; discover file membership in prerequisites, not during registration."))
}
