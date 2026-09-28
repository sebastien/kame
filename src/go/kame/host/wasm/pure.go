package wasm

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
)

// PureResult is intentionally byte-oriented at the module boundary. Text and
// diagnostics remain owned by the supplied allocator until Free.
type PureResult struct {
	Text       string
	Code       string
	Message    string
	HostNeeded bool
}

func (r *PureResult) Free(a mem.Allocator) {
	if r == nil {
		return
	}
	mem.FreeString(a, r.Text)
	mem.FreeString(a, r.Code)
	mem.FreeString(a, r.Message)
	*r = PureResult{}
}

func pureText(a mem.Allocator, text string) string {
	if text == "" {
		return ""
	}
	return core.NewString(a, text).Text
}

// EvaluatePure compiles and evaluates exactly one expression. It provides the
// wasm target's initial no-host vertical slice; requests are surfaced as
// HostNeeded instead of being serviced implicitly.
func EvaluatePure(a mem.Allocator, text string) PureResult {
	return EvaluateSourcePure(a, "", text)
}

// EvaluateSourcePure evaluates expression against one compiled source text.
// It is the synchronous, host-free half of the instance ABI.
func EvaluateSourcePure(a mem.Allocator, source string, text string) PureResult {
	parsed := script.Parse(a, "<wasm-source>", source)
	if len(parsed.Diagnostics) != 0 {
		parsed.Free()
		return PureResult{Code: pureText(a, "PARSE_ERR"), Message: pureText(a, "source contains invalid syntax")}
	}
	parsedExpression := script.Parse(a, "<wasm-expr>", text)
	if len(parsedExpression.Diagnostics) != 0 || len(parsedExpression.Items) != 1 || parsedExpression.Items[0].Expression == nil {
		parsedExpression.Free()
		parsed.Free()
		return PureResult{Code: pureText(a, "PARSE_ERR"), Message: pureText(a, "expected one valid expression")}
	}
	result := evaluateParsed(a, parsed, parsedExpression.Items[0].Expression)
	parsedExpression.Free()
	return result
}

// ValidateSource performs the compilation phase that is independent of a
// requested expression. The C ABI uses it before retaining copied source in an
// instance slot.
func ValidateSource(a mem.Allocator, source string) PureResult {
	parsed := script.Parse(a, "<wasm-source>", source)
	if len(parsed.Diagnostics) != 0 {
		parsed.Free()
		return PureResult{Code: pureText(a, "PARSE_ERR"), Message: pureText(a, "source contains invalid syntax")}
	}
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	engine := core.NewEngine(a)
	compiled := eval.CompileChecked(a, engine, parsed, registry)
	if compiled.Program == nil {
		out := PureResult{Code: pureText(a, "PARSE_ERR"), Message: pureText(a, "source cannot be compiled")}
		compiled.Free(a)
		engine.Free()
		registry.Free()
		parsed.Free()
		return out
	}
	compiled.Program.Free()
	compiled.Free(a)
	engine.Free()
	registry.Free()
	parsed.Free()
	return PureResult{}
}

func resolvePureDefinition(state any, key core.ResourceKey, context *eval.Context) eval.Result {
	return state.(*eval.Program).EvaluateDefinition(key, context)
}

func evaluateParsed(a mem.Allocator, parsed *script.Script, expression *expr.Expr) PureResult {
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	engine := core.NewEngine(a)
	program := eval.Compile(a, engine, parsed, registry)
	context := &eval.Context{Program: program, Scope: program.Scope, Run: a, ResolveDefinition: resolvePureDefinition, ResolverState: program}
	result := program.EvaluateWith(expression, context)
	if result.Waiting {
		result.Free(a)
		program.Free()
		engine.Free()
		registry.Free()
		parsed.Free()
		return PureResult{HostNeeded: true, Code: pureText(a, "HOST_REQUIRED"), Message: pureText(a, "expression requires a host request")}
	}
	if result.Diagnostic.Code != "" {
		out := PureResult{Code: pureText(a, result.Diagnostic.Code), Message: pureText(a, result.Diagnostic.Message)}
		result.Free(a)
		program.Free()
		engine.Free()
		registry.Free()
		parsed.Free()
		return out
	}
	value, ok := eval.Stringify(a, result.Value)
	result.Free(a)
	program.Free()
	engine.Free()
	registry.Free()
	parsed.Free()
	if !ok {
		return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "value cannot be represented as text")}
	}
	return PureResult{Text: value}
}
