package script_test

import (
	"kame/lang/script"
	"kame/lang/source"
	"solod.dev/so/testing"
)

func TestContributionFragmentCannotBorrowClosingDelimiter(t *testing.T) {
	a := t.Allocator()
	s := source.New(a, "fragments.km", "prefix\nitems += [1\n]\n")
	defer s.Free(a)
	end := len("prefix\nitems += [1\n")
	part := script.ParseFragment(a, s, "km", len("prefix\n"), end)
	defer part.Free()
	if len(part.Diagnostics) == 0 || part.Diagnostics[0].Code != "PARSE_ERR" {
		t.Fatal("contribution parsed beyond its fragment")
	}
	for i := range part.Diagnostics {
		if part.Diagnostics[i].Span.End > end {
			t.Error("fragment diagnostic crossed its source boundary")
		}
	}
}
