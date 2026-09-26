package program_test

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lang/eval"
)

func dependOnNamedTarget(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if len(values) != 1 || values[0].Kind != core.String { return eval.Result{Diagnostic: coreDiagnostic("invalid target dependency")} }
	key := core.ResourceKey{Kind: core.ResourceTarget, Name: values[0].Text}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func observeDependency(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	_ = values
	key := core.ResourceKey{Kind: core.ResourceTarget, Name: "input"}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func observeFileDependency(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if len(values) != 1 || values[0].Kind != core.String { return eval.Result{Diagnostic: coreDiagnostic("invalid file dependency")} }
	key := core.ResourceKey{Kind: core.ResourceFile, Name: values[0].Text}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func coreDiagnostic(message string) diagnostic.Diagnostic { return diagnostic.Diagnostic{Code: "EXPR_INVALID", Severity: diagnostic.Error, Message: message} }

