// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
)

func (p *Program) evaluate(engine *core.EngineContext, scope *Scope, expression *expr.Expr, context *Context) Result {
	result := p.evaluateExpression(engine, scope, expression, context)
	attachSource(&result, context)
	return result
}

func (p *Program) evaluateExpression(engine *core.EngineContext, scope *Scope, expression *expr.Expr, context *Context) Result {
	if expression == nil {
		return failure(context.Run, "EXPR_INVALID", source.Span{}, "definition needs an expression value")
	}
	context.Engine, context.Scope, context.Requests = engine, scope, p.Requests
	switch expression.Kind {
	case expr.Nil:
		return Result{Value: core.Value{Kind: core.Nil}}
	case expr.Boolean:
		return Result{Value: core.Value{Kind: core.Bool, Bool: expression.Bool}}
	case expr.Integer:
		return Result{Value: core.Value{Kind: core.Int, Int: expression.Int}}
	case expr.Float:
		return Result{Value: core.Value{Kind: core.Float, Float: expression.Float}}
	case expr.String:
		if expression.Pattern != nil {
			return Result{Value: core.NewPattern(context.Run, expression.Text)}
		}
		return p.stringValue(scope, expression.Parts, context)
	case expr.Symbol:
		return Result{Value: core.NewString(context.Run, expression.Text)}
	case expr.Path:
		if expression.Pattern != nil {
			return Result{Value: core.NewPattern(context.Run, expression.Text)}
		}
		return Result{Value: core.NewString(context.Run, expression.Text)}
	case expr.Name:
		return name(scope, expression.Text, expression.Span, context)
	case expr.Reference:
		return p.reference(scope, expression, context)
	case expr.List:
		return p.list(scope, expression.Items, context)
	case expr.Record:
		return p.record(scope, expression.Fields, context)
	case expr.Application:
		return p.application(scope, expression, context)
	case expr.CommandCapture, expr.CommandGraph:
		return p.capture(expression, context)
	case expr.Lambda:
		function := mem.Alloc[Function](context.Run)
		function.Kind, function.Parameters, function.Body, function.Scope = FunctionTemporary, expression.Parameters, expression.Body, scope
		scope.Retain()
		return Result{Value: core.Value{Kind: core.Callable, Callable: function}}
	case expr.Section:
		function := mem.Alloc[Function](context.Run)
		function.Kind, function.Parameters, function.Expression, function.Scope, function.Section = FunctionTemporary, expression.Parameters, expression.Body[0], scope, true
		scope.Retain()
		return Result{Value: core.Value{Kind: core.Callable, Callable: function}}
	case expr.Placeholder:
		return placeholder(scope, expression, context)
	case expr.Selector:
		return p.selector(expression.Text, expression.Span, context)
	}
	return failure(context.Run, "EXPR_INVALID", expression.Span, "invalid expression")
}

// placeholder reads the indexed argument from the nearest enclosing section
// scope. Placeholders never resolve through name lookup.
func placeholder(scope *Scope, expression *expr.Expr, context *Context) Result {
	for current := scope; current != nil; current = current.Parent {
		if current.Section == nil {
			continue
		}
		index := int(expression.Int)
		if index >= len(current.Section) {
			break
		}
		return Result{Value: current.Section[index].Clone(context.Run)}
	}
	return failure(context.Run, "EXPR_INVALID", expression.Span, "placeholder outside section")
}

func name(scope *Scope, name string, span source.Span, context *Context) Result {
	b := scope.lookup(name)
	if b == nil {
		return failure(context.Run, "REF_MISSING", span, "unknown reference: "+name)
	}
	if b.Kind == bindingValue {
		if b.Value.Kind == core.Callable {
			// Binding-owned callables are returned as borrowed wrappers so
			// call sites release the wrapper without freeing the binding's
			// function or scope. The wrapper retains the scope so an escaped
			// callable keeps its capture alive until Result.Free runs the
			// cycle breaker.
			source := b.Value.Callable.(*Function)
			wrapper := borrowFunction(context.Run, source)
			return Result{Value: core.Value{Kind: core.Callable, Callable: wrapper}}
		}
		return Result{Value: b.Value.Clone(context.Run)}
	}
	if b.Kind == bindingFunction {
		return Result{Value: core.Value{Kind: core.Callable, Callable: b.Function}}
	}
	if context.Engine == nil && context.ResolveDefinition != nil {
		return context.ResolveDefinition(context.ResolverState, b.Definition, context)
	}
	if context.Engine == nil || !context.Engine.Dependency(b.Definition) {
		if context.Engine != nil && context.Engine.Failed() {
			return Result{Diagnostic: context.Engine.Diagnostic()}
		}
		return Result{Waiting: true}
	}
	value := context.Engine.Value(b.Definition)
	if !value.OK {
		return Result{Waiting: true}
	}
	return Result{Value: value.Value.Clone(context.Run)}
}
