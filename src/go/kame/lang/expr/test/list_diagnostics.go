package exprcheck

import (
	"kame/internal/testing"
	"kame/lang/expr"
)

func TestListEOFDiagnostic(t *testing.T) {
	texts := []string{"[", "[1", "[1, 2", "[1, 2 "}
	for _, text := range texts {
		r := expr.Parse(t.Allocator(), "list.km", text)
		if len(r.Diagnostics) != 1 {
			t.Error("list EOF must produce exactly one diagnostic")
		} else if r.Diagnostics[0].Code != "PARSE_ERR" || r.Diagnostics[0].Message != "unclosed list; expected ']'" || r.Diagnostics[0].Span.Start != 0 || r.Diagnostics[0].Span.End != len(text) {
			t.Error("list EOF diagnostic lost code, closing delimiter, or bounded span")
		}
		if r.Expr == nil || r.Expr.Kind != expr.List {
			t.Error("list EOF discarded the owned partial AST")
		} else if text != "[" && (len(expr.Ungroup(r.Expr).Items) == 0 || expr.Ungroup(r.Expr).Items[0].Int != 1) {
			t.Error("list EOF discarded parsed items")
		}
		r.Free()
	}
}

func TestListAdjacentItemsRequireComma(t *testing.T) {
	texts := []string{"[1\"two\"]", "[[]1]", "[1[2]]"}
	for _, text := range texts {
		r := expr.Parse(t.Allocator(), "list.km", text)
		found := false
		for i := range r.Diagnostics {
			if r.Diagnostics[i].Code == "PARSE_ERR" && r.Diagnostics[i].Message == "expected ',' or ']' after list item" {
				found = true
			}
		}
		if !found {
			t.Error("adjacent list items lost their separator diagnostic")
		}
		r.Free()
	}
}
