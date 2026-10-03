package eval

import (
	"kame/core"
	"kame/lang/definition"
	"kame/lang/expr"
	"solod.dev/so/mem"
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
	replacement := mem.Alloc[definition.Definition](p.Alloc)
	replacement.Name, replacement.NameSpan, replacement.Span, replacement.Default = original.Name, original.NameSpan, original.Span, original.Default
	replacement.ValueKind = definition.ValueExpression
	replacement.Expression = mem.Alloc[expr.Expr](p.Alloc)
	replacement.Expression.Kind, replacement.Expression.Text, replacement.Expression.TextOwned, replacement.Expression.Span = expr.Symbol, owned(p.Alloc, text), true, original.Span
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
