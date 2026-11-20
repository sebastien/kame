package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/definition"
	"kame/lang/expr"
)

// Contributions are assembled at registration, never assignments at runtime.
// Keep the authored operands/spans and reuse ordinary operand suspension state.
func (p *Program) registerContribution(d *definition.Definition) {
	var aggregate *definition.Definition
	b := p.Scope.lookup(d.Name)
	if b != nil && b.Kind != bindingDefinition {
		p.Valid = false
		p.Diagnostics = append(p.Diagnostics, diagnostic.Diagnostic{Code: "DEF_INVALID", Severity: diagnostic.Error, Message: "contributions require a value definition", Span: diagnostic.Span{Start: d.NameSpan.Start, End: d.NameSpan.End}})
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
			aggregate.Expression.Items = append(aggregate.Expression.Items, expr.Clone(p.Alloc, original.Expression))
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
		p.Definitions = append(p.Definitions, aggregate)
		state := mem.Alloc[definitionState](p.Alloc)
		state.Program, state.Definition = p, aggregate
		node := p.Engine.AddOwned(core.ResourceKey{Kind: core.ResourceDefinition, Name: d.Name}, evaluateDefinition, state, freeDefinitionState)
		node.Restartable = true
		p.Nodes = append(p.Nodes, definitionNode{Name: owned(p.Alloc, d.Name), Node: node})
	}
	aggregate.Expression.Items = append(aggregate.Expression.Items, expr.Clone(p.Alloc, d.Expression))
}

func (p *Program) newAggregate(d *definition.Definition) *definition.Definition {
	aggregate := mem.Alloc[definition.Definition](p.Alloc)
	aggregate.Name, aggregate.NameSpan, aggregate.Span, aggregate.Default = d.Name, d.NameSpan, d.Span, d.Default
	aggregate.Aggregate = true
	aggregate.Expression = mem.Alloc[expr.Expr](p.Alloc)
	aggregate.Expression.Kind, aggregate.Expression.Span = expr.List, d.Span
	p.Aggregates = append(p.Aggregates, aggregate)
	return aggregate
}

func (p *Program) aggregateValue(d *definition.Definition, scope *Scope, c *Context) Result {
	operands := p.argumentValues(scope, d.Expression.Items, c, d.Span)
	return joinContributionValues(d.Expression.Items, operands, c)
}

type localAggregate struct {
	Inherited   expr.Expr
	Expressions []*expr.Expr
	Scopes      []*Scope
}

func (a *localAggregate) append(e *expr.Expr, scope *Scope) {
	a.Expressions = append(a.Expressions, e)
	a.Scopes = append(a.Scopes, scope)
}

func (p *Program) evaluateLocal(engine *core.EngineContext, local *localDefinition, c *Context) Result {
	if local.Configured != nil {
		return p.evaluate(engine, local.Scope, local.Configured, c)
	}
	if local.Aggregate == nil {
		return p.evaluate(engine, local.Scope, local.Expression, c)
	}
	aggregate := local.Aggregate
	previous := c.Engine
	c.Engine = engine
	operands := p.argumentValuesMode(local.Scope, aggregate.Expressions, aggregate.Scopes, c, local.Expression.Span, false, "")
	c.Engine = previous
	return joinContributionValues(aggregate.Expressions, operands, c)
}

func joinContributionValues(expressions []*expr.Expr, operands argumentResult, c *Context) Result {
	if operands.Result.Waiting || operands.Result.Completed || operands.Result.Diagnostic.Code != "" {
		return operands.Result
	}
	values := operands.Values
	for i := range values {
		if values[i].Kind != core.List {
			result := failure(c.Run, "EXPR_INVALID", expressions[i].Span, "+= base and contributions must be lists; got "+core.KindName(values[i].Kind))
			previous := c.SourceOrigin
			c.SourceOrigin = expressions[i].SourceOrigin
			attachSource(&result, c)
			c.SourceOrigin = previous
			freeValuesWithCallables(c.Run, values)
			return result
		}
	}
	var items []core.Value
	for i := range values {
		for j := range values[i].List {
			items = append(items, values[i].List[j])
		}
	}
	result := Result{Value: core.NewList(c.Run, items)}
	mem.FreeSlice(nil, items)
	// NewList transfers callable wrappers; don't discard their captured scopes.
	freeValues(c.Run, values)
	return result
}
