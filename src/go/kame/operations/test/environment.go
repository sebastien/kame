package lib_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestBoundEnvironmentReadsRespectSnapshotAndGrants(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	program := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "test", "(env \"MODE\")")
	context := eval.Context{Program: program, Scope: program.Scope, Run: a, HasEnvironment: true, Environment: []string{"MODE=scoped=with-equals", "OTHER=value"}, Grants: []eval.Grant{{Capability: eval.Env, Names: []string{"MODE"}}}}
	result := program.EvaluateWith(document.Expr, &context)
	if result.Waiting || result.Diagnostic.Code != "" || result.Value.Text != "scoped=with-equals" {
		t.Error("bound environment did not return its owned value without a host request")
	}
	result.Free(a)
	context.Environment = []string{"OTHER=value"}
	result = program.EvaluateWith(document.Expr, &context)
	if result.Waiting || result.Diagnostic.Code != "" || result.Value.Kind != core.Nil {
		t.Error("missing bound name did not return nil")
	}
	result.Free(a)
	context.Environment = nil
	result = program.EvaluateWith(document.Expr, &context)
	if result.Waiting || result.Diagnostic.Code != "" || result.Value.Kind != core.Nil {
		t.Error("empty bound snapshot fell through to ambient host requests")
	}
	result.Free(a)
	context.Environment = []string{"MODE=secret"}
	context.Grants = nil
	result = program.EvaluateWith(document.Expr, &context)
	if result.Diagnostic.Code != "CAP_DENIED" {
		t.Error("bound snapshot bypassed environment grants")
	}
	result.Free(a)
	document.Free()
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}
