package script_test

import (
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestKashEagerBindingsPreserveDeclarationAndStatementIdentity(t *testing.T) {
	a := t.Allocator()
	text := "let = 1\nlet value = $(printf %s typed) ?? \"fallback\"\ndef probe arg\n\tlet local = [arg value]\n\treturn (count local)\n"
	s := script.ParseKash(a, "eager.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 3 {
		t.Fatal("eager declarations rejected")
		return
	}
	if s.Items[0].Definition.Eager || !s.Items[1].Definition.Eager || s.Items[1].Definition.Expression.Kind != expr.KashLet {
		t.Error("eager and ordinary lazy declarations were conflated")
	}
	if s.Items[2].Definition.Expression.Body[0].Kind != expr.KashLet {
		t.Error("nested eager binding lost its runtime statement")
	}
	formatted := script.Format(a, s)
	defer mem.FreeString(a, formatted)
	again := script.ParseKash(a, "formatted.kash", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || !again.Items[1].Definition.Eager || again.Items[2].Definition.Expression.Body[0].Kind != expr.KashLet {
		t.Error("eager binding formatting changed semantics")
	}
}

func TestKashEagerBindingsRejectMalformedAndConflictingDeclarations(t *testing.T) {
	texts := []string{
		"let", "let value", "let value =", "let value =\nprintf must-not-run", "let env = 1", "let (helper) = 1", "let value += 1", "let value ?= 1",
		"let value = 1\nvalue = 2", "value = 1\nlet value = 2", "let value = 1\nlet value = 2", "def value\n\treturn 0\nlet value = 1",
		"for item in [1]\n\tlet item = 2\n", "def unused item\n\tlet item = 1\n", "def unused rest...\n\tlet rest = 1\n",
	}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "invalid.kash", texts[i])
		if len(s.Diagnostics) == 0 {
			t.Error("invalid eager declaration accepted: " + texts[i])
		}
		s.Free()
	}
}
