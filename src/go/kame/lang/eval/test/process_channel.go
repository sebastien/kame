package eval_test

import (
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

type outputPortCase struct {
	text string
	code string
	out  int
	err  int
}

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

func TestReservedOutputPortsLowerAndCountCaptureSinks(t *testing.T) {
	a := t.Allocator()
	cases := []outputPortCase{
		{"$(printf a >&out)", "", 1, 0},
		{"$(printf a >&err)", "", 0, 1},
		{"$((printf a >&err, printf b >&err))", "", 0, 2},
		{"$((printf a >&out, printf b >&out))", "KASH_AMBIGUOUS_CAPTURE", 2, 0},
		{"$(printf a |(frame :lines) |(filter :true | cat >&err) | cat)", "", 0, 1},
	}
	for i := range cases {
		parsed := expr.Parse(a, "ports.km", cases[i].text)
		if len(parsed.Diagnostics) != 0 {
			t.Errorf("port graph did not parse: %s", cases[i].text)
			parsed.Free()
			continue
		}
		flat := mem.Alloc[expr.Expr](a)
		result := eval.InspectProcessTopology(a, parsed.Expr, flat)
		if result.Diagnostic.Code != cases[i].code {
			t.Errorf("%s: topology diagnostic %s, want %s", cases[i].text, result.Diagnostic.Code, cases[i].code)
		}
		out, err := 0, 0
		for j := range flat.Items {
			if flat.Items[j].Kind == expr.CommandMux && flat.Items[j].Text == "out" {
				out++
			}
			if flat.Items[j].Kind == expr.CommandMux && flat.Items[j].Text == "err" {
				err++
			}
		}
		if out != cases[i].out || err != cases[i].err {
			t.Errorf("%s: lowered ports out=%d err=%d, want out=%d err=%d", cases[i].text, out, err, cases[i].out, cases[i].err)
		}
		result.Free(a)
		expr.Free(a, flat)
		parsed.Free()
	}

	invalid := []string{"$(printf a >&err | cat)", "$(printf a > ./file >&err)", "$(cat <&out)"}
	for i := range invalid {
		text := invalid[i]
		parsed := expr.Parse(a, "invalid-ports.km", text)
		if len(parsed.Diagnostics) == 0 {
			t.Errorf("invalid port disposition was accepted: %s", text)
		}
		parsed.Free()
	}
}
