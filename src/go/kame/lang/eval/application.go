package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
	"slices"

	"strconv"
	"strings"
)

type functionCallState struct {
	Head kashControlState
}

func freeFunctionCallState(a mem.Allocator, value any) {
	state := value.(*functionCallState)
	freeCallables(a, &state.Head.Value)
	freeKashControlState(a, &state.Head)
	mem.Free(a, state)
}

func (p *Program) application(scope *Scope, expression *expr.Expr, context *Context) Result {
	if len(expression.Items) == 0 {
		return failure(context.Run, "EXPR_INVALID", expression.Span, "empty application")
	}
	switch expression.Infix {
	case "??", "and", "or":
		return p.shortCircuitValue(scope, expression, context)
	}
	head := expression.Items[0]
	if context.PurePredicate && head.Kind == expr.Name && (head.Text == "run!" || head.Text == "capture!" || head.Text == "pipe!" || head.Text == "await") {
		return failure(context.Run, "KASH_PREDICATE_EFFECT", expression.Span, "stream predicates require pure, nonblocking computations")
	}
	if head.Kind == expr.Name {
		if head.Text == "await" {
			return p.awaitProcess(expression, context)
		}
		if head.Text == "run!" || head.Text == "capture!" || head.Text == "pipe!" {
			return p.processExpression(expression, context)
		}
		if head.Text == "let" {
			return p.let(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "if" {
			return p.conditional(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "with" {
			return p.scopedWith(scope, expression.Items[1:], context, expression.Span)
		}
		if head.Text == "match" {
			return p.patternMatch(scope, expression.Items[1:], context, expression.Span)
		}
		if aliased, ok := comparisonOperation(expression.Infix); ok {
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
	var retained *functionCallState
	if p.Scope.replaceBindings && context.Engine != nil {
		start, end := context.operationStart, context.operationEnd
		context.operationStart, context.operationEnd = expression.Span.Start, expression.Span.End
		retained, _ = context.OperationState().(*functionCallState)
		context.operationStart, context.operationEnd = start, end
	}
	before, beforePaths := len(context.Effects), len(context.WritePaths)
	var callee Result
	if retained != nil {
		callee.Value = borrowValue(context.Run, retained.Head.Value)
		replayControlEffects(&retained.Head, context)
	} else {
		callee = p.evaluate(context.Engine, scope, head, context)
	}
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
		kind := callee.Value.Kind
		freeCallables(context.Run, &callee.Value)
		callee.Value.Free(context.Run)
		return failure(context.Run, "EXPR_INVALID", head.Span, "application head is "+core.KindName(kind)+", not callable")
	}
	function := callee.Value.Callable.(*Function)
	context.Scope = scope
	if retained == nil && p.Scope.replaceBindings && context.Engine != nil &&
		function.Kind != FunctionOperation && function.Kind != FunctionInterpreter && function.NativeCall == nil {
		retained = mem.Alloc[functionCallState](p.Alloc)
		retained.Head.Value = borrowValue(p.Alloc, callee.Value)
		for _, effect := range context.Effects[before:] {
			effect.Data = slices.Clone(effect.Data)
			retained.Head.Effects = append(retained.Head.Effects, effect)
		}
		for _, name := range context.WritePaths[beforePaths:] {
			retained.Head.WritePaths = append(retained.Head.WritePaths, owned(p.Alloc, name))
		}
		start, end := context.operationStart, context.operationEnd
		context.operationStart, context.operationEnd = expression.Span.Start, expression.Span.End
		context.SetOperationState(retained, freeFunctionCallState)
		context.operationStart, context.operationEnd = start, end
	}
	if retained != nil {
		function = retained.Head.Value.Callable.(*Function)
	}
	result := p.call(function, expression.Items[1:], context, expression.Span)
	if retained != nil && !result.Waiting {
		start, end := context.operationStart, context.operationEnd
		context.operationStart, context.operationEnd = expression.Span.Start, expression.Span.End
		context.ClearOperationState()
		context.operationStart, context.operationEnd = start, end
	}
	freeCallables(context.Run, &callee.Value)
	return result
}

// comparisonOperation maps infix comparisons to their library contracts.
func comparisonOperation(operator string) (string, bool) {
	switch operator {
	case "==":
		return "eq?", true
	case "!=":
		return "ne?", true
	case "<":
		return "lt?", true
	case ">":
		return "gt?", true
	case "<=":
		return "lte?", true
	case ">=":
		return "gte?", true
	}
	return "", false
}

func hasArgumentSpread(arguments []*expr.Expr) bool {
	return slices.ContainsFunc(arguments, func(argument *expr.Expr) bool {
		return argument.Kind == expr.ListSpread
	})
}

// expandArguments gives the call independent owners while keeping the authored
// operands available for suspension and diagnostics.
func expandArguments(a mem.Allocator, arguments []*expr.Expr, values []core.Value) ([]*expr.Expr, []core.Value) {
	var origins []*expr.Expr
	var expanded []core.Value
	for i, argument := range arguments {
		if argument.Kind == expr.ListSpread {
			for _, value := range values[i].List {
				expanded = append(expanded, borrowValue(a, value))
				origins = append(origins, argument.Items[0])
			}
		} else {
			expanded = append(expanded, borrowValue(a, values[i]))
			origins = append(origins, argument)
		}
	}
	return origins, expanded
}

func (p *Program) operation(scope *Scope, operation *Operation, arguments []*expr.Expr, context *Context, span source.Span) Result {
	if context.PurePredicate && operation.Category != PureOperation {
		return failure(context.Run, "KASH_PREDICATE_EFFECT", span, "stream predicates cannot call effectful or unclassified operations")
	}
	if operation.ScopedFinalizer {
		if context.forbidsFinalization() {
			return failure(context.Run, "PHASE_INVALID", span, "finally! is invalid in this execution phase")
		}
		if state := p.findFinalizer(context, span); state != nil {
			return p.advanceFinalizer(state, context)
		}
	}
	spread := hasArgumentSpread(arguments)
	count := len(arguments)
	if spread {
		count = -1 // Arity depends on the evaluated spread operands.
	}
	ready := p.prepareOperation(operation, count, context, span)
	if ready.Waiting || ready.Completed || ready.Diagnostic.Code != "" {
		return ready
	}
	if operation.Preflight != nil {
		previousName, previousSpan, previousArguments := context.OperationName, context.Span, context.operationArguments
		context.OperationName, context.Span, context.operationArguments = operation.Name, span, arguments
		checked := operation.Preflight(context, arguments)
		context.OperationName, context.Span, context.operationArguments = previousName, previousSpan, previousArguments
		if checked.Waiting || checked.Diagnostic.Code != "" {
			return checked
		}
	}
	effects, paths := len(context.Effects), len(context.WritePaths)
	operands := p.argumentValuesMode(scope, arguments, nil, context, span, operation.DeferredReplacement, operation.Name)
	if operands.Result.Waiting || operands.Result.Completed || operands.Result.Diagnostic.Code != "" {
		return operands.Result
	}
	callArguments, callValues := arguments, operands.Values
	if spread {
		callArguments, callValues = expandArguments(context.Run, arguments, operands.Values)
		defer mem.FreeSlice(nil, callArguments)
		count := len(callValues)
		if count < operation.MinArity || (operation.MaxArity >= 0 && count > operation.MaxArity) {
			freeValuesWithCallables(context.Run, callValues)
			freeValuesWithCallables(context.Run, operands.Values)
			return arityFailure(context.Run, span, operation.Name, count, operation.MinArity, operation.MaxArity)
		}
	}
	if i := operands.ReplacementIndex; i >= 0 {
		function := mem.Alloc[Function](context.Run)
		function.PayloadAlloc = context.Run
		function.Kind, function.Scope, function.Expression = FunctionTemporary, scope, expr.Clone(context.Run, arguments[i])
		function.ExpressionOwned = true
		function.DeferredReplacement = true
		scope.Retain()
		freeCallables(context.Run, &callValues[1])
		callValues[1] = core.Value{Kind: core.Callable, Callable: function}
	}
	context.Scope = scope
	completedEffects, completedPaths := len(context.Effects), len(context.WritePaths)
	// Operations consume callable wrappers; suspended operands retain independent owners.
	if spread {
		result := p.invokeOperation(operation, callValues, callArguments, context, span)
		freeValues(context.Run, callValues)
		if result.Waiting && p.retainArguments(scope, context, span, operation.Name, operands.Values, len(arguments), operands.Facts, effects, completedEffects, paths, completedPaths) {
			return result
		}
		freeValuesWithCallables(context.Run, operands.Values)
		return result
	}
	var retained []core.Value
	if context.sequencedOperand {
		for i := range operands.Values {
			if operands.Values[i].HasCallable() {
				retained = operands.Values
				operands.Values = make([]core.Value, len(retained))
				for j := range retained {
					operands.Values[j] = borrowValue(context.Run, retained[j])
				}
				break
			}
		}
	}
	result := p.invokeOperation(operation, operands.Values, arguments, context, span)
	if retained != nil {
		freeValues(context.Run, operands.Values)
		operands.Values = retained
	}
	// A suspended operation owns its progress, but its completed operands also
	// need to survive: rebuilding a large glob list on every lstat is quadratic.
	if result.Waiting && p.retainArguments(scope, context, span, operation.Name, operands.Values, len(arguments), operands.Facts, effects, completedEffects, paths, completedPaths) {
		return result
	}
	// Operations release consumed callable wrappers; release only value storage.
	if retained != nil {
		freeValuesWithCallables(context.Run, operands.Values)
	} else {
		freeValues(context.Run, operands.Values)
	}
	return result
}

// EvaluateReplacement evaluates a retained operand in a match-local scope.
func (c *Context) EvaluateReplacement(value core.Value, captures core.Value, parent *Scope) Result {
	function := value.Callable.(*Function)
	child := RenderChildScope(c, parent, captures)
	if child == nil {
		return failure(c.Run, "EXPR_INVALID", c.Span, "invalid capture scope")
	}
	previousClone := c.CloneCallableAST
	c.CloneCallableAST = true
	result := c.Program.evaluate(c.Engine, child, function.Expression, c)
	c.CloneCallableAST = previousClone
	child.Free()
	if result.Diagnostic.Code == "REF_MISSING" && function.Expression.Kind == expr.Path {
		for i := range function.Expression.Parts {
			part := function.Expression.Parts[i]
			if part.Form == "{" && part.Expr != nil && part.Expr.Kind == expr.Name &&
				result.Diagnostic.Span.Start >= part.Span.Start && result.Diagnostic.Span.Start < part.Span.End {
				span := source.Span{Start: result.Diagnostic.Span.Start, End: result.Diagnostic.Span.End}
				result.Free(c.Run)
				return failure(c.Run, "PAT_INVALID", span, "pattern reference is missing")
			}
		}
	}
	return result
}

// ReleaseCallable releases a retained operand using its owning allocator.
func ReleaseCallable(a mem.Allocator, value *core.Value) { freeCallables(a, value) }

func (p *Program) prepareOperation(operation *Operation, count int, context *Context, span source.Span) Result {
	_ = p
	failureOnly := operation.Category == ControlOperation && operation.Behavior == RaisesFailure
	trackedDeclarationRead := operation.Name == "files" || operation.Name == "dirs" || operation.Name == "wildcard" || operation.Name == "env"
	if context.DeclarationFiles != nil && context.Phase == PlanningPhase && operation.HasContract() && operation.Category != PureOperation && !failureOnly && !trackedDeclarationRead {
		context.MarkPhaseInvalid()
		if context.SourceSelection && len(operation.Capabilities) != 0 {
			return failure(context.Run, "CAP_DENIED", span, "operation is outside source-selection authority")
		}
		return failure(context.Run, "PHASE_INVALID", span, "`"+operation.Name+"` is invalid during declaration registration")
	}
	if context.Phase == RenderingPhase && (operation.Category == OutputOperation || operation.Category == ProcessOperation || operation.Category == VolatileOperation) {
		context.MarkPhaseInvalid()
		return failure(context.Run, "PHASE_INVALID", span, "`"+operation.Name+"` is invalid in a value-producing rule body or shell-template expansion; use an action block to perform effects")
	}
	if context.PurePredicate && operation.Category != PureOperation {
		return failure(context.Run, "KASH_PREDICATE_EFFECT", span, "stream predicates cannot call effectful or unclassified operations")
	}
	if context.pureValueBlock && operation.HasContract() && !operation.AllowsPhase(context.Phase) {
		return failure(context.Run, "PHASE_INVALID", span, "`"+operation.Name+"` is invalid in this value block's phase")
	}
	if context.deferred {
		return Result{Completed: true}
	}
	// A dependency may publish while a host operation is outstanding. Keep its
	// single request active until the engine supplies the matching completion;
	// the resumed evaluation then snapshots every dependency at its latest value.
	if context.Engine != nil && context.Engine.Submitted() && context.Completion().RequestID == 0 {
		return Result{Waiting: true}
	}
	if count >= 0 && (count < operation.MinArity || (operation.MaxArity >= 0 && count > operation.MaxArity)) {
		return arityFailure(context.Run, span, operation.Name, count, operation.MinArity, operation.MaxArity)
	}
	if context.OperationObserver != nil {
		context.OperationObserver(context.ResolverState, operation.Name, operation.Version)
	}
	if context.Engine != nil {
		context.Engine.Observe(core.ResourceKey{Kind: core.ResourceOperation, Name: operation.Name}, operation.VersionSignature())
	}
	for i := range operation.Capabilities {
		if !context.allowed(operation.Capabilities[i]) {
			if operation.Capabilities[i] == Env {
				return environmentDenied(context.Run, span, "")
			}
			message := "`" + operation.Name + "` requires a capability that was not granted"
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
	// Registered operations can return a source-neutral diagnostic. Supply
	// the call site only when no authored primary location has been established.
	if result.Diagnostic.Code != "" && result.Diagnostic.Source == "" {
		if result.Diagnostic.Span.Start == 0 && result.Diagnostic.Span.End == 0 {
			result.Diagnostic.Span = diagnostic.Span{Start: span.Start, End: span.End}
		}
		attachSource(&result, context)
	}
	context.OperationName, context.operationArguments = previousName, previousArguments
	context.activeCapabilities, context.Span, context.operationStart, context.operationEnd = previousCapabilities, previousSpan, previousStart, previousEnd
	if context.Phase == RenderingPhase && context.phaseInvalid {
		result.Free(context.Run)
		return failure(context.Run, "PHASE_INVALID", span, "rule body computation and shell-template expansion cannot perform effects")
	}
	if context.denied {
		result.Free(context.Run)
		return failure(context.Run, "CAP_DENIED", span, "`"+operation.Name+"` requires a capability that was not granted")
	}
	return result
}

func (p *Program) call(function *Function, arguments []*expr.Expr, context *Context, span source.Span) Result {
	if function.Kind == FunctionInterpreter {
		return p.callValues(function, nil, context, span)
	}
	if function.Kind == FunctionOperation {
		operation := p.operationForCallable(function)
		if operation == nil {
			return failure(context.Run, "EXPR_INVALID", span, "operation callable unavailable or belongs to another invocation")
		}
		result := p.operation(context.Scope, operation, arguments, context, span)
		attachNamedFrame(&result, context, span, "operation", function.OperationName)
		return result
	}
	context.observeDefinitionSource(function.Definition)
	scope := context.Scope
	operands := p.argumentValues(scope, arguments, context, span)
	if operands.Result.Waiting || operands.Result.Completed || operands.Result.Diagnostic.Code != "" {
		return operands.Result
	}
	values := operands.Values
	if hasArgumentSpread(arguments) {
		origins, expanded := expandArguments(context.Run, arguments, values)
		mem.FreeSlice(nil, origins)
		freeValuesWithCallables(context.Run, values)
		values = expanded
	}
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
	context.observeDefinitionSource(function.Definition)
	if function.Kind == FunctionOperation {
		operation := p.operationForCallable(function)
		if operation == nil {
			discardCallValues(context.Run, values)
			return failure(context.Run, "EXPR_INVALID", span, "operation callable unavailable or belongs to another invocation")
		}
		result := p.prepareOperation(operation, len(values), context, span)
		if !result.Waiting && !result.Completed && result.Diagnostic.Code == "" {
			result = p.invokeOperation(operation, values, nil, context, span)
		} else {
			discardCallValues(context.Run, values)
		}
		attachNamedFrame(&result, context, span, "operation", function.OperationName)
		return result
	}
	if function.Kind == FunctionInterpreter {
		discardCallValues(context.Run, values)
		return failure(context.Run, "EXPR_INVALID", span, "use a def block to author Kash actions")
	}
	if function.NativeCall != nil {
		if len(values) != function.Arity {
			discardCallValues(context.Run, values)
			return arityFailure(context.Run, span, "function", len(values), function.Arity, function.Arity)
		}
		return function.NativeCall(context, function.Native, values)
	}
	fixed := len(function.Parameters)
	rest := fixed != 0 && function.Parameters[fixed-1].Rest
	if rest {
		fixed--
	}
	if !rest && len(values) > fixed {
		discardCallValues(context.Run, values)
		name := "function"
		if function.Definition != nil {
			name = function.Definition.Name
		}
		return arityFailure(context.Run, functionAritySpan(function, context, span), name, len(values), fixed, fixed)
	}
	child := newScope(context.Run, function.Scope)
	child.sourceStorage = function.storage
	if child.sourceStorage != nil {
		child.sourceStorage.References++
	}
	defer child.Free()
	if function.Section {
		// The section scope owns clones of the arguments so a closure that
		// escapes the call keeps its arguments alive. The caller frees the
		// original values.
		owned := make([]core.Value, len(values))
		for i := range values {
			owned[i] = values[i].Clone(context.Run)
		}
		child.Section = owned
	} else {
		// No wouldStrandCapture check here: child is freshly created, so it cannot
		// be an ancestor of any capture scope and the store cannot deadlock.
		for i := 0; i < fixed; i++ {
			if i < len(values) {
				child.setValue(function.Parameters[i].Name, values[i])
			} else {
				// Missing arguments are dynamic: an unbound parameter is nil.
				child.setValue(function.Parameters[i].Name, core.Value{Kind: core.Nil})
			}
		}
		if rest {
			start := min(fixed, len(values))
			remainder := core.NewList(context.Run, values[start:])
			child.setValue(function.Parameters[fixed].Name, remainder)
			remainder.Free(context.Run)
		}
	}
	previousArgs, previousScope, previousHasArgs := context.Args, context.Scope, context.HasArgs
	previousClone := context.CloneCallableAST
	if function.AST != nil || function.ExpressionOwned {
		context.CloneCallableAST = true
	}
	previousRuleFrames := context.ruleCallFrames
	if context.TrackRuleCalls {
		label := "function"
		if function.Definition != nil {
			label = function.Definition.Name
		}
		location := context.Program.LocateSource(context.Source, span)
		if origin := context.SourceOrigin; origin > 0 && origin <= len(p.SourceParts) {
			part := p.SourceParts[origin-1]
			location = Location{Source: part.Name, Span: source.Span{Start: span.Start - part.Start + part.Offset, End: span.End - part.Start + part.Offset}}
		}
		context.ruleCallFrames = append(context.ruleCallFrames, diagnostic.Frame{Kind: "call", Label: label,
			Source: location.Source, Span: diagnostic.Span{Start: location.Span.Start, End: location.Span.End}})
	}
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
	context.ruleCallFrames = previousRuleFrames
	context.Args, context.Scope, context.HasArgs = previousArgs, previousScope, previousHasArgs
	context.CloneCallableAST = previousClone
	mem.FreeString(context.Run, context.CallPath)
	context.CallPath = previousCallPath
	label := "function"
	if function.Definition != nil {
		label = function.Definition.Name
	}
	attachNamedFrame(&result, context, span, "call", label)
	return result
}

func discardCallValues(a mem.Allocator, values []core.Value) {
	for i := range values {
		freeCallables(a, &values[i])
	}
}

func attachNamedFrame(result *Result, context *Context, span source.Span, kind string, label string) {
	if result.Diagnostic.Code == "" || result.Diagnostic.ExitOnly() {
		return
	}
	a := context.Run
	if a == nil {
		a = mem.System
	}
	location := context.Program.LocateSource(context.Source, span)
	if context.Program != nil && context.Program.Script != nil && context.Source == context.Program.Script.Source.Name && context.SourceOrigin > 0 && context.SourceOrigin <= len(context.Program.SourceParts) {
		part := context.Program.SourceParts[context.SourceOrigin-1]
		location = Location{Source: part.Name, Span: source.Span{Start: span.Start - part.Start + part.Offset, End: span.End - part.Start + part.Offset}}
	}
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
	frame := diagnostic.Frame{Kind: frameKind, Label: frameLabel, Source: frameSource, Span: diagnostic.Span{Start: span.Start, End: span.End}}
	if context.EntrySource != nil && frame.Source == context.EntrySource.Name {
		frame.SourceText = context.EntrySource.Text
	}
	result.Diagnostic.Frames = append(result.Diagnostic.Frames, frame)
}

// functionAritySpan points arity diagnostics at the authored parameter list
// when it belongs to the source currently being evaluated.
func functionAritySpan(function *Function, context *Context, fallback source.Span) source.Span {
	if function == nil || len(function.Parameters) == 0 {
		return fallback
	}
	start := function.Parameters[0].Span.Start
	end := function.Parameters[len(function.Parameters)-1].Span.End
	text := ""
	if context != nil && context.EntrySource != nil {
		var authored *expr.Expr
		switch {
		case function.Definition != nil:
			authored = function.Definition.Expression
		case function.AST != nil:
			authored = function.AST
		case function.Expression != nil:
			authored = function.Expression
		case len(function.Body) != 0:
			authored = function.Body[0]
		}
		if authored == nil || authored.EntrySource != context.EntrySource {
			return fallback
		}
		text = context.EntrySource.Text
	}
	if context != nil && context.Program != nil && context.SourceOrigin > 0 && context.SourceOrigin <= len(context.Program.SourceParts) {
		part := context.Program.SourceParts[context.SourceOrigin-1]
		if part.Module || part.Included != nil || start < part.Start || end > part.End {
			return fallback
		}
	}
	if text == "" && context != nil && context.Program != nil && context.Program.Script != nil {
		text = context.Program.Script.Source.Text
	}
	if text != "" {
		if start >= 0 && end >= start && end <= len(text) {
			if open := strings.LastIndexByte(text[:start], '['); open >= 0 {
				if close := strings.IndexByte(text[end:], ']'); close >= 0 {
					return source.Span{Start: open, End: end + close + 1}
				}
			}
		}
	}
	return source.Span{Start: start, End: end}
}

func arityFailure(a mem.Allocator, span source.Span, name string, count int, min int, max int) Result {
	got := strconv.FormatInt(int64(count), 10)
	expected := strconv.FormatInt(int64(min), 10)
	if max < 0 {
		expected = "at least " + expected
	} else if max != min {
		expected += " to " + strconv.FormatInt(int64(max), 10)
	}
	unit := "arguments"
	if min == 1 && (max == 1 || max < 0) {
		unit = "argument"
	}
	return failure(a, "EXPR_INVALID", span, "`"+name+"` expects "+expected+" "+unit+"; got "+got)
}
