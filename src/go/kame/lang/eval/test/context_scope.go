package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/testing"
)

func callerScopeOperation(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = values
	return eval.Result{Value: core.Value{Kind: core.Bool, Bool: c.Scope == state.(*eval.Scope)}}
}

func TestOperandEvaluationPreservesOperationCallerScope(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "scope.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	registry.Add(eval.Operation{Name: "caller-scope", MinArity: 1, MaxArity: 1, Call: callerScopeOperation, Context: p.Scope})
	expression := expr.Parse(a, "scope.km", "(caller-scope (let [temporary :true] :nil))")
	result := p.Evaluate(a, expression.Expr, p.Scope)
	if result.Diagnostic.Code != "" || result.Value.Kind != core.Bool || !result.Value.Bool {
		t.Error("temporary operand scope replaced the operation caller scope")
	}
	result.Free(a)
	expression.Free()
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
