package eval_test

import (
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestInvocationInputRootExcludesAnonymousProducersAndCurrentOperands(t *testing.T) {
	texts := []string{"cat", "cat | cat", "cat <(cat)", "cat <(cat <(cat))", "(cat, cat) |(merge) cat", "cat | cat <&"}
	expected := []bool{true, true, true, true, false, true}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "input.kash", texts[i])
		if len(s.Diagnostics) != 0 {
			t.Fatal("invalid input test graph: " + texts[i])
			return
		}
		flat := mem.Alloc[expr.Expr](t.Allocator())
		r := eval.InspectProcessTopology(t.Allocator(), s.Items[0].Expression, flat)
		if r.Diagnostic.Code != "" || (flat.InputRoot >= 0) != expected[i] {
			t.Error("incorrect primary input root: " + texts[i])
		}
		if flat.InputRoot >= 0 && (flat.Items[flat.InputRoot].Kind != expr.CommandStage || flat.Items[flat.InputRoot].Int != 0) {
			t.Error("non-executable/current-stream receiver inherited input")
		}
		r.Free(t.Allocator())
		expr.Free(t.Allocator(), flat)
		s.Free()
	}
}
