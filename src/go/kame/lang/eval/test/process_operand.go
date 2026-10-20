package eval_test

import (
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestLiveStreamOperandsHaveDistinctArgumentPorts(t *testing.T) {
	texts := []string{
		"diff -u <(printf a) <(printf b)",
		"printf a | cat <&",
		"printf a | cat <& <&",
		"cat <&",
		"stream reports\n(printf a >&reports, cat <&reports)",
		"stream reports\n(printf a >&reports, cat <&reports <&reports)",
		"cat <((printf a, printf b))",
		"cat <(printf a |(frame :lines))",
	}
	codes := []string{"", "", "KASH_DOUBLE_CONSUME", "KASH_CHANNEL_UNBOUND", "", "KASH_CHANNEL_DUP_READER", "KASH_AMBIGUOUS_CAPTURE", ""}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "operands.kash", texts[i])
		if len(s.Diagnostics) != 0 {
			t.Errorf("operand fixture did not parse: %s", texts[i])
			s.Free()
			continue
		}
		var graph *expr.Expr
		for j := range s.Items {
			e := s.Items[j].Expression
			if e != nil && e.Kind == expr.CommandGraph {
				graph = e
				break
			}
		}
		flat := mem.Alloc[expr.Expr](t.Allocator())
		r := eval.InspectProcessTopology(t.Allocator(), graph, flat)
		if r.Diagnostic.Code != codes[i] {
			t.Errorf("%s: got %s want %s", texts[i], r.Diagnostic.Code, codes[i])
		}
		if i == 0 && (len(flat.Items) != 3 || len(flat.Edges) != 2 || flat.Edges[0].Argument == flat.Edges[1].Argument) {
			t.Error("two independent producer arguments were conflated")
		}
		if i == 1 && (len(flat.Edges) != 1 || flat.Edges[0].Argument == 0) {
			t.Error("<& left an implicit stdin consumer")
		}
		r.Free(t.Allocator())
		expr.Free(t.Allocator(), flat)
		s.Free()
	}
}
