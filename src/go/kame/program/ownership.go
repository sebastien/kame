package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"kame/lang/rule"
	"kame/lang/source"
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
	// Lock resources before the uncertain production record and any effects.
	if d := p.holdResources(entry); d.Code != "" {
		return d
	}
	if len(entry.OwnershipPaths) == 0 {
		return diagnostic.Diagnostic{}
	}
	return p.Options.Ownership(p.Options.OwnershipState, p, entry.OwnershipPaths, entry.Plan.Target, false)
}

// holdResources serializes concurrent producers on declared outputs and on the
// target name of recipes without declared file outputs. Paths are acquired as
// one sorted batch; repeated calls for the same instance add nothing.
func (p *Program) holdResources(entry *instance) diagnostic.Diagnostic {
	if p.Options.LockResources == nil || p.Options.DryRun {
		return diagnostic.Diagnostic{}
	}
	var fresh []LockedResource
	if entry.Rule.Kind != rule.FileRule && !heldResource(entry.Resources, entry.Plan.Target, "task") {
		fresh = slices.Append(p.Alloc, fresh, LockedResource{Name: cloneText(p.Alloc, entry.Plan.Target), Kind: cloneText(p.Alloc, "task")})
	}
	for i := range entry.OwnershipPaths {
		if heldResource(entry.Resources, entry.OwnershipPaths[i], "output") {
			continue
		}
		fresh = slices.Append(p.Alloc, fresh, LockedResource{Name: cloneText(p.Alloc, entry.OwnershipPaths[i]), Kind: cloneText(p.Alloc, "output")})
	}
	if len(fresh) == 0 {
		slices.Free(p.Alloc, fresh)
		return diagnostic.Diagnostic{}
	}
	sortResourceBatch(fresh)
	d := p.Options.LockResources(p.Options.ResourcesState, fresh)
	if d.Code != "" {
		freeResources(p.Alloc, fresh)
		slices.Free(p.Alloc, fresh)
		return d
	}
	for i := range fresh {
		entry.Resources = slices.Append(p.Alloc, entry.Resources, fresh[i])
	}
	slices.Free(p.Alloc, fresh)
	return diagnostic.Diagnostic{}
}

func heldResource(resources []LockedResource, name string, kind string) bool {
	for i := range resources {
		if resources[i].Kind == kind && resources[i].Name == name {
			return true
		}
	}
	return false
}

// sortResourceBatch keeps cross-process acquisition order stable for the paths
// a producer declares. Task names never collide with declared paths.
func sortResourceBatch(resources []LockedResource) {
	for i := 1; i < len(resources); i++ {
		item := resources[i]
		j := i - 1
		for j >= 0 && strings.Compare(resources[j].Name, item.Name) > 0 {
			resources[j+1] = resources[j]
			j--
		}
		resources[j+1] = item
	}
}

func freeResources(a mem.Allocator, resources []LockedResource) {
	for i := range resources {
		mem.FreeString(a, resources[i].Name)
		mem.FreeString(a, resources[i].Kind)
	}
}

func (p *Program) releaseResources(entry *instance) {
	if len(entry.Resources) == 0 {
		return
	}
	if p.Options.UnlockResources != nil {
		p.Options.UnlockResources(p.Options.ResourcesState, entry.Resources)
	}
	freeResources(p.Alloc, entry.Resources)
	slices.Free(p.Alloc, entry.Resources)
	entry.Resources = nil
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

// CleanupBlocker describes the first declaration preventing complete read-only
// ownership verification. Its source is borrowed; it never contains values.
type CleanupBlocker struct {
	Reason   string
	Location eval.Location
}

func (p *Program) cleanupBlocker(reason string, span source.Span) CleanupBlocker {
	return CleanupBlocker{Reason: reason, Location: p.Eval.LocateSource(p.Parsed.Source.Name, span)}
}

func (p *Program) CleanupContextBlocker() CleanupBlocker {
	for i := range p.Rules {
		r := p.Rules[i].Rule
		if len(r.Arguments) != 0 {
			return p.cleanupBlocker("target-arguments", r.Arguments[0].Span)
		}
		for j := range r.Inputs {
			input := r.Inputs[j]
			if _, literal := rule.LiteralInput(input); literal {
				continue
			}
			reason := "computed-prerequisite"
			if input.Kind == rule.InputWildcard {
				reason = "wildcard-prerequisite"
			}
			if input.Kind == rule.InputString {
				reason = "interpolated-prerequisite"
			}
			if input.Kind == rule.InputTemplate {
				reason = "capture-prerequisite"
			}
			return p.cleanupBlocker(reason, input.Span)
		}
	}
	if len(p.GeneratedRules) != 0 {
		return p.cleanupBlocker("generated-declarations", p.GeneratedRules[0].Header)
	}
	for i := range p.Eval.DefinitionArgs {
		if core.ValueSignature(p.Eval.DefinitionArgs[i]).Mode != core.SignatureContent {
			return CleanupBlocker{Reason: "unhashable-arguments"}
		}
	}
	return CleanupBlocker{}
}

// CleanupContext deliberately uses a whole-source/configuration/environment
// guard. Unknown computed input membership and generated declarations withhold
// deletion rather than treating incomplete discovery as absence of consumers.
func (p *Program) CleanupContext() core.Signature {
	if p.CleanupContextBlocker().Reason != "" {
		return core.Signature{}
	}
	var sink hashSink
	sink.state = core.NewDigest()
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
			text, literal := rule.LiteralInput(input)
			if !literal || !isFileName(text) || p.HasTarget(text) {
				continue
			}
			name := p.canonicalTarget(text, true)
			if !slices.Contains(paths, name) {
				paths = slices.Append(p.Alloc, paths, name)
			} else {
				mem.FreeString(p.Alloc, name)
			}
		}
	}
	return paths
}
