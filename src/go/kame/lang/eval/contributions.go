package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/definition"
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Contributions are assembled at registration, never assignments at runtime.
// Keep the authored operands/spans and reuse ordinary operand suspension state.
func (p *Program) registerContribution(d *definition.Definition) {
	var aggregate *definition.Definition
	b := p.Scope.lookup(d.Name)
	if b != nil && b.Kind != bindingDefinition {
		p.Valid = false
		p.Diagnostics = slices.Append(p.Alloc, p.Diagnostics, diagnostic.Diagnostic{Code: "DEF_INVALID", Severity: diagnostic.Error, Message: "contributions require a value definition", Span: diagnostic.Span{Start: d.NameSpan.Start, End: d.NameSpan.End}})
		return
	}
	for i := range p.Definitions {
		original := p.Definitions[i]
		if original.Name != d.Name {
			continue
		}
		aggregate = original
		if !original.Aggregate {
			aggregate = p.newAggregate(original)
			aggregate.Expression.Items = slices.Append(p.Alloc, aggregate.Expression.Items, expr.Clone(p.Alloc, original.Expression))
			p.Definitions[i] = aggregate
			for j := range p.Nodes {
				if p.Nodes[j].Name == d.Name {
					state := p.Nodes[j].Node.Context.(*definitionState)
					state.Definition = aggregate
					break
				}
			}
		}
		break
	}
	if aggregate == nil {
		aggregate = p.newAggregate(d)
		p.Scope.setDefinition(d.Name)
		p.Definitions = slices.Append(p.Alloc, p.Definitions, aggregate)
		state := mem.Alloc[definitionState](p.Alloc)
		state.Program, state.Definition = p, aggregate
		node := p.Engine.AddOwned(core.ResourceKey{Kind: core.ResourceDefinition, Name: d.Name}, evaluateDefinition, state, freeDefinitionState)
		node.Restartable = true
		p.Nodes = slices.Append(p.Alloc, p.Nodes, definitionNode{Name: owned(p.Alloc, d.Name), Node: node})
	}
	aggregate.Expression.Items = slices.Append(p.Alloc, aggregate.Expression.Items, expr.Clone(p.Alloc, d.Expression))
}

func (p *Program) newAggregate(d *definition.Definition) *definition.Definition {
	aggregate := mem.Alloc[definition.Definition](p.Alloc)
	aggregate.Name, aggregate.NameSpan, aggregate.Span, aggregate.Default = d.Name, d.NameSpan, d.Span, d.Default
	aggregate.Aggregate = true
	aggregate.Expression = mem.Alloc[expr.Expr](p.Alloc)
	aggregate.Expression.Kind, aggregate.Expression.Span = expr.List, d.Span
	p.Aggregates = slices.Append(p.Alloc, p.Aggregates, aggregate)
	return aggregate
}

func (p *Program) aggregateValue(d *definition.Definition, scope *Scope, c *Context) Result {
	operands := p.argumentValues(scope, d.Expression.Items, c, d.Span)
	if operands.Result.Waiting || operands.Result.Diagnostic.Code != "" {
		return operands.Result
	}
	values := operands.Values
	for i := range values {
		if values[i].Kind != core.List {
			result := failure(c.Run, "EXPR_INVALID", d.Expression.Items[i].Span, "+= base and contributions must be lists; got "+core.KindName(values[i].Kind))
			freeValuesWithCallables(c.Run, values)
			return result
		}
	}
	var items []core.Value
	for i := range values {
		for j := range values[i].List {
			items = slices.Append(c.Run, items, values[i].List[j])
		}
	}
	result := Result{Value: core.NewList(c.Run, items)}
	slices.Free(c.Run, items)
	// NewList transfers callable wrappers; don't discard their captured scopes.
	freeValues(c.Run, values)
	return result
}
