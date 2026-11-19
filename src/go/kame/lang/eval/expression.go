package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
)

// Keep recursive evaluation within the native and WASM stack budgets.
const MaxEvaluationDepth = 32

func (p *Program) evaluate(engine *core.EngineContext, scope *Scope, expression *expr.Expr, context *Context) Result {
	expression = expr.Ungroup(expression)
	if context.evaluationDepth >= MaxEvaluationDepth {
		span := source.Span{}
		if expression != nil {
			span = expression.Span
		}
		return failure(context.Run, "EVAL_DEPTH", span, "evaluation depth limit exceeded")
	}
	context.evaluationDepth++
	previousEnv := context.ReserveEnv
	previousOrigin := context.SourceOrigin
	previousSource, previousEntry := context.Source, context.EntrySource
	previousEngine, previousScope, previousRequests := context.Engine, context.Scope, context.Requests
	context.Engine = engine
	if expression != nil {
		context.ReserveEnv = expression.Kash
		context.SourceOrigin = expression.SourceOrigin
		context.EntrySource = expression.EntrySource
		if expression.EntrySource != nil {
			context.Source = expression.EntrySource.Name
		} else if expression.SourceOrigin > 0 && p.Script != nil {
			context.Source = p.Script.Source.Name
		}
		context.observeExpressionSource(expression)
	}
	result := p.evaluateExpression(engine, scope, expression, context)
	if context.dependencyFailure.Code != "" {
		result.Free(context.Run)
		result = Result{Diagnostic: context.dependencyFailure}
		context.dependencyFailure = diagnostic.Diagnostic{}
	}
	if context.deferred {
		result.Free(context.Run)
		result = Result{Completed: true}
	}
	context.ReserveEnv = previousEnv
	context.Engine, context.Scope, context.Requests = previousEngine, previousScope, previousRequests
	context.evaluationDepth--
	attachSource(&result, context)
	context.SourceOrigin = previousOrigin
	context.Source, context.EntrySource = previousSource, previousEntry
	return result
}

func (p *Program) evaluateExpression(engine *core.EngineContext, scope *Scope, expression *expr.Expr, context *Context) Result {
	if expression == nil {
		return failure(context.Run, "EXPR_INVALID", source.Span{}, "definition needs an expression value")
	}
	if expression.ActionPrefix != "" {
		return failure(context.Run, "EXPR_INVALID", expression.Span, "action form does not yet support evaluation")
	}
	context.Engine, context.Scope, context.Requests = engine, scope, p.Requests
	switch expression.Kind {
	case expr.CommandStream:
		return Result{Value: core.Value{Kind: core.Nil}}
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
		return p.stringValue(scope, expression, context)
	case expr.Symbol:
		return Result{Value: core.NewString(context.Run, expression.Text)}
	case expr.Path:
		if expression.Pattern != nil && len(expression.Parts) == 0 {
			text := expr.CanonicalPattern(context.Run, expression.Pattern)
			result := Result{Value: core.NewPattern(context.Run, text)}
			mem.FreeString(context.Run, text)
			return result
		}
		return p.pathExpression(scope, expression, context)
	case expr.Name:
		if expression.Kash && expression.Text == "env" {
			return failure(context.Run, "REF_MISSING", expression.Span, "environment namespace requires a named variable")
		}
		return name(scope, expression.Text, expression.Span, context)
	case expr.InvocationJoin:
		r := p.joinProcesses(context)
		if r.Waiting || r.Diagnostic.Code != "" {
			return r
		}
		r.Free(context.Run)
		return p.finishDefers(context.Deferred, context, false)
	case expr.KashDefer:
		return p.registerDefer(expression, context)
	case expr.KashFunction:
		return p.nativeKash(expression, scope, context)
	case expr.KashReturn:
		return p.kashReturn(scope, expression, context)
	case expr.KashLet:
		return p.kashLet(scope, expression, context)
	case expr.KashExit:
		return p.kashExit(scope, expression, context)
	case expr.KashWith:
		return p.kashWith(scope, expression, context)
	case expr.KashBreak, expr.KashContinue:
		if context.Phase != EvaluatePhase || context.PurePredicate || context.loopDepth == 0 {
			return failure(context.Run, "PHASE_INVALID", expression.Span, "loop transfer requires an active local loop")
		}
		return Result{LoopTransfer: expression.Kind}
	case expr.Reference, expr.EnvironmentReference:
		return p.reference(scope, expression, context)
	case expr.List:
		return p.list(scope, expression, context)
	case expr.ListSpread:
		r := p.evaluate(engine, scope, expression.Items[0], context)
		if !r.Waiting && !r.Completed && r.Diagnostic.Code == "" && r.Value.Kind != core.List {
			r.Free(context.Run)
			return failure(context.Run, "EXPR_INVALID", expression.Items[0].Span, "list spread requires a list")
		}
		return r
	case expr.Record:
		return p.record(scope, expression.Fields, context)
	case expr.ValuePipe:
		return p.valuePipe(scope, expression, context)
	case expr.Application:
		return p.application(scope, expression, context)
	case expr.Conditional:
		return p.conditional(scope, expression.Items, context, expression.Span)
	case expr.CommandCapture, expr.CommandGraph, expr.CommandTest:
		return p.capture(expression, context)
	case expr.CommandList:
		return p.commandList(expression, context)
	case expr.KashIf, expr.KashMatch, expr.Match:
		return p.kashControl(scope, expression, context)
	case expr.KashFor, expr.KashWhile, expr.KashRecords:
		return p.kashLoop(scope, expression, context)
	case expr.ValueRecovery:
		return p.shortCircuitValue(scope, expression, context)
	case expr.Lambda:
		function := mem.Alloc[Function](context.Run)
		function.PayloadAlloc = context.Run
		if context.CloneCallableAST {
			function.AST = expr.Clone(context.Run, expression)
			expression = function.AST
		}
		function.Kind, function.Parameters, function.Body, function.Scope = FunctionTemporary, expression.Parameters, expression.Body, scope
		scope.Retain()
		return Result{Value: core.Value{Kind: core.Callable, Callable: function}}
	case expr.Section:
		function := mem.Alloc[Function](context.Run)
		function.PayloadAlloc = context.Run
		if context.CloneCallableAST {
			function.AST = expr.Clone(context.Run, expression)
			expression = function.AST
		}
		function.Kind, function.Parameters, function.Expression, function.Scope, function.Section = FunctionTemporary, expression.Parameters, expression.Body[0], scope, true
		scope.Retain()
		return Result{Value: core.Value{Kind: core.Callable, Callable: function}}
	case expr.Placeholder:
		return placeholder(scope, expression, context)
	case expr.Selector:
		result := p.selector(expression.Text, expression.Span, context)
		if expression.Expansion == "selector" {
			return literalSelectorTip(context.Run, result)
		}
		return result
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
			return Result{Value: core.Value{Kind: core.Nil}}
		}
		return Result{Value: borrowValue(context.Run, current.Section[index])}
	}
	return failure(context.Run, "EXPR_INVALID", expression.Span, "placeholder outside section")
}

func name(scope *Scope, name string, span source.Span, context *Context) Result {
	if name == pipeInputName {
		for current := scope; current != nil; current = current.Parent {
			if current.pipeInput != nil {
				return context.Program.pipeInputValue(current, context)
			}
		}
		return failure(context.Run, "EXPR_INVALID", span, "pipe input is unavailable")
	}
	if len(name) > 2 && name[:2] == "x/" {
		return context.TrackedTool(name[2:], span)
	}
	b := scope.lookup(name)
	if b == nil {
		if name == "inputs" {
			return context.Program.selector("@<*", span, context)
		}
		if name == "outputs" {
			return context.Program.selector("@>*", span, context)
		}
		if name == "inputs/changed" {
			return context.Program.selector("@<?", span, context)
		}
		if name == "args" {
			return context.Program.selector("@*", span, context)
		}
		if name == "kash" && context.Program != nil {
			return Result{Value: context.Program.KashConstructor()}
		}
		if name == "kame" && context.Program != nil {
			return Result{Value: context.Program.KameSelector()}
		}
		if IsSpecialForm(name) {
			return failure(context.Run, "EXPR_INVALID", span, "special form is not a callable value: "+name)
		}
		if context.Program != nil {
			if operation := context.Program.Registry.lookup(name); operation != nil {
				return Result{Value: context.Program.newOperationCallable(operation.Name)}
			}
		}
		return missingOperation(context, name, span, true)
	}
	if b.Kind == bindingValue {
		return Result{Value: borrowValue(context.Run, b.Value)}
	}
	if b.Kind == bindingEager {
		if !b.Initialized {
			if context.Program.DryRun {
				context.deferred = true
				return Result{Completed: true}
			}
			r := failure(context.Run, "BINDING_UNINITIALIZED", span, "eager binding read before its initializer was reached")
			r.Diagnostic.Protected = true
			return r
		}
		return Result{Value: borrowValue(context.Run, b.Value)}
	}
	if b.Kind == bindingFunction {
		context.observeDefinitionSource(b.Function.Definition)
		// A definition can be returned from its defining let scope. Keep
		// that scope alive until the returned value is consumed or rejected.
		wrapper := borrowFunction(context.Run, b.Function)
		return Result{Value: core.Value{Kind: core.Callable, Callable: wrapper}}
	}
	if b.Kind == bindingLocalDefinition {
		context.observeExpressionSource(b.Local.Expression)
		return context.Program.localValue(b.Local, context)
	}
	if context.PurePredicate {
		return context.Program.EvaluateDefinition(b.Definition, context)
	}
	if context.Engine == nil && context.ResolveDefinition != nil {
		return context.ResolveDefinition(context.ResolverState, b.Definition, context)
	}
	key := b.Definition
	if context.Engine != nil && (context.HasDefinitionNamespace || context.needsFinalizationView()) {
		node := context.Program.scopedDefinition(key, context)
		if node != nil {
			key = node.Key
		}
	}
	ready := false
	if context.Engine != nil {
		context.Program.bindDefinitionInput(key, context)
		if context.RecoverFailures || context.Program.DryRun {
			// Dry-run recipes must receive a lazy definition's deferred-read
			// diagnostic before the engine propagates it as a target failure.
			ready = context.Engine.TryDependency(key)
			d := context.Engine.DependencyDiagnostic(key)
			if d.Code != "" {
				return Result{Diagnostic: d.Clone(context.Run)}
			}
		} else {
			ready = context.Engine.Dependency(key)
		}
	}
	if !ready {
		if context.Engine != nil && context.Engine.Failed() {
			d := context.Engine.Diagnostic()
			return Result{Diagnostic: d.Clone(context.Run)}
		}
		return Result{Waiting: true}
	}
	value := context.Engine.Value(key)
	if !value.OK {
		return Result{Waiting: true}
	}
	return Result{Value: value.Value.Clone(context.Run)}
}
