// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/lang/definition"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Callable ownership protocol.
//
// core.Value.Clone and core.Value.Free are callable-blind: callables are
// shallow-copied and Free releases only string/bytes/list/record storage.
// Callable scope lifetime lives here:
//
//   - freeValues and freeRecord release storage only. Use them when values
//     were cloned into a result, child scope, or operation state (transfer).
//     Deep freeing there would release a scope still owned by the result.
//   - freeValuesWithCallables and freeRecordWithCallables release scopes in
//     addition to storage. Use them when values will not be transferred into
//     a result or binding (discard on Waiting/diagnostic paths).
//   - Operation arguments are split: the operation frees consumed callables
//     through Context.FreeCallable, then the caller shallow-frees the argument
//     slice. The pre-Call error path discards deeply because no callee ran.
//   - Result.Free always frees deeply; it owns its value.
//   - Ancestor stores are rejected: storing a value that would strand its
//     capture (wouldStrandCapture) fails with DEF_ESCAPE via
//     rejectStrandedCapture. Enforcement lives in the checked
//     Scope.trySetValue wrapper used by let/def value forms; raw setValue
//     stays for fresh-child stores in callValues that cannot strand.
//
// FunctionKind records closure lifetimes and distinguishes the two kinds of
// immutable, invocation-owned callable: Kash scripts/constructors and operations.
type FunctionKind int

const (
	// FunctionTemporary is a value-owned closure. It owns its captured Scope
	// (when non-nil) and its Native state (through NativeFree). Lambdas,
	// sections, and native sections (replace) are temporary.
	FunctionTemporary FunctionKind = iota
	// FunctionBorrowed is a value-owned wrapper copied from a binding. It
	// retains the captured Scope and shares parameters, body, and Native
	// state with its source. It releases its wrapper, scope hold, and owned
	// payload share; authored ASTs remain owned by the source scope/program.
	//
	// Native state and dynamic AST storage have an independent retained owner.
	FunctionBorrowed
	// FunctionDefinition is a scope-owned definition function (def, script
	// function). The scope owns the Function struct plus owned AST slices;
	// values carrying it are borrowed and must not free the struct.
	FunctionDefinition
	// FunctionInvocation is an immutable Kash callable owned by Program until shutdown.
	FunctionInvocation
	// FunctionOperation is an immutable registry callable owned by Program.
	FunctionOperation
)

type Function struct {
	storage      *FunctionStorage
	AST          *expr.Expr
	// PayloadAlloc owns native state and cloned ASTs, not borrowed wrappers.
	PayloadAlloc mem.Allocator
	Owner        *Program
	// OperationName is the registry key for FunctionOperation, not a discriminator.
	OperationName       string
	KashConstructor     bool
	KashScript          *script.Script
	Kind                FunctionKind
	Parameters          []expr.Parameter
	Body                []*expr.Expr
	Expression          *expr.Expr
	ExpressionOwned     bool
	DeferredReplacement bool
	Scope               *Scope
	ParametersOwned     bool
	ParameterNamesOwned bool
	BodyOwned           bool
	Definition          *definition.Definition
	// Section marks a placeholder section. Positional arguments bind to the
	// call scope for placeholder lookup instead of parameter names.
	Section bool
	// NativeCall, when set, computes the result from evaluated argument
	// values. Arity gives the fixed argument count. Native holds the
	// implementation's opaque state and NativeFree releases it after the last
	// temporary/borrowed payload share is freed.
	NativeCall func(c *Context, state any, values []core.Value) Result
	Arity      int
	Native     any
	NativeFree func(a mem.Allocator, state any)
}

// Lazily shared only when a callable with owned payload is borrowed. Scopes
// remain separate holds so the existing lexical cycle breaker can see them.
type FunctionStorage struct {
	Alloc      mem.Allocator
	References int
	Native     any
	Free       func(a mem.Allocator, state any)
	AST        *expr.Expr
	Expression *expr.Expr
}

// BorrowValue copies storage and gives every callable leaf an independent
// wrapper. core.Value.Clone intentionally remains callable-blind.
func (c *Context) BorrowValue(value core.Value) core.Value { return borrowValue(c.Run, value) }

func borrowValue(a mem.Allocator, value core.Value) core.Value {
	result := value.Clone(a)
	borrowValueCallables(a, &result)
	return result
}

func borrowValueCallables(a mem.Allocator, value *core.Value) {
	if value.Kind == core.Callable && value.Callable != nil && value.CallableOwner == nil {
		value.Callable = borrowFunction(a, value.Callable.(*Function))
	}
	for i := range value.List {
		borrowValueCallables(a, &value.List[i])
	}
	for i := range value.Record {
		borrowValueCallables(a, &value.Record[i].Value)
	}
}

// borrowFunction copies a binding-owned callable into a value-owned wrapper
// that retains the captured Scope. The wrapper shares parameters, body, and
// Native state with its source.
func borrowFunction(a mem.Allocator, source *Function) *Function {
	if source.Kind == FunctionInvocation || source.Kind == FunctionOperation {
		return source
	}
	if source.storage == nil && (source.NativeFree != nil || source.AST != nil || source.ExpressionOwned) {
		payloadAlloc := source.PayloadAlloc
		if payloadAlloc == nil {
			payloadAlloc = a
		}
		storage := mem.Alloc[FunctionStorage](payloadAlloc)
		storage.Alloc, storage.References = payloadAlloc, 1
		storage.Native, storage.Free, storage.AST = source.Native, source.NativeFree, source.AST
		if source.ExpressionOwned {
			storage.Expression = source.Expression
		}
		source.storage = storage
	}
	wrapper := mem.Alloc[Function](a)
	*wrapper = *source
	wrapper.Kind = FunctionBorrowed
	if wrapper.storage != nil {
		wrapper.storage.References++
	}
	if wrapper.Scope != nil {
		wrapper.Scope.Retain()
	}
	return wrapper
}

// DiscardCallables releases rejected argument closures, including fresh nested
// closures. Container clones can share binding-owned functions; those must stay
// alive until their lexical binding is released, unlike value-owned wrappers.
func (c *Context) DiscardCallables(value *core.Value) {
	c.discardArgumentCallables(value, 0)
}

func (c *Context) discardArgumentCallables(value *core.Value, depth int) {
	// Rejecting malformed host graphs must not recurse through cycles forever.
	if depth >= 256 {
		return
	}
	if value.Kind == core.Callable {
		for scope := c.Scope; scope != nil; scope = scope.Parent {
			for i := range scope.Bindings {
				if containsCallable(scope.Bindings[i].Value, value.Callable, 0) {
					value.Callable = nil
					return
				}
			}
		}
		freeCallables(c.Run, value)
		return
	}
	for i := range value.List {
		c.discardArgumentCallables(&value.List[i], depth+1)
	}
	for i := range value.Record {
		c.discardArgumentCallables(&value.Record[i].Value, depth+1)
	}
}

func containsCallable(value core.Value, function any, depth int) bool {
	if depth >= 256 {
		return false
	}
	if value.Kind == core.Callable {
		return value.Callable == function
	}
	for i := range value.List {
		if containsCallable(value.List[i], function, depth+1) {
			return true
		}
	}
	for i := range value.Record {
		if containsCallable(value.Record[i].Value, function, depth+1) {
			return true
		}
	}
	return false
}

func freeCallables(a mem.Allocator, value *core.Value) {
	if value.Kind == core.Callable {
		if value.Callable == nil {
			return
		}
		function := value.Callable.(*Function)
		if function.Kind == FunctionTemporary {
			freeTemporaryFunction(a, function)
		} else if function.Kind == FunctionBorrowed {
			freeBorrowedWrapper(a, function)
		}
		value.Callable = nil
		return
	}
	if value.Kind == core.List {
		for i := range value.List {
			freeCallables(a, &value.List[i])
		}
	}
	if value.Kind == core.Record {
		for i := range value.Record {
			freeCallables(a, &value.Record[i].Value)
		}
	}
}

func freeTemporaryFunction(a mem.Allocator, function *Function) {
	freeFunctionStorage(a, function)
	function.Scope.Free()
	mem.Free(a, function)
}

func freeBorrowedWrapper(a mem.Allocator, function *Function) {
	if function.storage != nil {
		freeFunctionStorage(a, function)
	}
	function.Scope.Free()
	function.Native = nil
	mem.Free(a, function)
}

func freeFunctionStorage(a mem.Allocator, function *Function) {
	storage := function.storage
	if storage != nil {
		storage.References--
		if storage.References != 0 {
			return
		}
		if storage.Free != nil {
			storage.Free(storage.Alloc, storage.Native)
		}
		expr.Free(storage.Alloc, storage.AST)
		expr.Free(storage.Alloc, storage.Expression)
		mem.Free(storage.Alloc, storage)
		return
	}
	if function.PayloadAlloc != nil {
		a = function.PayloadAlloc
	}
	if function.NativeFree != nil {
		function.NativeFree(a, function.Native)
	}
	expr.Free(a, function.AST)
	if function.ExpressionOwned {
		expr.Free(a, function.Expression)
	}
}

// wouldStrandCapture reports whether storing value in scope would strand a
// capture: true when value holds a callable whose capture scope is strictly
// enclosed by scope (scope is an ancestor of the capture). Such a store
// deadlocks teardown: scope waits on the capture's parent retain while the
// capture waits on the stored wrapper, and the cycle breaker only handles
// self-cycles. Same-scope, descendant, and cousin stores stay safe through
// transfer discipline and late-release re-driving, so only ancestor stores
// are rejected. Nil and nil-scope captures cannot strand and are allowed.
//
// The name distinguishes this forbidden ancestor store from the allowed
// "escaped" closures in tests (e.g. a let-bound lambda returned as its own
// result): returning a closure keeps its capture alive through the borrowed
// wrapper, while storing it into an ancestor strands it.
func wouldStrandCapture(scope *Scope, value core.Value) bool {
	if value.Kind == core.Callable {
		function := value.Callable.(*Function)
		if function != nil && (function.Kind == FunctionInvocation || function.Kind == FunctionOperation) {
			return false
		}
		if function == nil || function.Scope == nil || function.Scope == scope {
			return false
		}
		for s := function.Scope.Parent; s != nil; s = s.Parent {
			if s == scope {
				return true
			}
		}
		return false
	}
	if value.Kind == core.List {
		for i := range value.List {
			if wouldStrandCapture(scope, value.List[i]) {
				return true
			}
		}
	}
	if value.Kind == core.Record {
		for i := range value.Record {
			if wouldStrandCapture(scope, value.Record[i].Value) {
				return true
			}
		}
	}
	return false
}

// freeValues releases storage only. Use after values were cloned into a
// result, child scope, or operation state (transfer).
func freeValues(a mem.Allocator, values []core.Value) {
	for i := range values {
		values[i].Free(a)
	}
	slices.Free(a, values)
}

// freeValuesWithCallables releases scopes in addition to storage. Use when
// values will not be transferred into a result or binding (discard).
func freeValuesWithCallables(a mem.Allocator, values []core.Value) {
	for i := range values {
		freeCallables(a, &values[i])
	}
	freeValues(a, values)
}

func freeRecord(a mem.Allocator, values []core.RecordField) {
	for i := range values {
		values[i].Value.Free(a)
	}
	slices.Free(a, values)
}

// freeRecordWithCallables releases scopes in addition to storage. Use only
// for freshly evaluated fields that share no storage with a source record.
// Selection fields cloned from a source record must stay shallow: the source
// still owns those scopes.
func freeRecordWithCallables(a mem.Allocator, values []core.RecordField) {
	for i := range values {
		freeCallables(a, &values[i].Value)
	}
	freeRecord(a, values)
}

// breakCallableCycle releases self-retained lambdas whose only remaining
// owners are bindings inside the scope itself. An escaping callable retains
// the scope through its borrowed wrapper, so this runs only when every
// remaining reference is such a self-retained callable.
//
// The outer Scope.Free early-returns after a successful break: the recursive
// frees below drive References to zero, and the innermost free releases the
// struct, names, sections, and parent. The breakingCycles guard prevents the
// recursive frees from rediscovering the same cycle while it is dismantled.
//
// Only bindingValue callables participate. Section arguments and
// scope-owned definition functions are excluded: sections are owned storage
// freed with the scope, definitions never retain their scope.
func (s *Scope) breakCallableCycle() bool {
	count := 0
	for i := range s.Bindings {
		count += countSelfRetained(s.Bindings[i].Value, s)
	}
	if count == 0 || count != s.References {
		return false
	}
	// Transient extraction scratch. Allocator-owned so Tracker sees the
	// backing; freed explicitly below before returning. Save the allocator
	// first: the recursive frees below release the scope struct itself, so
	// s.Alloc must not be read after they run.
	a := s.Alloc
	values := slices.Make[core.Value](a, count)
	n := 0
	for i := range s.Bindings {
		extractSelfRetained(&s.Bindings[i].Value, s, values, &n)
	}
	s.breakingCycles = true
	for i := range values {
		freeCallables(a, &values[i])
	}
	slices.Free(a, values)
	return true
}

func countSelfRetained(value core.Value, scope *Scope) int {
	// Evaluator-owned values are trees: each transient callable leaf owns a
	// distinct wrapper. Host-created aliased/cyclic ownership graphs are not valid.
	if value.Kind == core.Callable && value.Callable != nil {
		if isSelfRetainedCallable(value.Callable.(*Function), scope) {
			return 1
		}
	}
	count := 0
	for i := range value.List {
		count += countSelfRetained(value.List[i], scope)
	}
	for i := range value.Record {
		count += countSelfRetained(value.Record[i].Value, scope)
	}
	return count
}

func extractSelfRetained(value *core.Value, scope *Scope, values []core.Value, n *int) {
	if value.Kind == core.Callable && value.Callable != nil && isSelfRetainedCallable(value.Callable.(*Function), scope) {
		values[*n] = *value
		*n = *n + 1
		*value = core.Value{}
		return
	}
	for i := range value.List {
		extractSelfRetained(&value.List[i], scope, values, n)
	}
	for i := range value.Record {
		extractSelfRetained(&value.Record[i].Value, scope, values, n)
	}
}

func isSelfRetainedCallable(function *Function, scope *Scope) bool {
	if function.Scope != scope {
		return false
	}
	return function.Kind == FunctionTemporary || function.Kind == FunctionBorrowed
}
