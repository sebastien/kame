package program

import (
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/source"
)

// Keep declaration locations, including include-source mappings, in the shared
// selection diagnostic so builds and every inspection surface explain the same collision.
func (p *Program) producerAmbiguity(target string, first, second producerCandidate) diagnostic.Diagnostic {
	d := failure(p.Alloc, "TGT_AMBIG", "multiple producers for output: "+target)
	d.Target = cloneText(p.Alloc, target)
	secondLocation := p.targetLocation(second.Output)
	d.Source = cloneText(p.Alloc, secondLocation.Source)
	d.Span = diagnostic.Span{Start: secondLocation.Span.Start, End: secondLocation.Span.End}
	location := p.targetLocation(first.Output)
	d.Related = append(d.Related, diagnostic.Related{Message: cloneText(p.Alloc, "competing producer: "+first.Output.Text), Source: cloneText(p.Alloc, location.Source), Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}})
	d.Notes = append(d.Notes, cloneText(p.Alloc, "conflicting producer: "+second.Output.Text))
	d.Tips = append(d.Tips, cloneText(p.Alloc, "Use disjoint output patterns or one grouped producer; source-file existence never selects a producer."))
	p.appendDeclarationContext(&d, p.RuleDeclarations[first.Rule])
	p.appendDeclarationContext(&d, p.RuleDeclarations[second.Rule])
	return d
}

func (p *Program) targetLocation(target rule.Target) eval.Location {
	if name := p.declarationSourceName(target.SourceOrigin); name != "" {
		return eval.Location{Source: name, Span: target.Span}
	}
	return p.Eval.LocateSource(p.Parsed.Source.Name, target.Span)
}

func (p *Program) declarationSourceName(origin int) string {
	if origin > 0 && origin <= len(p.Eval.SourceParts) {
		part := &p.Eval.SourceParts[origin-1]
		if part.Module || part.Included != nil {
			return part.Name
		}
	}
	return ""
}

func (p *Program) declarationDiagnostic(span source.Span, origin int, code, message string) diagnostic.Diagnostic {
	d := p.outputDiagnostic(span, code, message)
	if name := p.declarationSourceName(origin); name != "" {
		mem.FreeString(p.Alloc, d.Source)
		d.Source = cloneText(p.Alloc, name)
	}
	return d
}

func (p *Program) registrationTip(d *diagnostic.Diagnostic) {
	d.Tips = append(d.Tips, cloneText(p.Alloc, "Declare a source-independent output pattern or a pure configured output list; discover file membership in prerequisites, not during registration."))
}
