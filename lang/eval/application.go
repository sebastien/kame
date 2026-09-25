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

func (p *Program) application(scope *Scope, expression *expr.Expr, context *Context) Result {
	if len(expression.Items) == 0 {
		return failure(context.Run, "EXPR_INVALID", expression.Span, "empty application")
	}
	head := expression.Items[0]
	if head.Kind == expr.Name {
		if head.Text == "?" {
			return p.fallback(scope, expression.Items[1:], context)
		}
		if head.Text == "let" {
			return p.let(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "def" {
			return p.def(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "eval" {
			return p.evalText(scope, expression.Items[1:], context, expression.Span)
		}
	}
	callee := p.evaluate(context.Engine, scope, head, context)
	if callee.Waiting || callee.Diagnostic.Code != "" {
		if callee.Diagnostic.Code == "REF_MISSING" && head.Kind == expr.Name && p.Registry != nil {
			operation := p.Registry.lookup(head.Text)
			callee.Diagnostic.Free(context.Run)
			if operation != nil {
				result := p.operation(scope, operation, expression.Items[1:], context, expression.Span)
				attachFrame(&result, context, expression.Span, "operation")
				return result
			}
			return failure(context.Run, "OP_UNKNOWN", head.Span, "unknown operation: "+head.Text)
		}
		return callee
	}
	if callee.Value.Kind != core.Callable {
		callee.Value.Free(context.Run)
		return failure(context.Run, "EXPR_INVALID", head.Span, "application head is not callable")
	}
	function := callee.Value.Callable.(*Function)
	result := p.call(function, expression.Items[1:], context, expression.Span)
	freeCallables(context.Run, &callee.Value)
	return result
}

func (p *Program) operation(scope *Scope, operation *Operation, arguments []*expr.Expr, context *Context, span source.Span) Result {
	_, _ = p, scope
	// A dependency may publish while a host operation is outstanding. Keep its
	// single request active until the engine supplies the matching completion;
	// the resumed evaluation then snapshots every dependency at its latest value.
	if context.Engine != nil && context.Engine.Submitted() && context.Completion().RequestID == 0 {
		return Result{Waiting: true}
	}
	if len(arguments) < operation.MinArity || (operation.MaxArity >= 0 && len(arguments) > operation.MaxArity) {
		return failure(context.Run, "EXPR_INVALID", span, "invalid operation arity")
	}
	if context.OperationObserver != nil {
		context.OperationObserver(context.ResolverState, operation.Name, operation.Version)
	}
	for i := range operation.Capabilities {
		if !context.allowed(operation.Capabilities[i]) {
			return failure(context.Run, "CAP_DENIED", span, "operation capability denied")
		}
	}
	values := slices.Make[core.Value](context.Run, len(arguments))
	for i := range arguments {
		r := p.evaluate(context.Engine, context.Scope, arguments[i], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			freeValues(context.Run, values)
			return r
		}
		values[i] = r.Value
	}
	previousCapabilities, previousSpan, previousStart, previousEnd := context.activeCapabilities, context.Span, context.operationStart, context.operationEnd
	context.denied, context.activeCapabilities, context.Span = false, operation.Capabilities, span
	context.operationStart, context.operationEnd = span.Start, span.End
	result := operation.Call(context, operation.Context, values)
	context.activeCapabilities, context.Span, context.operationStart, context.operationEnd = previousCapabilities, previousSpan, previousStart, previousEnd
	freeValues(context.Run, values)
	if context.denied {
		result.Free(context.Run)
		return failure(context.Run, "CAP_DENIED", span, "operation capability denied")
	}
	return result
}

func (p *Program) call(function *Function, arguments []*expr.Expr, context *Context, span source.Span) Result {
	values := slices.Make[core.Value](context.Run, len(arguments))
	defer freeValues(context.Run, values)
	for i := range arguments {
		r := p.evaluate(context.Engine, context.Scope, arguments[i], context)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		values[i] = r.Value
	}
	return p.callValues(function, values, context, span)
}

func (p *Program) callValues(function *Function, values []core.Value, context *Context, span source.Span) Result {
	fixed := len(function.Parameters)
	rest := fixed != 0 && function.Parameters[fixed-1].Rest
	if rest {
		fixed--
	}
	if len(values) < fixed || (!rest && len(values) != fixed) {
		return failure(context.Run, "EXPR_INVALID", span, "invalid function arity")
	}
	child := newScope(context.Run, function.Scope)
	defer child.Free()
	for i := 0; i < fixed; i++ {
		child.setValue(function.Parameters[i].Name, values[i])
	}
	if rest {
		remainder := core.NewList(context.Run, values[fixed:])
		child.setValue(function.Parameters[fixed].Name, remainder)
		remainder.Free(context.Run)
	}
	previousArgs, previousScope, previousHasArgs := context.Args, context.Scope, context.HasArgs
	context.Args, context.HasArgs = values, true
	var result Result
	if function.Definition != nil {
		result = p.definitionValue(context.Engine, function.Definition, child, context)
	} else if function.Expression != nil {
		result = p.evaluate(context.Engine, child, function.Expression, context)
	} else {
		result = p.body(child, function.Body, context)
	}
	context.Args, context.Scope, context.HasArgs = previousArgs, previousScope, previousHasArgs
	attachFrame(&result, context, span, "call")
	return result
}

func attachFrame(result *Result, context *Context, span source.Span, label string) {
	if result.Diagnostic.Code == "" {
		return
	}
	a := context.Run
	if a == nil {
		a = mem.System
	}
	frameLabel := label
	// An owned diagnostic releases every frame label. Static frame labels must
	// therefore be cloned before becoming part of its owned frame slice.
	if result.Diagnostic.Owned {
		frameLabel = owned(a, label)
	}
	result.Diagnostic.Frames = slices.Append(a, result.Diagnostic.Frames, diagnostic.Frame{Label: frameLabel, Span: diagnostic.Span{Start: span.Start, End: span.End}})
}
