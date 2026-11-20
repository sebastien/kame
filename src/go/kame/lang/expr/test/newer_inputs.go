package exprcheck

import (
	"kame/internal/testing"
	"kame/lang/expr"
)

func TestChangedInputsParsesAsNamedList(t *testing.T) {
	parsed := expr.Parse(t.Allocator(), "newer.km", "count changed-inputs")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || parsed.Expr == nil || parsed.Expr.Kind != expr.Application ||
		len(parsed.Expr.Items) != 2 || parsed.Expr.Items[1].Kind != expr.Name || parsed.Expr.Items[1].Text != "changed-inputs" {
		t.Error("changed-inputs did not parse as a named list argument")
	}
}
