package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/testing"
)

func TestCompositionUncheckedCompileHonorsValueDiagnostics(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	defer engine.Free()
	parsed := script.Parse(a, "invalid.km", "unused = ([] (include ./a.km))\nother = 42\n")
	defer parsed.Free()
	p := eval.Compile(a, engine, parsed, nil)
	defer p.Free()
	if p.Valid || len(p.Diagnostics) == 0 || p.Diagnostics[0].Code != "EXPR_INVALID" || len(p.Nodes) != 0 || len(p.Definitions) != 0 || p.Definition("other") != nil {
		t.Error("unchecked compilation registered definitions despite value-placement errors")
	}
	name := expr.Parse(a, "lookup", "unused")
	defer name.Free()
	context := &eval.Context{Program: p, Scope: p.Scope, Run: a}
	result := p.EvaluateWith(name.Expr, context)
	defer result.Free(a)
	defer eval.FreeEffects(a, context.Effects)
	if result.Diagnostic.Code != "REF_MISSING" || len(context.Effects) != 0 {
		t.Error("invalid unchecked program retained a scope binding or effects")
	}
	if request := p.Requests.Next(); request.OK {
		request.Request.Free(a)
		t.Error("invalid unchecked compilation submitted host work")
	}
}
