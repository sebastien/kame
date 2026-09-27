package program

import (
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/path"
)

func (p *Program) canonicalTarget(target string, file bool) string {
	if !file {
		return cloneText(p.Alloc, target)
	}
	if path.IsAbs(target) {
		return path.Clean(p.Alloc, target)
	}
	return path.Join(p.Alloc, p.Options.Directory, target)
}

func (p *Program) selectRule(target string) selection {
	requested := target
	file := isPath(target) || hasSlash(target)
	if file {
		target = p.canonicalTarget(target, true)
	}
	var matched *rule.Rule
	var captures []template.CaptureValue
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if (r.Kind == rule.FileRule) != file {
			continue
		}
		for j := range r.Outputs {
			output := r.Outputs[j]
			if output.Template {
				continue
			}
			if !file && output.Text == target {
				return selection{Rule: r}
			}
			if file {
				literal := p.canonicalTarget(output.Text, true)
				matches := literal == target
				mem.FreeString(p.Alloc, literal)
				if matches {
					mem.FreeString(p.Alloc, target)
					return selection{Rule: r}
				}
			}
		}
	}
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if (r.Kind == rule.FileRule) != file {
			continue
		}
		for j := range r.Outputs {
			output := r.Outputs[j]
			if !output.Template {
				continue
			}
			matchTarget := requested
			if file && !isPath(matchTarget) {
				matchTarget = "./" + matchTarget
			}
			match := output.TargetForm.MatchTarget(p.Alloc, matchTarget)
			if match == nil {
				continue
			}
			if matched != nil {
				if matched != r || !sameCaptures(captures, match.Captures) {
					match.Free(p.Alloc)
					freeCaptures(p.Alloc, captures)
					if file {
						mem.FreeString(p.Alloc, target)
					}
					return selection{Ambiguous: true}
				}
				match.Free(p.Alloc)
				continue
			}
			matched, captures = r, cloneCaptures(p.Alloc, match.Captures)
			match.Free(p.Alloc)
		}
	}
	if file {
		mem.FreeString(p.Alloc, target)
	}
	return selection{Rule: matched, Captures: captures}
}
