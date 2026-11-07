package script_test

import (
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestKashLoopTransfersPreserveLexicalTargetsAndFormatting(t *testing.T) {
	a := t.Allocator()
	text := "for item in [1 2]\n\twith\n\t\tif (eq? item 1)\n\t\t\tcontinue\n\t\tbreak\nwhile (eq? 1 1)\n\tbreak\ndef cleanup\n\tfor item in [1]\n\t\tcontinue\n"
	s := script.ParseKash(a, "transfers.kash", text)
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 3 {
		t.Fatal("local loop transfers rejected")
		return
	}
	with := s.Items[0].Expression.Items[0].Body[0]
	if with.Body[0].Items[0].Body[0].Kind != expr.KashContinue || with.Body[1].Kind != expr.KashBreak {
		t.Error("loop transfers parsed as commands")
	}
	formatted := script.Format(a, s)
	defer mem.FreeString(a, formatted)
	again := script.ParseKash(a, "formatted.kash", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || again.Items[1].Expression.Items[0].Body[0].Kind != expr.KashBreak {
		t.Error("transfer formatting changed semantics")
	}
}

func TestKashLoopTransfersRejectNonlocalTargetsAndProcessSyntax(t *testing.T) {
	texts := []string{
		"break", "continue", "if (eq? 1 2)\n\tbreak", "with\n\tcontinue", "def unused\n\tbreak",
		"for item in [1]\n\tdef unused\n\t\tcontinue\n\tprintf forbidden",
		"for item in [1]\n\tdef unused\n\t\twhile (eq? 1 2)\n\t\t\tbreak\n\t\tcontinue\n\tprintf forbidden",
		"for item in [1]\n\tbreak 2", "for item in [1]\n\tcontinue label", "for item in [1]\n\tbreak ?",
		"for item in [1]\n\tcontinue ?? 0", "for item in [1]\n\tbreak > ./out", "for item in [1]\n\tcontinue &",
		"for item in [1]\n\tbreak | cat", "for item in [1]\n\tprintf forbidden | continue",
		"for item in [1]\n\tdefer break", "for item in [1]\n\tdefer continue; printf forbidden",
	}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "invalid.kash", texts[i])
		if len(s.Diagnostics) == 0 {
			t.Error("invalid loop transfer accepted: " + texts[i])
		}
		s.Free()
	}
}
