package script_test

import (
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestKashRecordLoopParsingAndFormatting(t *testing.T) {
	a := t.Allocator()
	s := script.ParseKash(a, "records.kash", "for line from ./events :lines\n\twith\n\t\tcontinue\nfor path from $paths :nul\n\tbreak\n")
	defer s.Free()
	if len(s.Diagnostics) != 0 || len(s.Items) != 2 {
		t.Fatal("record loops rejected")
		return
	}
	if s.Items[0].Expression.Kind != expr.KashRecords || s.Items[0].Expression.RecordFrame != ":lines" || s.Items[1].Expression.RecordFrame != ":nul" {
		t.Error("record source/framing lost")
	}
	text := script.Format(a, s)
	defer mem.FreeString(a, text)
	again := script.ParseKash(a, "formatted.kash", text)
	defer again.Free()
	if len(again.Diagnostics) != 0 || again.Items[1].Expression.Kind != expr.KashRecords || again.Items[1].Expression.RecordFrame != ":nul" {
		t.Error("record loop formatting changed semantics")
	}
}

func TestKashRecordLoopsRejectAmbiguousHeadersAndCollisions(t *testing.T) {
	texts := []string{
		"for x from ./file\n\tbreak", "for x from ./file :bytes\n\tbreak", "for x from ./file $frame\n\tbreak",
		"for x from ./file ?? ./other :lines\n\tbreak", "for x from cat ./file :lines\n\tbreak", "for x from ./file :lines; true\n\tbreak",
		"for x from ./file :lines\n\tlet x = 1", "for env from ./file :lines\n\tbreak", "for x from ./file :lines\n",
	}
	for i := range texts {
		s := script.ParseKash(t.Allocator(), "invalid.kash", texts[i])
		if len(s.Diagnostics) == 0 {
			t.Error("invalid record header accepted: " + texts[i])
		}
		s.Free()
	}
}
