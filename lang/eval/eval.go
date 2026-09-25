// Package eval evaluates LittleMake language ASTs over core values.
package eval

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lang/expr"
	"littlemake/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func (p *Program) Evaluate(run mem.Allocator, expression *expr.Expr, scope *Scope) Result {
	return p.evaluate(nil, scope, expression, &Context{Program: p, Scope: scope, Run: run, Requests: p.Requests})
}

// EvaluateWith evaluates an expression with caller-provided capability grants
// and selector frames. The context remains caller-owned.
func (p *Program) EvaluateWith(expression *expr.Expr, context *Context) Result {
	if p == nil || context == nil {
		return failure(mem.System, "EXPR_INVALID", source.Span{}, "missing evaluation context")
	}
	if context.Program == nil {
		context.Program = p
	}
	if context.Scope == nil {
		context.Scope = p.Scope
	}
	if context.Requests == nil {
		context.Requests = p.Requests
	}
	result := p.evaluate(context.Engine, context.Scope, expression, context)
	attachContextFrames(&result, context)
	attachSource(&result, context)
	return result
}

// attachContextFrames prepends caller frames. Owned diagnostics release every
// frame label, so borrowed context labels are cloned before attaching.
func attachContextFrames(result *Result, context *Context) {
	if result.Diagnostic.Code == "" || len(context.Frames) == 0 {
		return
	}
	a := context.Run
	if a == nil {
		a = mem.System
	}
	total := len(context.Frames) + len(result.Diagnostic.Frames)
	frames := slices.Make[diagnostic.Frame](a, total)
	for i := range context.Frames {
		label := context.Frames[i].Label
		if result.Diagnostic.Owned {
			label = owned(a, label)
		}
		frames[i] = diagnostic.Frame{Label: label, Span: context.Frames[i].Span}
	}
	copy(frames[len(context.Frames):], result.Diagnostic.Frames)
	if result.Diagnostic.Owned {
		for i := range result.Diagnostic.Frames {
			if result.Diagnostic.Frames[i].Label != "" {
				mem.FreeString(a, result.Diagnostic.Frames[i].Label)
			}
		}
	}
	slices.Free(a, result.Diagnostic.Frames)
	result.Diagnostic.Frames = frames
}

// EvaluateDefinition evaluates one lazy value definition in the caller's phase.
// Planning uses it to resolve only definitions reached by its active expression.
func (p *Program) EvaluateDefinition(key core.ResourceKey, context *Context) Result {
	if p == nil || key.Kind != core.ResourceDefinition {
		return failure(context.Run, "REF_MISSING", source.Span{}, "unknown definition")
	}
	for i := range p.Definitions {
		d := p.Definitions[i]
		if d.Name != key.Name {
			continue
		}
		result := p.definitionValue(nil, d, p.Scope, context)
		attachFrame(&result, context, d.Span, "definition")
		attachSource(&result, context)
		return result
	}
	return failure(context.Run, "REF_MISSING", source.Span{}, "unknown definition: "+key.Name)
}

func attachSource(result *Result, context *Context) {
	// Context and program sources outlive their diagnostic results. Diagnostics
	// borrowed from engine nodes must not be cloned or freed here; owned
	// diagnostics copy the source so Free releases it consistently.
	if result.Diagnostic.Code != "" && context.Source != "" && result.Diagnostic.Target == "" {
		if result.Diagnostic.Owned {
			result.Diagnostic.Target = cloneFailureText(context.Run, context.Source)
		} else {
			result.Diagnostic.Target = context.Source
		}
	}
}

func (p *Program) evaluate(engine *core.EngineContext, scope *Scope, expression *expr.Expr, context *Context) Result {
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
		return p.stringValue(scope, expression.Parts, context)
	case expr.Symbol, expr.Path:
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
	case expr.Lambda:
		function := mem.Alloc[Function](context.Run)
		function.Parameters, function.Body, function.Scope, function.Temporary = expression.Parameters, expression.Body, scope, true
		scope.Retain()
		return Result{Value: core.Value{Kind: core.Callable, Callable: function}}
	case expr.Selector:
		return p.selector(expression.Text, expression.Span, context)
	}
	return failure(context.Run, "EXPR_INVALID", expression.Span, "invalid expression")
}

func name(scope *Scope, name string, span source.Span, context *Context) Result {
	b := scope.lookup(name)
	if b == nil {
		return failure(context.Run, "REF_MISSING", span, "unknown reference: "+name)
	}
	if b.Kind == bindingValue {
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

func (p *Program) list(scope *Scope, items []*expr.Expr, context *Context) Result {
	values := slices.Make[core.Value](context.Run, len(items))
	for i := range items {
		r := p.evaluate(context.Engine, scope, items[i], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			freeValues(context.Run, values)
			return r
		}
		values[i] = r.Value
	}
	result := Result{Value: core.NewList(context.Run, values)}
	freeValues(context.Run, values)
	return result
}

func (p *Program) record(scope *Scope, fields []expr.Field, context *Context) Result {
	values := slices.Make[core.RecordField](context.Run, len(fields))
	for i := range fields {
		r := p.evaluate(context.Engine, scope, fields[i].Value, context)
		if r.Waiting || r.Diagnostic.Code != "" {
			freeRecord(context.Run, values)
			return r
		}
		values[i] = core.RecordField{Key: fields[i].Key, Value: r.Value}
	}
	result := Result{Value: core.NewRecord(context.Run, values)}
	freeRecord(context.Run, values)
	return result
}
