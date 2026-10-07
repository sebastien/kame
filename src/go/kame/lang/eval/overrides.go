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
	replacement.ValueKind = definition.ValueExpression
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
	var digest [64]byte
	const hex = "0123456789abcdef"
	for i := range context.DefinitionNamespace {
		digest[i*2] = hex[context.DefinitionNamespace[i]>>4]
		digest[i*2+1] = hex[context.DefinitionNamespace[i]&15]
	}
	name := "\x00definition:" + string(digest[:]) + ":" + key.Name
	scopedKey := core.ResourceKey{Kind: core.ResourceDefinition, Name: name}
	if node := p.Engine.Lookup(scopedKey); node != nil {
		return node
	}
	var authored *definition.Definition
	for i := range p.Script.Items {
		d := p.Script.Items[i].Definition
		if d != nil && !d.Function && d.Name == key.Name {
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
	for i := range context.RuleFrames { if context.RuleFrames[i].ServiceRule { state.ServiceRule = true } }
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
	if provided {
		state.Definition, state.Owned = p.literalDefinition(authored, text), true
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
