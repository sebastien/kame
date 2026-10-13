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

func TestValueFragmentRetainsBoundedCommentOrderAndOffsets(t *testing.T) {
	a := t.Allocator()
	prefix := "prior : dependency\n"
	text := prefix + "  # before  \nv = 1\n// after\n" + "following : dependency\n"
	s := source.New(a, "fragments", text)
	defer s.Free(a)
	end := len(prefix) + len("  # before  \nv = 1\n// after\n")
	part := script.ParseFragment(a, s, "km", len(prefix), end)
	defer part.Free()
	if len(part.Diagnostics) != 0 || len(part.Items) != 3 {
		t.Fatal("value fragment crossed an independent rule fragment")
		return
	}
	if part.Items[0].Kind != script.Comment || part.Items[0].Text != "# before" || part.Items[0].Span.Start != len(prefix)+2 || part.Items[0].Span.End != len(prefix)+len("  # before") || part.Items[1].Kind != script.Definition || part.Items[2].Kind != script.Comment {
		t.Error("bounded value fragment changed comment spans or order")
	}
}

func TestValueScriptRuleDiagnosticNamesTheLanguage(t *testing.T) {
	a := t.Allocator()
	s := source.New(a, "wrong.km", "default : ./out")
	defer s.Free(a)
	part := script.ParseFragment(a, s, "km", 0, len(s.Text))
	defer part.Free()
	if len(part.Diagnostics) != 1 || part.Diagnostics[0].Code != "PARSE_ERR" || part.Diagnostics[0].Message != "build rule syntax is not accepted in a km value script; use --lang kmk for definitions and build rules" {
		t.Error("rule diagnostic does not explain the grammar mismatch")
	}
}

func TestValueScriptSymbolAdjacencyDoesNotSuggestBuildGrammar(t *testing.T) {
	a := t.Allocator()
	s := source.New(a, "wrong.km", "default :false")
	defer s.Free(a)
	part := script.ParseFragment(a, s, "km", 0, len(s.Text))
	defer part.Free()
	if len(part.Diagnostics) != 1 || part.Diagnostics[0].Message != "expected statement separator" {
		t.Error("symbol adjacency was misidentified as a build rule")
	}
}
