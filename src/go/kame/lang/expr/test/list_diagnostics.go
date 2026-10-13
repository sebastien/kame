package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/testing"
)

func TestListEOFDiagnostic(t *testing.T) {
	texts := []string{"[", "[1", "[1 2", "[1 2 "}
	for _, text := range texts {
		r := expr.Parse(t.Allocator(), "list.km", text)
		if len(r.Diagnostics) != 1 {
			t.Error("list EOF must produce exactly one diagnostic")
		} else if r.Diagnostics[0].Code != "PARSE_ERR" || r.Diagnostics[0].Message != "unclosed list; expected ']'" || r.Diagnostics[0].Span.Start != 0 || r.Diagnostics[0].Span.End != len(text) {
			t.Error("list EOF diagnostic lost code, closing delimiter, or bounded span")
		}
		if r.Expr == nil || r.Expr.Kind != expr.List {
			t.Error("list EOF discarded the owned partial AST")
		} else if text != "[" && (len(r.Expr.Items) == 0 || r.Expr.Items[0].Int != 1) {
			t.Error("list EOF discarded parsed items")
		}
		r.Free()
	}
}

func TestListAdjacentItemsStillNeedWhitespace(t *testing.T) {
	texts := []string{"[1\"two\"]", "[[]1]", "[1[2]]"}
	for _, text := range texts {
		r := expr.Parse(t.Allocator(), "list.km", text)
		found := false
		for i := range r.Diagnostics {
			if r.Diagnostics[i].Code == "PARSE_ERR" && r.Diagnostics[i].Message == "list items need whitespace" { found = true }
		}
		if !found { t.Error("adjacent list items lost their whitespace diagnostic") }
		r.Free()
	}
}
