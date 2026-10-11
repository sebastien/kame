package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *Program) beginOwnership(entry *instance, commands string, effects []eval.Effect, writes []string) diagnostic.Diagnostic {
	if p.Options.Ownership == nil {
		return diagnostic.Diagnostic{}
	}
	if entry.Rule.Kind == rule.FileRule && (commands != "" || hasYield(effects)) {
		for i := range entry.Plan.Outputs {
			name := p.canonicalTarget(entry.Plan.Outputs[i], true)
			if !slices.Contains(entry.OwnershipPaths, name) {
				entry.OwnershipPaths = slices.Append(p.Alloc, entry.OwnershipPaths, name)
			} else {
				mem.FreeString(p.Alloc, name)
			}
		}
	}
	for i := range writes {
		name := p.canonicalTarget(writes[i], true)
		if !slices.Contains(entry.OwnershipPaths, name) {
			entry.OwnershipPaths = slices.Append(p.Alloc, entry.OwnershipPaths, name)
		} else {
			mem.FreeString(p.Alloc, name)
		}
	}
	if len(entry.OwnershipPaths) == 0 {
		return diagnostic.Diagnostic{}
	}
	return p.Options.Ownership(p.Options.OwnershipState, p, entry.OwnershipPaths, entry.Plan.Target, false)
}

func (p *Program) commitOwnership(entry *instance) diagnostic.Diagnostic {
	if p.Options.Ownership == nil || len(entry.OwnershipPaths) == 0 {
		return diagnostic.Diagnostic{}
	}
	d := p.Options.Ownership(p.Options.OwnershipState, p, entry.OwnershipPaths, entry.Plan.Target, true)
	if d.Code == "" {
		freeStrings(p.Alloc, entry.OwnershipPaths)
		entry.OwnershipPaths = nil
	}
	return d
}

func (p *Program) WarnOwnership(message string) {
	d := failure(p.Alloc, "CLEAN_STATE", message)
	d.Severity = diagnostic.Warning
	p.emit(Event{Kind: CacheWarning, Diagnostic: d})
}

// EnableAutomaticClean supplies an ordinary bare graph target, without replacing
// an authored rule/definition or tying cleanup to default's reachable outputs.
func (p *Program) EnableAutomaticClean(callback func(any, *Program) diagnostic.Diagnostic, state any) {
	if p.HasTarget("clean") {
		return
	}
	r := mem.Alloc[rule.Rule](p.Alloc)
	r.Kind = rule.TaskRule
	r.Outputs = slices.Append(p.Alloc, r.Outputs, rule.Target{Kind: rule.TargetName, Text: "clean"})
	p.Rules = slices.Append(p.Alloc, p.Rules, registeredRule{Rule: r})
	p.automaticCleanRule, p.AutomaticClean, p.AutomaticCleanState = r, callback, state
}

// CleanupContext deliberately uses a whole-source/configuration/environment
// guard. Unknown computed input membership and generated declarations withhold
// deletion rather than treating incomplete discovery as absence of consumers.
func (p *Program) CleanupContext() core.Signature {
	for i := range p.Rules {
		if len(p.Rules[i].Rule.Arguments) != 0 {
			return core.Signature{}
		}
		for j := range p.Rules[i].Rule.Inputs {
			input := p.Rules[i].Rule.Inputs[j]
			if input.Kind == rule.InputExpression || input.Kind == rule.InputWildcard || input.Kind == rule.InputString || input.Kind == rule.InputTemplate {
				return core.Signature{}
			}
		}
	}
	if len(p.GeneratedRules) != 0 {
		return core.Signature{}
	}
	var sink hashSink
	sink.state = newSHA256()
	sink.appendText("kame-clean-context-v1")
	// Session composition supplies a final separator newline. The caller's raw
	// authored-source signature still detects formatting edits; do not treat a
	// frontend-generated separator as a different cleanup context.
	sink.appendText(strings.TrimSpace(p.Parsed.Source.Text))
	// Include resolved declarations as well as authored bytes: a pure computed
	// output set can change without changing its expression's source spelling.
	sink.appendU64(uint64(len(p.Rules)))
	for i := range p.Rules {
		r := p.Rules[i].Rule
		sink.appendU64(uint64(r.Kind))
		sink.appendU64(uint64(len(r.Outputs)))
		for j := range r.Outputs {
			sink.appendText(r.Outputs[j].Text)
			sink.appendU64(uint64(r.Outputs[j].Kind))
		}
	}
	sink.appendU64(uint64(len(p.Eval.Registry.Items)))
	for i := range p.Eval.Registry.Items {
		sink.appendText(p.Eval.Registry.Items[i].Name)
		sink.appendText(p.Eval.Registry.Items[i].Version)
	}
	sink.appendU64(uint64(len(p.Configuration)))
	for i := range p.Configuration {
		sink.appendText(p.Configuration[i])
	}
	sink.appendU64(uint64(len(p.Options.Environment)))
	for i := range p.Options.Environment {
		sink.appendText(p.Options.Environment[i])
	}
	sink.appendU64(uint64(len(p.Options.ToolOverrides)))
	for i := range p.Options.ToolOverrides {
		sink.appendText(p.Options.ToolOverrides[i])
	}
	sink.appendU64(uint64(len(p.Options.Shell)))
	for i := range p.Options.Shell {
		sink.appendText(p.Options.Shell[i])
	}
	sink.appendU64(uint64(len(p.Eval.DefinitionArgs)))
	for i := range p.Eval.DefinitionArgs {
		signature := core.ValueSignature(p.Eval.DefinitionArgs[i])
		if signature.Mode != core.SignatureContent {
			return core.Signature{}
		}
		sink.state.Write(signature.Digest[:])
	}
	signature := core.Signature{Mode: core.SignatureContent}
	sink.state.Sum(signature.Digest[:])
	return signature
}

// CleanupExternalInputs inventories only static concrete paths. The context
// guard marks declarations needing further discovery unverifiable.
func (p *Program) CleanupExternalInputs() []string {
	var paths []string
	for i := range p.Rules {
		for j := range p.Rules[i].Rule.Inputs {
			input := p.Rules[i].Rule.Inputs[j]
			if input.Kind != rule.InputPath || p.HasTarget(input.Text) {
				continue
			}
			name := p.canonicalTarget(input.Text, true)
			if !slices.Contains(paths, name) {
				paths = slices.Append(p.Alloc, paths, name)
			} else {
				mem.FreeString(p.Alloc, name)
			}
		}
	}
	return paths
}
