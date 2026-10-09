package eval

import (
	"kame/core"
	"kame/lang/definition"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// OverrideDefinition installs a literal string before a value definition is
// requested. The evaluator owns its replacement AST; the authored AST remains
// untouched, retaining its name and diagnostic position.
func (p *Program) OverrideDefinition(name, text string) bool {
	node := p.Definition(name)
	if node == nil || node.Requested || node.State != core.NodeIdle {
		return false
	}
	state := node.Context.(*definitionState)
	original := state.Definition
	replacement := p.literalDefinition(original, text)
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
	replacement := mem.Alloc[definition.Definition](p.Alloc)
	replacement.Name, replacement.NameSpan, replacement.Span, replacement.Default = original.Name, original.NameSpan, original.Span, original.Default
	replacement.Expression = mem.Alloc[expr.Expr](p.Alloc)
	replacement.Expression.Kind, replacement.Expression.Text, replacement.Expression.TextOwned, replacement.Expression.Span = expr.Symbol, owned(p.Alloc, text), true, original.Span
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
	state.InvalidRuleFrames = invalidFrames
	// Opaque or over-depth values cannot define a stable cached context. Reject
	// them on evaluation rather than sharing zero digests or retaining processes.
	for i := 0; !invalidFrames && i < len(context.RuleFrames); i++ {
		frame := RuleFrame{FileRule: context.RuleFrames[i].FileRule, ServiceRule: context.RuleFrames[i].ServiceRule}
		for j := range context.RuleFrames[i].Inputs {
			frame.Inputs = slices.Append(p.Alloc, frame.Inputs, context.RuleFrames[i].Inputs[j].Clone(p.Alloc))
		}
		for j := range context.RuleFrames[i].Outputs {
			frame.Outputs = slices.Append(p.Alloc, frame.Outputs, context.RuleFrames[i].Outputs[j].Clone(p.Alloc))
		}
		for j := range context.RuleFrames[i].NewerInputs {
			frame.NewerInputs = slices.Append(p.Alloc, frame.NewerInputs, context.RuleFrames[i].NewerInputs[j].Clone(p.Alloc))
		}
		state.RuleFrames = slices.Append(p.Alloc, state.RuleFrames, frame)
	}
	for i := range context.Environment {
		state.Environment = slices.Append(p.Alloc, state.Environment, owned(p.Alloc, context.Environment[i]))
	}
	prefix := "KAME_" + key.Name + "="
	text, provided := "", false
	for i := range state.Environment {
		if strings.HasPrefix(state.Environment[i], prefix) {
			text, provided = state.Environment[i][len(prefix):], true
		}
	}
	prefix = key.Name + "="
	for i := range p.explicitDefinitionOverrides {
		if strings.HasPrefix(p.explicitDefinitionOverrides[i], prefix) {
			text, provided = p.explicitDefinitionOverrides[i][len(prefix):], true
		}
	}
	canonical := p.Definition(key.Name).Context.(*definitionState)
	if provided {
		state.Definition, state.Owned = p.literalDefinition(authored, text), true
	} else if canonical.Owned {
		// Canonical literals belong to the unscoped node and can be replaced again
		// before demand. Keep this scoped snapshot independent of that lifetime.
		state.Definition, state.Owned = p.literalDefinition(authored, authored.Expression.Text), true
	}
	node := p.Engine.AddOwned(scopedKey, evaluateDefinition, state, freeDefinitionState)
	if node == nil {
		freeDefinitionState(p.Alloc, state)
		return nil
	}
	node.Restartable = true
	return node
}

// DefinitionWith returns a lazy node bound to the caller's runtime snapshot.
// Without a namespace it preserves ordinary invocation-level evaluation.
func (p *Program) DefinitionWith(name string, context *Context) *core.Node {
	node := p.Definition(name)
	if node == nil || context == nil || !context.HasDefinitionNamespace {
		return node
	}
	return p.scopedDefinition(node.Key, context)
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
	slices.Free(p.Alloc, p.explicitDefinitionOverrides)
	p.explicitDefinitionOverrides = nil
	for i := range values {
		p.explicitDefinitionOverrides = slices.Append(p.Alloc, p.explicitDefinitionOverrides, owned(p.Alloc, values[i]))
	}
}
