package script_test

import (
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestKashFunctionsUseNativeIndentedBodies(t *testing.T) {
	a := t.Allocator()
	text := "defn probe first rest...\n  defn nested\n    return 0\n  if (nop first)\n    return 75 ?\n  (nested)\n  return (count rest)\nresult = (probe :true 1 2)\n\"defn\" literal\n\"return\" literal\n"
	s := script.ParseKash(a, "functions.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 4 {
		for i := range s.Diagnostics {
			t.Error(s.Diagnostics[i].Message)
		}
		t.Fatal("native functions did not parse")
		return
	}
	d := s.Items[0].Definition
	if !d.Function || !d.Parameters[1].Rest || d.Expression.Kind != expr.KashFunction || d.Expression.Body[0].Items[0].Kind != expr.KashFunction {
		t.Error("function body or parameter boundaries were lost")
	}
	copy := expr.Clone(a, d.Expression)
	defer expr.Free(a, copy)
	formatted := script.Format(a, s)
	defer mem.FreeString(a, formatted)
	again := script.ParseKash(a, "formatted.kash", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 {
		t.Fatal("formatted functions failed to parse")
		return
	}
	canonical := script.Format(a, again)
	defer mem.FreeString(a, canonical)
	if formatted != canonical || copy.Body[1].Items[0].Body[0].Kind != expr.KashReturn || !copy.Body[1].Items[0].Body[0].AcceptExit {
		t.Error("function clone/format lost native returns or acceptance")
	}
}

func TestKashFunctionsRejectMalformedDefinitionsAndReturns(t *testing.T) {
	texts := []string{
		"return 0", "if false\n  return 0", "defn", "defn probe\n", "defn probe {\n  return 0",
		"defn env\n  return 0", "defn probe env\n  return 0", "defn probe a a\n  return 0",
		"defn probe rest... other\n  return 0", "defn probe; printf no\n  return 0",
		"defn probe\n  return", "defn probe\n  return 0 ? printf no", "defn probe\n  defer return 0",
		"defn probe\n  return 0\nprobe = 1", "probe = 1\ndefn probe\n  return 0",
		"defn probe\n  return :nil ?? 0", "defn probe\n  return 0 &", "defn probe\n  return 0 > ./file",
		"printf before; defn probe\n  return 0",
		"defn probe#comment\n  return 0", "defn probe value#comment\n  return 0",
	}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "invalid.kash", texts[i])
		if len(s.Diagnostics) == 0 {
			t.Error("invalid function or return accepted: " + texts[i])
		}
		s.Free()
	}
}
