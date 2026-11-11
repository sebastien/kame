package scriptcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/expr"
	"kame/lang/script"
)

func TestKashExitParsesAndFormatsAsATransfer(t *testing.T) {
	a := t.Allocator()
	s := script.ParseKash(a, "exit.kash", "exit (if :true 0 7)\ndef stop status\n\texit $status\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 || s.Items[0].Expression.Kind != expr.KashExit {
		t.Fatal("exit rejected or parsed as a command")
		return
	}
	text := script.Format(a, s)
	defer mem.FreeString(a, text)
	again := script.ParseKash(a, "formatted.kash", text)
	defer again.Free()
	if len(again.Diagnostics) != 0 || again.Items[0].Expression.Kind != expr.KashExit || again.Items[1].Definition.Expression.Body[0].Kind != expr.KashExit {
		t.Error("exit formatting changed semantics")
	}
}

func TestKashExitRejectsMissingOperandsAcceptanceAndProcessSyntax(t *testing.T) {
	texts := []string{"exit", "exit; printf forbidden", "exit 0 ?", "exit 0 ?? 1", "exit 0 1", "exit 0 > ./out", "exit 0 | cat", "exit 0 &", "defer exit 0", "defer exit; printf forbidden", "printf x | exit 0"}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "invalid.kash", texts[i])
		if len(s.Diagnostics) == 0 {
			t.Error("invalid exit accepted: " + texts[i])
		}
		s.Free()
	}
}
