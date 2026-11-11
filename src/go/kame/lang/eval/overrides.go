package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"

	"strings"
)

// OverrideDefinition installs a literal string before a value definition is
// requested. The evaluator owns its replacement AST; the authored AST remains
// untouched, retaining its name and diagnostic position.
func (p *Program) OverrideDefinition(name, text string) bool {
	literal := expr.Expr{Kind: expr.Symbol, Text: text}
	return p.overrideDefinition(name, &literal)
}

// OverrideDefinitionValue accepts closed literal data only. Validation precedes
// registration, including for overrides of definitions that are never demanded.
func (p *Program) OverrideDefinitionValue(name, text string) bool {
	parsed := expr.Parse(p.Alloc, "<set>", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || !expr.IsLiteral(parsed.Expr) {
		return false
	}
	return p.overrideDefinition(name, parsed.Expr)
}

func (p *Program) overrideDefinition(name string, expression *expr.Expr) bool {
	node := p.Definition(name)
	if node == nil || node.Requested || node.State != core.NodeIdle {
		return false
	}
	state := node.Context.(*definitionState)
	original := state.Definition
	replacement := p.expressionDefinition(original, expression)
	for i := range p.Definitions {
		if p.Definitions[i] == original {
			p.Definitions[i] = replacement
		}
	}
	if state.Owned {
		definition.Free(p.Alloc, original)
	}
	state.Definition, state.Owned = replacement, true
	return true
}

func (p *Program) literalDefinition(original *definition.Definition, text string) *definition.Definition {
	literal := expr.Expr{Kind: expr.Symbol, Text: text, Span: original.Span}
	return p.expressionDefinition(original, &literal)
}

func (p *Program) expressionDefinition(original *definition.Definition, expression *expr.Expr) *definition.Definition {
	replacement := mem.Alloc[definition.Definition](p.Alloc)
	replacement.Name, replacement.NameSpan, replacement.Span, replacement.Default = original.Name, original.NameSpan, original.Span, original.Default
	replacement.Expression = expr.Clone(p.Alloc, expression)
	replacement.Expression.Span = original.Span
	return replacement
}

// scopedDefinition retains ordinary engine publication, replay and cycle handling
// while isolating values derived from different target snapshots.
func (p *Program) scopedDefinition(key core.ResourceKey, context *Context) *core.Node {
	if key.Kind != core.ResourceDefinition {
		return nil
	}
	// The environment namespace alone cannot distinguish rule-local selectors.
	// Keep the original namespace in state so nested definitions bind identically.
	namespace := context.DefinitionNamespace
	if context.forbidsFinalization() {
		identity := core.NewDigest()
		identity.Write(namespace[:])
		identity.Text("finalization-forbidden")
		identity.Uint64(uint64(context.Phase))
		identity.Sum(namespace[:])
	}
	invalidFrames := false
	if len(context.RuleFrames) != 0 {
		identity := core.NewDigest()
		identity.Write(namespace[:])
		identity.Uint64(uint64(len(context.RuleFrames)))
		for i := range context.RuleFrames {
			frame := context.RuleFrames[i]
			flags := uint64(0)
			if frame.FileRule {
				flags |= 1
			}
			if frame.ServiceRule {
				flags |= 2
			}
			identity.Uint64(flags)
			inputs := core.ValueSignature(core.Value{Kind: core.List, List: frame.Inputs})
			outputs := core.ValueSignature(core.Value{Kind: core.List, List: frame.Outputs})
			newer := core.ValueSignature(core.Value{Kind: core.List, List: frame.NewerInputs})
			if inputs.Mode == core.SignatureUnavailable || outputs.Mode == core.SignatureUnavailable || newer.Mode == core.SignatureUnavailable {
				invalidFrames = true
			}
			identity.Uint64(uint64(inputs.Mode))
			identity.Uint64(uint64(outputs.Mode))
			identity.Uint64(uint64(newer.Mode))
			identity.Write(inputs.Digest[:])
			identity.Write(outputs.Digest[:])
			identity.Write(newer.Digest[:])
		}
		identity.Sum(namespace[:])
	}
	var digest [64]byte
	const hex = "0123456789abcdef"
	for i := range namespace {
		digest[i*2] = hex[namespace[i]>>4]
		digest[i*2+1] = hex[namespace[i]&15]
	}
	name := "\x00definition:" + string(digest[:]) + ":" + key.Name
	scopedKey := core.ResourceKey{Kind: core.ResourceDefinition, Name: name}
	if node := p.Engine.Lookup(scopedKey); node != nil {
		return node
	}
	var authored *definition.Definition
	for i := range p.Definitions {
		d := p.Definitions[i]
		if d.Name == key.Name {
			authored = d
			break
		}
	}
	if authored == nil {
		return nil
	}
	state := mem.Alloc[definitionState](p.Alloc)
	state.Program, state.Definition, state.Scoped = p, authored, true
	state.Namespace, state.Phase = context.DefinitionNamespace, context.Phase
	state.HasNamespace, state.HasEnvironment = context.HasDefinitionNamespace, context.HasDefinitionNamespace || context.HasEnvironment
	state.FinalizationForbidden = context.forbidsFinalization()
	// A restriction-only view does not redefine ordinary lazy-definition effect
	// timing. Existing target namespaces still carry their original phase policy.
	if !context.HasDefinitionNamespace {
		state.Phase = EvaluatePhase
	}
	state.InvalidRuleFrames = invalidFrames
	// Opaque or over-depth values cannot define a stable cached context. Reject
	// them on evaluation rather than sharing zero digests or retaining processes.
	for i := 0; !invalidFrames && i < len(context.RuleFrames); i++ {
		frame := RuleFrame{FileRule: context.RuleFrames[i].FileRule, ServiceRule: context.RuleFrames[i].ServiceRule}
		for j := range context.RuleFrames[i].Inputs {
			frame.Inputs = append(frame.Inputs, context.RuleFrames[i].Inputs[j].Clone(p.Alloc))
		}
		for j := range context.RuleFrames[i].Outputs {
			frame.Outputs = append(frame.Outputs, context.RuleFrames[i].Outputs[j].Clone(p.Alloc))
		}
		for j := range context.RuleFrames[i].NewerInputs {
			frame.NewerInputs = append(frame.NewerInputs, context.RuleFrames[i].NewerInputs[j].Clone(p.Alloc))
		}
		state.RuleFrames = append(state.RuleFrames, frame)
	}
	for i := range context.Environment {
		state.Environment = append(state.Environment, owned(p.Alloc, context.Environment[i]))
	}
	prefix := "KAME_" + key.Name + "="
	text, provided := "", false
	for i := range state.Environment {
		if strings.HasPrefix(state.Environment[i], prefix) {
			text, provided = state.Environment[i][len(prefix):], true
		}
	}
	typed := false
	for i := range p.explicitDefinitionOverrides {
		override := definition.ParseOverride(p.explicitDefinitionOverrides[i])
		if override.Name == key.Name {
			text, provided, typed = override.Text, true, override.Typed
		}
	}
	canonical := p.Definition(key.Name).Context.(*definitionState)
	if provided && !typed {
		state.Definition, state.Owned = p.literalDefinition(authored, text), true
	} else if canonical.Owned {
		// Canonical literals belong to the unscoped node and can be replaced again
		// before demand. Keep this scoped snapshot independent of that lifetime.
		state.Definition, state.Owned = p.expressionDefinition(authored, canonical.Definition.Expression), true
	}
	node := p.Engine.AddOwned(scopedKey, evaluateDefinition, state, freeDefinitionState)
	if node == nil {
		freeDefinitionState(p.Alloc, state)
		return nil
	}
	node.Restartable = true
	state.bindInputPolicy(node.Generation, context.InputClosed, context.RecordBody)
	return node
}

// Bind once per generation at first demand, not in a new definition namespace:
// changing input policy must not rerun an already memoized definition.
func (p *Program) bindDefinitionInput(key core.ResourceKey, context *Context) {
	if key.Kind != core.ResourceDefinition {
		return
	}
	name := AuthoredDefinitionName(key.Name)
	for i := range p.Definitions {
		if p.Definitions[i].Name != name {
			continue
		}
		node := p.Engine.Lookup(key)
		if node != nil {
			state := node.Context.(*definitionState)
			if !state.InputBound || state.InputGeneration != node.Generation {
				state.bindInputPolicy(node.Generation, context.InputClosed, context.RecordBody)
			}
		}
		return
	}
}

// DefinitionWith returns a lazy node bound to the caller's runtime snapshot.
// Without a namespace it preserves ordinary invocation-level evaluation.
func (p *Program) DefinitionWith(name string, context *Context) *core.Node {
	node := p.Definition(name)
	if node == nil || context == nil || (!context.HasDefinitionNamespace && !context.needsFinalizationView()) {
		if node != nil && context != nil {
			p.bindDefinitionInput(node.Key, context)
		}
		return node
	}
	scoped := p.scopedDefinition(node.Key, context)
	if scoped != nil {
		p.bindDefinitionInput(scoped.Key, context)
	}
	return scoped
}

// AuthoredDefinitionName removes invocation-only snapshot identity from a
// definition observation. Persisted reuse records bind this name anew.
func AuthoredDefinitionName(name string) string {
	const prefix = "\x00definition:"
	offset := len(prefix) + 64
	if strings.HasPrefix(name, prefix) && len(name) > offset && name[offset] == ':' {
		return name[offset+1:]
	}
	return name
}

// SetDefinitionOverrides retains explicit CLI configuration separately from
// ambient overrides, so it wins in every target environment.
func (p *Program) SetDefinitionOverrides(values []string) {
	for i := range p.explicitDefinitionOverrides {
		mem.FreeString(p.Alloc, p.explicitDefinitionOverrides[i])
	}
	mem.FreeSlice(nil, p.explicitDefinitionOverrides)
	p.explicitDefinitionOverrides = nil
	for i := range values {
		p.explicitDefinitionOverrides = append(p.explicitDefinitionOverrides, owned(p.Alloc, values[i]))
	}
}
