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

func (p *Program) body(scope *Scope, body []*expr.Expr, context *Context) Result {
	result := Result{Value: core.Value{Kind: core.Nil}}
	for i := range body {
		result.Value.Free(context.Run)
		result = p.evaluate(context.Engine, scope, body[i], context)
		if result.Waiting || result.Diagnostic.Code != "" {
			return result
		}
	}
	return result
}

func (p *Program) fallback(scope *Scope, values []*expr.Expr, context *Context) Result {
	for i := range values {
		r := p.evaluate(context.Engine, scope, values[i], context)
		if r.Waiting || r.Diagnostic.Code != "REF_MISSING" {
			return r
		}
		r.Diagnostic.Free(context.Run)
	}
	return Result{Value: core.Value{Kind: core.Nil}}
}

func (p *Program) let(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) == 0 || values[0].Kind != expr.List || len(values[0].Items)%2 != 0 {
		return failure(context.Run, "EXPR_INVALID", span, "let needs name/value pairs")
	}
	child := newScope(context.Run, scope)
	defer child.Free()
	for i := 0; i < len(values[0].Items); i += 2 {
		name := values[0].Items[i]
		if name.Kind != expr.Name {
			return failure(context.Run, "DEF_INVALID", name.Span, "let binding needs a name")
		}
		r := p.evaluate(context.Engine, child, values[0].Items[i+1], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		child.setValue(name.Text, r.Value)
		r.Value.Free(context.Run)
	}
	return p.body(child, values[1:], context)
}

func (p *Program) def(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) < 2 || values[0].Kind != expr.Name {
		return failure(context.Run, "DEF_INVALID", span, "def needs a name and value")
	}
	if len(values) >= 3 && values[1].Kind == expr.List {
		parameters := slices.Make[expr.Parameter](context.Run, len(values[1].Items))
		for i := range values[1].Items {
			parameter := values[1].Items[i]
			if parameter.Kind != expr.Name || (parameter.Rest && i != len(values[1].Items)-1) {
				for j := 0; j < i; j++ {
					mem.FreeString(context.Run, parameters[j].Name)
				}
				slices.Free(context.Run, parameters)
				return failure(context.Run, "DEF_INVALID", parameter.Span, "invalid function parameter")
			}
			for j := 0; j < i; j++ {
				if parameters[j].Name == parameter.Text {
					for j := 0; j < i; j++ {
						mem.FreeString(context.Run, parameters[j].Name)
					}
					slices.Free(context.Run, parameters)
					return failure(context.Run, "DEF_INVALID", parameter.Span, "duplicate function parameter")
				}
			}
			parameters[i] = expr.Parameter{Name: owned(context.Run, parameter.Text), Span: parameter.Span, Rest: parameter.Rest}
		}
		function := mem.Alloc[Function](context.Run)
		for i := 2; i < len(values); i++ {
			function.Body = slices.Append(context.Run, function.Body, expr.Clone(context.Run, values[i]))
		}
		function.Parameters, function.Scope, function.Owned, function.ParametersOwned, function.ParameterNamesOwned, function.BodyOwned = parameters, scope, true, true, true, true
		scope.setFunction(values[0].Text, function)
		return Result{Value: core.Value{Kind: core.Nil}}
	}
	if len(values) != 2 {
		return failure(context.Run, "DEF_INVALID", span, "invalid def form")
	}
	r := p.evaluate(context.Engine, scope, values[1], context)
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	scope.setValue(values[0].Text, r.Value)
	return r
}

func (p *Program) evalText(scope *Scope, values []*expr.Expr, context *Context, span source.Span) Result {
	if len(values) != 1 {
		return failure(context.Run, "EXPR_INVALID", span, "eval needs one string")
	}
	r := p.evaluate(context.Engine, scope, values[0], context)
	if r.Waiting || r.Diagnostic.Code != "" {
		return r
	}
	if r.Value.Kind != core.String {
		r.Value.Free(context.Run)
		return failure(context.Run, "EXPR_INVALID", span, "eval needs text")
	}
	parsed := expr.Parse(context.Run, "<eval>", r.Value.Text)
	r.Value.Free(context.Run)
	if len(parsed.Diagnostics) != 0 {
		parseDiagnostic := parsed.Diagnostics[0]
		parsed.Free()
		return Result{Diagnostic: diagnostic.Diagnostic{Code: parseDiagnostic.Code, Severity: diagnosticSeverity(parseDiagnostic.Severity), Message: parseDiagnostic.Message, Span: diagnostic.Span{Start: parseDiagnostic.Span.Start, End: parseDiagnostic.Span.End}}}
	}
	result := p.evaluate(context.Engine, scope, parsed.Expr, context)
	parsed.Free()
	return result
}
