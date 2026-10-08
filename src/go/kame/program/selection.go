package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *Program) canonicalTarget(target string, file bool) string {
	if !file {
		return cloneText(p.Alloc, target)
	}
	if core.IsResourceURIName(target) {
		return cloneText(p.Alloc, target)
	}
	if path.IsAbs(target) {
		return path.Clean(p.Alloc, target)
	}
	return path.Join(p.Alloc, p.Options.Directory, target)
}

func (p *Program) selectRule(target string) selection {
	requested := target
	assignments := ""
	if split := firstSpace(target); split >= 0 {
		name := target[:split]
		for i := range p.Rules {
			candidate := p.Rules[i].Rule
			if candidate == nil || len(candidate.Arguments) == 0 || candidate.Kind == rule.FileRule {
				continue
			}
			for j := range candidate.Outputs {
				if !candidate.Outputs[j].Template && candidate.Outputs[j].Text == name {
					assignments = target[skipSpaces(target, split):]
					target = name
					break
				}
			}
			if target == name {
				break
			}
		}
	}
	selectedTarget := target
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
				selected := selection{Rule: r, Target: cloneText(p.Alloc, selectedTarget)}
				if len(r.Arguments) != 0 {
					binding := p.bindTargetArguments(r, assignments)
					selected.Arguments, selected.Diagnostic = binding.Values, binding.Diagnostic
				}
				return selected
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
			relativeOwned := false
			if file && path.IsAbs(matchTarget) && !path.IsAbs(output.Text) {
				matchTarget = p.relativePath(matchTarget)
				relativeOwned = true
			}
			match := output.TargetForm.MatchTarget(p.Alloc, matchTarget)
			if match == nil {
				if relativeOwned {
					mem.FreeString(p.Alloc, matchTarget)
				}
				continue
			}
			if match.Limited {
				match.Free(p.Alloc)
				if relativeOwned {
					mem.FreeString(p.Alloc, matchTarget)
				}
				if file {
					mem.FreeString(p.Alloc, target)
				}
				return selection{Diagnostic: diagnostic.Diagnostic{Source: cloneText(p.Alloc, p.Parsed.Source.Name), Code: cloneText(p.Alloc, "PAT_LIMIT"), Severity: diagnostic.Error, Message: cloneText(p.Alloc, "regular-expression match exceeded its step budget"), Span: diagnostic.Span{Start: output.Span.Start, End: output.Span.End}, Target: cloneText(p.Alloc, selectedTarget), Owned: true}}
			}
			if matched != nil {
				if matched != r || !sameCaptures(captures, match.Captures) {
					match.Free(p.Alloc)
					if relativeOwned {
						mem.FreeString(p.Alloc, matchTarget)
					}
					freeCaptures(p.Alloc, captures)
					if file {
						mem.FreeString(p.Alloc, target)
					}
					return selection{Ambiguous: true}
				}
				match.Free(p.Alloc)
				if relativeOwned {
					mem.FreeString(p.Alloc, matchTarget)
				}
				continue
			}
			matched, captures = r, cloneCaptures(p.Alloc, match.Captures)
			match.Free(p.Alloc)
			if relativeOwned {
				mem.FreeString(p.Alloc, matchTarget)
			}
		}
	}
	if file {
		mem.FreeString(p.Alloc, target)
	}
	return selection{Rule: matched, Captures: captures, Target: cloneText(p.Alloc, selectedTarget)}
}

func firstSpace(text string) int {
	for i := range text {
		if text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n' {
			return i
		}
	}
	return -1
}

func skipSpaces(text string, at int) int {
	for at < len(text) && (text[at] == ' ' || text[at] == '\t' || text[at] == '\r' || text[at] == '\n') {
		at++
	}
	return at
}

type argumentBindingResult struct {
	Values     []ArgumentValue
	Diagnostic diagnostic.Diagnostic
}

func (p *Program) bindTargetArguments(r *rule.Rule, input string) argumentBindingResult {
	provided := make([]bool, len(r.Arguments))
	values := make([]string, len(r.Arguments))
	for i := range r.Arguments {
		if r.Arguments[i].Optional {
			values[i] = r.Arguments[i].Default
		}
	}
	for at := 0; at < len(input); {
		at = skipSpaces(input, at)
		if at == len(input) {
			break
		}
		end := at
		for end < len(input) && input[end] != ' ' && input[end] != '\t' && input[end] != '\r' && input[end] != '\n' {
			end++
		}
		item := input[at:end]
		equal := strings.IndexByte(item, '=')
		if equal <= 0 || strings.IndexByte(item, 0) >= 0 {
			return argumentBindingResult{Diagnostic: failure(p.Alloc, "TGT_ARGUMENT", "malformed target argument assignment: "+item)}
		}
		index := -1
		for i := range r.Arguments {
			if r.Arguments[i].Name == item[:equal] {
				index = i
				break
			}
		}
		if index < 0 {
			return argumentBindingResult{Diagnostic: failure(p.Alloc, "TGT_ARGUMENT", "unknown target argument: "+item[:equal])}
		}
		if provided[index] {
			return argumentBindingResult{Diagnostic: failure(p.Alloc, "TGT_ARGUMENT", "duplicate target argument: "+item[:equal])}
		}
		provided[index], values[index] = true, item[equal+1:]
		at = end
	}
	var bound []ArgumentValue
	for i := range r.Arguments {
		if !provided[i] && !r.Arguments[i].Optional {
			freeArguments(p.Alloc, bound)
			return argumentBindingResult{Diagnostic: failure(p.Alloc, "TGT_ARGUMENT", "missing required target argument: "+r.Arguments[i].Name)}
		}
		bound = slices.Append(p.Alloc, bound, ArgumentValue{Name: cloneText(p.Alloc, r.Arguments[i].Name), Value: cloneText(p.Alloc, values[i])})
	}
	return argumentBindingResult{Values: bound}
}

func freeArguments(a mem.Allocator, arguments []ArgumentValue) {
	for i := range arguments {
		mem.FreeString(a, arguments[i].Name)
		mem.FreeString(a, arguments[i].Value)
	}
	slices.Free(a, arguments)
}

func targetArgumentKey(a mem.Allocator, target string, arguments []ArgumentValue) string {
	if len(arguments) == 0 {
		return cloneText(a, target)
	}
	b := strings.NewBuilder(a)
	defer b.Free()
	b.WriteString(target)
	for i := range arguments {
		b.WriteByte(0)
		b.WriteString(arguments[i].Name)
		b.WriteByte(0)
		b.WriteString(arguments[i].Value)
	}
	return cloneText(a, b.String())
}

// JoinTargetOperands combines CLI assignment operands with the preceding
// declared named task target. Returned strings are owned by the Program's
// allocator; unconsumed targets are copied unchanged.
func (p *Program) JoinTargetOperands(targets []string) []string {
	var operands []string
	for i := range targets {
		configured := false
		for j := range p.ParameterDefinitions {
			if targets[i] == p.ParameterDefinitions[j] { configured = true; break }
		}
		if !configured { operands = slices.Append(p.Alloc, operands, targets[i]) }
	}
	defer slices.Free(p.Alloc, operands)
	if (len(operands) == 0 || (isTargetAssignment(operands[0]) && !p.HasTarget(operands[0]))) && p.HasTarget("default") {
		var defaults []string
		defaults = slices.Append(p.Alloc, defaults, "default")
		for i := range operands { defaults = slices.Append(p.Alloc, defaults, operands[i]) }
		slices.Free(p.Alloc, operands)
		operands = defaults
	}
	targets = operands
	var joined []string
	for i := 0; i < len(targets); i++ {
		name := targets[i]
		if i+1 == len(targets) || !isTargetAssignment(targets[i+1]) || (!p.hasNamedArgumentTarget(name) && p.HasTarget(targets[i+1])) {
			joined = slices.Append(p.Alloc, joined, cloneText(p.Alloc, name))
			continue
		}
		b := strings.NewBuilder(p.Alloc)
		b.WriteString(name)
		for i+1 < len(targets) && isTargetAssignment(targets[i+1]) {
			i++
			b.WriteByte(' ')
			b.WriteString(targets[i])
		}
		joined = slices.Append(p.Alloc, joined, cloneText(p.Alloc, b.String()))
		b.Free()
	}
	return joined
}

func isTargetAssignment(text string) bool {
	equal := strings.IndexByte(text, '=')
	if equal <= 0 {
		return false
	}
	name := text[:equal]
	return !isPath(name) && !hasSlash(name)
}

func (p *Program) hasNamedArgumentTarget(name string) bool {
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if r == nil || len(r.Arguments) == 0 || r.Kind == rule.FileRule {
			continue
		}
		for j := range r.Outputs {
			if !r.Outputs[j].Template && r.Outputs[j].Text == name {
				return true
			}
		}
	}
	return false
}
