package program

import (
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type producerCandidate struct {
	Rule     *rule.Rule
	Output   rule.Target
	Captures []template.CaptureValue
}

func freeProducerCandidates(a mem.Allocator, candidates *[]producerCandidate) {
	for i := range *candidates {
		freeCaptures(a, (*candidates)[i].Captures)
	}
	slices.Free(a, *candidates)
}

type precedenceShape struct {
	Prefix  string
	Suffix  string
	Pattern string
	Valid   bool
}

func outputPrecedenceShape(output rule.Target) precedenceShape {
	shape := precedenceShape{}
	if output.TargetForm == nil { return shape }
	for i := range output.TargetForm.Parts {
		part := output.TargetForm.Parts[i]
		if part.Kind == template.Capture {
			if shape.Pattern != "" || part.Regex != nil || (part.Pattern != "*" && part.Pattern != "**") { return precedenceShape{} }
			shape.Pattern = part.Pattern
		} else if shape.Pattern == "" {
			shape.Prefix = part.Text
		} else {
			shape.Suffix = part.Text
		}
	}
	shape.Valid = shape.Pattern != ""
	return shape
}

// moreSpecific proves only the structural containment specified in spec 045.
// Unknown pattern relationships remain ambiguous rather than using a score.
func (p *Program) moreSpecific(narrow, broad rule.Target) bool {
	a, b := outputPrecedenceShape(narrow), outputPrecedenceShape(broad)
	if !a.Valid || !b.Valid { return false }
	// A sentinel preserves a trailing slash or partial path segment at the capture
	// boundary while normalizing relative and absolute prefix coordinates.
	ap := p.canonicalTarget(a.Prefix+"__capture__", true)
	bp := p.canonicalTarget(b.Prefix+"__capture__", true)
	defer mem.FreeString(p.Alloc, ap)
	defer mem.FreeString(p.Alloc, bp)
	aPrefix, bPrefix := ap[:len(ap)-len("__capture__")], bp[:len(bp)-len("__capture__")]
	if !strings.HasPrefix(aPrefix, bPrefix) || !strings.HasSuffix(a.Suffix, b.Suffix) { return false }
	if b.Pattern == "*" {
		if a.Pattern != "*" || strings.Contains(aPrefix[len(bPrefix):], "/") || strings.Contains(a.Suffix[:len(a.Suffix)-len(b.Suffix)], "/") { return false }
	}
	return aPrefix != bPrefix || a.Suffix != b.Suffix || a.Pattern != b.Pattern
}
