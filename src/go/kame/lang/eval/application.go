// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

func (p *Program) application(scope *Scope, expression *expr.Expr, context *Context) Result {
	if len(expression.Items) == 0 {
		return failure(context.Run, "EXPR_INVALID", expression.Span, "empty application")
	}
	head := expression.Items[0]
	if head.Kind == expr.Name {
		if head.Text == "await" { return p.awaitProcess(expression, context) }
		if head.Text == "run!" || head.Text == "pipe!" {
			return p.processExpression(expression, context)
		}
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
		if head.Text == "if" {
			return p.conditional(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "and" {
			return p.logicalAnd(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "or" {
			return p.logicalOr(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "with" {
			return p.scopedWith(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "match" {
			return p.patternMatch(scope, expression.Items[1:], context, expression.Span)
		}
		// Comparison aliases parse as Name atoms; canonicalize before lookup.
		if aliased, ok := comparisonAlias(head.Text); ok {
			if p.Registry != nil {
				operation := p.Registry.lookup(aliased)
				if operation != nil {
					result := p.operation(scope, operation, expression.Items[1:], context, expression.Span)
					attachNamedFrame(&result, context, expression.Span, "operation", head.Text)
					return result
				}
			}
			return missingOperation(context, head.Text, head.Span, false)
		}
	}
	callee := p.evaluate(context.Engine, scope, head, context)
	if callee.Waiting || callee.Diagnostic.Code != "" {
		if callee.Diagnostic.Code == "REF_MISSING" && head.Kind == expr.Name && scope.lookup(head.Text) == nil && p.Registry != nil {
			// Kash reserves bare env for its namespace, but (env NAME) remains
			// an ordinary operation call as before.
			operation := p.Registry.lookup(head.Text)
			callee.Diagnostic.Free(context.Run)
			if operation != nil {
				result := p.operation(scope, operation, expression.Items[1:], context, expression.Span)
				attachNamedFrame(&result, context, expression.Span, "operation", head.Text)
				return result
			}
			return missingOperation(context, head.Text, head.Span, false)
		}
		return callee
	}
	if callee.Value.Kind != core.Callable {
		freeCallables(context.Run, &callee.Value)
		callee.Value.Free(context.Run)
		return failure(context.Run, "EXPR_INVALID", head.Span, "application head is not callable")
	}
	function := callee.Value.Callable.(*Function)
	context.Scope = scope
	result := p.call(function, expression.Items[1:], context, expression.Span)
	freeCallables(context.Run, &callee.Value)
	return result
}

// comparisonAlias maps symbolic comparison atoms to canonical operation names.
func comparisonAlias(name string) (string, bool) {
	switch name {
	case "=":
		return "eq", true
	case "==":
		return "is", true
	case "!=":
		return "ne", true
	case "<":
		return "lt", true
	case ">":
		return "gt", true
	case "<=":
		return "lte", true
	case ">=":
		return "gte", true
	}
	return "", false
}

func (p *Program) operation(scope *Scope, operation *Operation, arguments []*expr.Expr, context *Context, span source.Span) Result {
	ready := p.prepareOperation(operation, len(arguments), context, span)
	if ready.Waiting || ready.Diagnostic.Code != "" { return ready }
	if operation.Preflight != nil {
		previousName, previousSpan, previousArguments := context.OperationName, context.Span, context.operationArguments
		context.OperationName, context.Span, context.operationArguments = operation.Name, span, arguments
		checked := operation.Preflight(context, arguments)
		context.OperationName, context.Span, context.operationArguments = previousName, previousSpan, previousArguments
		if checked.Waiting || checked.Diagnostic.Code != "" { return checked }
	}
	operands := p.argumentValuesMode(scope, arguments, context, span, operation.DeferredReplacement)
	if operands.Result.Waiting || operands.Result.Diagnostic.Code != "" { return operands.Result }
	if operation.DeferredReplacement && operands.Values[0].Kind == core.Pattern {
		function := mem.Alloc[Function](context.Run)
		function.Kind, function.Scope, function.Expression = FunctionTemporary, scope, expr.Clone(context.Run, arguments[1])
		function.ExpressionOwned = true
		function.DeferredReplacement = true
		scope.Retain()
		operands.Values[1] = core.Value{Kind: core.Callable, Callable: function}
	}
	context.Scope = scope
	result := p.invokeOperation(operation, operands.Values, arguments, context, span)
	// Operations release consumed callable wrappers; release only value storage.
	freeValues(context.Run, operands.Values)
	return result
}

// EvaluateReplacement evaluates a retained operand in a match-local scope.
func (c *Context) EvaluateReplacement(value core.Value, captures core.Value, parent *Scope) Result {
	if c.ReserveEnv {
		for i := range captures.Record { if captures.Record[i].Key == "env" { return failure(c.Run, "DEF_INVALID", c.Span, "env is reserved in Kash") } }
	}
	function := value.Callable.(*Function)
	child := RenderChildScope(c, parent, captures)
	if child == nil { return failure(c.Run, "EXPR_INVALID", c.Span, "invalid capture scope") }
	result := c.Program.evaluate(c.Engine, child, function.Expression, c)
	child.Free()
	return result
}

// ReleaseCallable releases a retained operand using its owning allocator.
func ReleaseCallable(a mem.Allocator, value *core.Value) { freeCallables(a, value) }

func (p *Program) prepareOperation(operation *Operation, count int, context *Context, span source.Span) Result {
	_ = p
	// A dependency may publish while a host operation is outstanding. Keep its
	// single request active until the engine supplies the matching completion;
	// the resumed evaluation then snapshots every dependency at its latest value.
	if context.Engine != nil && context.Engine.Submitted() && context.Completion().RequestID == 0 {
		return Result{Waiting: true}
	}
	if count < operation.MinArity || (operation.MaxArity >= 0 && count > operation.MaxArity) {
		return arityFailure(context.Run, span, operation.Name, count, operation.MinArity, operation.MaxArity)
	}
	if context.OperationObserver != nil {
		context.OperationObserver(context.ResolverState, operation.Name, operation.Version)
	}
	if context.Engine != nil {
		context.Engine.Observe(core.ResourceKey{Kind: core.ResourceOperation, Name: operation.Name}, core.ValueSignature(core.Value{Kind: core.String, Text: operation.Version}))
	}
	for i := range operation.Capabilities {
		if !context.allowed(operation.Capabilities[i]) {
			message := "`"+operation.Name+"` requires a capability that was not granted"
			if operation.Capabilities[i] == Read {
				message += "; grant read access with --allow-read=ROOT"
			}
			return failure(context.Run, "CAP_DENIED", span, message)
		}
	}
	return Result{}
}

func (p *Program) invokeOperation(operation *Operation, values []core.Value, arguments []*expr.Expr, context *Context, span source.Span) Result {
	_ = p
	previousCapabilities, previousSpan, previousStart, previousEnd := context.activeCapabilities, context.Span, context.operationStart, context.operationEnd
	previousName, previousArguments := context.OperationName, context.operationArguments
	context.OperationName, context.operationArguments = operation.Name, arguments
	context.denied, context.activeCapabilities, context.Span = false, operation.Capabilities, span
	context.operationStart, context.operationEnd = span.Start, span.End
	result := operation.Call(context, operation.Context, values)
	if result.Diagnostic.Code != "" && !result.Diagnostic.Owned {
		owned := result.Diagnostic.Clone(context.Run)
		result.Diagnostic.Free(context.Run)
		result.Diagnostic = owned
	}
	// Legacy/custom operations can return a source-neutral diagnostic. Supply
	// the call site only when no authored primary location has been established.
	if result.Diagnostic.Code != "" && result.Diagnostic.Source == "" {
		if result.Diagnostic.Span.Start == 0 && result.Diagnostic.Span.End == 0 {
			result.Diagnostic.Span = diagnostic.Span{Start: span.Start, End: span.End}
		}
		attachSource(&result, context)
	}
	context.OperationName, context.operationArguments = previousName, previousArguments
	context.activeCapabilities, context.Span, context.operationStart, context.operationEnd = previousCapabilities, previousSpan, previousStart, previousEnd
	if context.denied {
		result.Free(context.Run)
		return failure(context.Run, "CAP_DENIED", span, "`"+operation.Name+"` requires a capability that was not granted")
	}
	return result
}

func (p *Program) call(function *Function, arguments []*expr.Expr, context *Context, span source.Span) Result {
	if function.Kind == FunctionOperation {
		operation := p.operationForCallable(function)
		if operation == nil { return failure(context.Run, "EXPR_INVALID", span, "operation callable unavailable or belongs to another invocation") }
		result := p.operation(context.Scope, operation, arguments, context, span)
		attachNamedFrame(&result, context, span, "operation", function.OperationName)
		return result
	}
	scope := context.Scope
	operands := p.argumentValues(scope, arguments, context, span)
	if operands.Result.Waiting || operands.Result.Diagnostic.Code != "" { return operands.Result }
	values := operands.Values
	// Argument expressions may enter temporary scopes. Native constructors
	// capture the application scope, which must still be alive after arguments.
	context.Scope = scope
	result := p.callValues(function, values, context, span)
	// Transfer: callValues cloned arguments into the child scope; release
	// only storage here so shared callables are freed once by the child.
	freeValues(context.Run, values)
	return result
}

func (p *Program) callValues(function *Function, values []core.Value, context *Context, span source.Span) Result {
	if function.Kind == FunctionOperation {
		operation := p.operationForCallable(function)
		if operation == nil { return failure(context.Run, "EXPR_INVALID", span, "operation callable unavailable or belongs to another invocation") }
		result := p.prepareOperation(operation, len(values), context, span)
		if !result.Waiting && result.Diagnostic.Code == "" { result = p.invokeOperation(operation, values, nil, context, span) }
		attachNamedFrame(&result, context, span, "operation", function.OperationName)
		return result
	}
	if function.Kind == FunctionInvocation {
		if function.Owner != p { return failure(context.Run, "EXPR_INVALID", span, "Kash callable belongs to another invocation") }
		previousStart, previousEnd := context.operationStart, context.operationEnd
		context.operationStart, context.operationEnd = span.Start, span.End
		var result Result
		if function.KashConstructor { result = p.constructKash(values, context, span) } else { result = p.invokeKash(function.KashScript, function.Scope, values, context) }
		context.operationStart, context.operationEnd = previousStart, previousEnd
		return result
	}
	if function.NativeCall != nil {
		if len(values) != function.Arity {
			return arityFailure(context.Run, span, "function", len(values), function.Arity, function.Arity)
		}
		return function.NativeCall(context, function.Native, values)
	}
	fixed := len(function.Parameters)
	rest := fixed != 0 && function.Parameters[fixed-1].Rest
	if rest {
		fixed--
	}
	if len(values) < fixed || (!rest && len(values) != fixed) {
		name := "function"
		if function.Definition != nil {
			name = function.Definition.Name
		}
		max := fixed
		if rest {
			max = -1
		}
		return arityFailure(context.Run, span, name, len(values), fixed, max)
	}
	child := newScope(context.Run, function.Scope)
	defer child.Free()
	if function.Section {
		// The section scope owns clones of the arguments so a closure that
		// escapes the call keeps its arguments alive. The caller frees the
		// original values.
		owned := slices.Make[core.Value](context.Run, len(values))
		for i := range values {
			owned[i] = values[i].Clone(context.Run)
		}
		child.Section = owned
	} else {
		// No wouldStrandCapture check here: child is freshly created, so it cannot
		// be an ancestor of any capture scope and the store cannot deadlock.
		for i := 0; i < fixed; i++ {
			child.setValue(function.Parameters[i].Name, values[i])
		}
		if rest {
			remainder := core.NewList(context.Run, values[fixed:])
			child.setValue(function.Parameters[fixed].Name, remainder)
			remainder.Free(context.Run)
		}
	}
	previousArgs, previousScope, previousHasArgs := context.Args, context.Scope, context.HasArgs
	previousCallPath := context.CallPath
	context.CallPath = invocationPath(context.Run, previousCallPath, span.Start, span.End)
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
	mem.FreeString(context.Run, context.CallPath)
	context.CallPath = previousCallPath
	label := "function"
	if function.Definition != nil {
		label = function.Definition.Name
	}
	attachNamedFrame(&result, context, span, "call", label)
	return result
}

func attachNamedFrame(result *Result, context *Context, span source.Span, kind string, label string) {
	if result.Diagnostic.Code == "" {
		return
	}
	a := context.Run
	if a == nil {
		a = mem.System
	}
	location := context.Program.LocateSource(context.Source, span)
	if context.Source != "" && location.Source == "" {
		return
	}
	span = location.Span
	frameLabel, frameKind, frameSource := label, kind, location.Source
	// An owned diagnostic releases every frame label. Static frame labels must
	// therefore be cloned before becoming part of its owned frame slice.
	if result.Diagnostic.Owned {
		frameLabel = owned(a, label)
		frameKind = owned(a, kind)
		frameSource = owned(a, location.Source)
	}
	for i := range result.Diagnostic.Frames {
		frame := result.Diagnostic.Frames[i]
		if frame.Kind == frameKind && frame.Label == frameLabel && frame.Source == frameSource && frame.Span.Start == span.Start && frame.Span.End == span.End {
			if result.Diagnostic.Owned {
				mem.FreeString(a, frameLabel)
				mem.FreeString(a, frameKind)
				mem.FreeString(a, frameSource)
			}
			return
		}
	}
	result.Diagnostic.Frames = slices.Append(a, result.Diagnostic.Frames, diagnostic.Frame{Kind: frameKind, Label: frameLabel, Source: frameSource, Span: diagnostic.Span{Start: span.Start, End: span.End}})
}

func arityFailure(a mem.Allocator, span source.Span, name string, count int, min int, max int) Result {
	var countBuffer, minBuffer, maxBuffer [strconv.MaxIntBase10Len]byte
	got := strconv.FormatInt(countBuffer[:], int64(count), 10)
	expected := strconv.FormatInt(minBuffer[:], int64(min), 10)
	if max < 0 {
		expected = "at least " + expected
	} else if max != min {
		expected += " to " + strconv.FormatInt(maxBuffer[:], int64(max), 10)
	}
	unit := "arguments"
	if min == 1 && (max == 1 || max < 0) {
		unit = "argument"
	}
	return failure(a, "EXPR_INVALID", span, "`"+name+"` expects "+expected+" "+unit+"; got "+got)
}
