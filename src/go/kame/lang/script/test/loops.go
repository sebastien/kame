package script_test

import (
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestKashLoopsParseFormatAndRejectInvalidHeaders(t *testing.T) {
	a := t.Allocator()
	text := "for item in [1 2]\n  while (nop :false)\n    printf %s $item\n"
	s := script.ParseKash(a, "loops.kash", text)
	if len(s.Diagnostics) != 0 || len(s.Items) != 1 {
		t.Error("loop parse failed")
		s.Free()
		return
	}
	e := s.Items[0].Expression
	if e.Kind != expr.KashFor || e.Text != "item" || e.Items[0].Items[0].Kind != expr.List || e.Items[0].Body[0].Kind != expr.KashWhile {
		t.Error("loop AST lost binding, iterable or nested condition")
	}
	formatted := script.FormatWithIndent(a, s, "\t")
	round := script.ParseKash(a, "formatted.kash", formatted)
	again := script.FormatWithIndent(a, round, "\t")
	if len(round.Diagnostics) != 0 || again != formatted {
		t.Error("loop formatting is not idempotent")
	}
	mem.FreeString(a, formatted)
	mem.FreeString(a, again)
	round.Free()
	s.Free()
	bad := []string{
		"for env in []\n\ttrue", "for x in []\n\tx = 1", "for x of []\n\ttrue",
		"for x in [] extra\n\ttrue", "while true; false\n\ttrue", "while $(true)\n\ttrue",
		"while true\ntrue", "for x in []\n\t# no body", "for x in\n\ttrue",
	}
	for i := range bad {
		parsed := script.ParseKash(a, "invalid.kash", bad[i])
		if len(parsed.Diagnostics) == 0 {
			t.Errorf("invalid loop %d accepted", i)
		}
		parsed.Free()
	}
}
