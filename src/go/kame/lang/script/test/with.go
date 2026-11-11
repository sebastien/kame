package scriptcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/expr"
	"kame/lang/script"
)

func TestKashWithPreservesSetupOrderBodyAndFormatting(t *testing.T) {
	a := t.Allocator()
	s := script.ParseKash(a, "with.kash", "exports = [MODE: \"release\"]\nwith :env $exports :cwd ./project\n\tlet local = 1\n\twith\n\t\tprintf %s $local\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 2 {
		t.Fatal("with block rejected")
		return
	}
	e := s.Items[1].Expression
	if e.Kind != expr.KashWith || len(e.Items) != 2 || e.Items[0].Text != "env" || e.Items[1].Text != "cwd" || e.Body[1].Kind != expr.KashWith {
		t.Error("with lost setup order or nested body")
	}
	text := script.Format(a, s)
	defer mem.FreeString(a, text)
	again := script.ParseKash(a, "formatted.kash", text)
	defer again.Free()
	if len(again.Diagnostics) != 0 || again.Items[1].Expression.Kind != expr.KashWith || again.Items[1].Expression.Items[0].Text != "env" {
		t.Error("with formatting changed semantics")
	}
}

func TestKashWithRejectsInvalidHeadersAndEmptyBodies(t *testing.T) {
	texts := []string{
		"with", "with\n# comment\n", "with\nprintf outside\n", "printf prior; with\n\tprintf forbidden",
		"with :cwd\n\tprintf forbidden", "with :env\n\tprintf forbidden", "with :cwd ./one :cwd ./two\n\tprintf forbidden",
		"with :env $one :env $two\n\tprintf forbidden", "with :timeout 1\n\tprintf forbidden", "with :MODE release\n\tprintf forbidden",
		"with :cwd ./project ?? ./fallback\n\tprintf forbidden", "with :cwd ./project; printf forbidden\n\tprintf forbidden",
		"with :cwd ./project &\n\tprintf forbidden", "with :cwd ./project > ./out\n\tprintf forbidden", "printf prior | with :cwd ./project\n\tprintf forbidden",
	}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "invalid.kash", texts[i])
		if len(s.Diagnostics) == 0 {
			t.Error("invalid with block accepted: " + texts[i])
		}
		s.Free()
	}
}
