package eval_test

import (
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestNamedChannelsResolveBeforeAnyEvaluation(t *testing.T) {
	texts := []string{
		"stream reports\n(printf a >&reports, &reports | cat)",
		"stream reports\n(printf a >&reports, printf b >&reports, &reports | cat)",
		"stream reports\n(printf a >&reports, &reports | cat, &reports | cat)",
		"stream reports\nprintf a >&reports\n&reports | cat",
		"(printf a >&reports, &reports | cat)",
		"stream first\nstream second\n(&first | cat >&second, &second | cat >&first)",
	}
	codes := []string{"", "KASH_CHANNEL_DUP_WRITER", "KASH_CHANNEL_DUP_READER", "KASH_CHANNEL_UNBOUND", "KASH_CHANNEL_UNBOUND", "KASH_GRAPH_CYCLE"}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "channels.kash", texts[i])
		if len(s.Diagnostics) != 0 {
			t.Errorf("channel fixture did not parse: %s", texts[i])
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
			t.Errorf("got %s want %s", r.Diagnostic.Code, codes[i])
		}
		if i == 0 && (len(flat.Items) != 2 || len(flat.Edges) != 1) {
			t.Error("named aliases created runtime workers instead of a direct pipe")
		}
		r.Free(t.Allocator())
		expr.Free(t.Allocator(), flat)
		s.Free()
	}
}
