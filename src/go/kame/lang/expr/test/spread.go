package exprcheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/expr"
)

func TestListSpreadCloneAndFormat(t *testing.T) {
	a := t.Allocator()
	text := `[...prefix "printf" ...[1 [2]] ...(map uppercase ["a"]) ...$(printf captured)]`
	r := parse(t, text)
	defer r.Free()
	if r.Expr == nil || len(r.Expr.Items) != 5 || r.Expr.Items[0].Kind != expr.ListSpread || r.Expr.Items[0].Items[0].Kind != expr.Name {
		t.Fatal("list spread lost its operand boundary")
		return
	}
	copy := expr.Clone(a, r.Expr)
	defer expr.Free(a, copy)
	formatted := expr.Format(a, copy)
	defer mem.FreeString(a, formatted)
	again := parse(t, formatted)
	defer again.Free()
	canonical := expr.Format(a, again.Expr)
	defer mem.FreeString(a, canonical)
	if formatted != canonical || copy.Items[0].Span.Start != 1 || copy.Items[0].Items[0].Span.Start != 4 {
		t.Error("spread formatting or clone lost syntax/spans")
	}
}

func TestListSpreadRejectsInvalidPositions(t *testing.T) {
	texts := []string{`[... ]`, `[...]`, `...prefix`, `(nop ...prefix)`, `[name: 1 ...prefix]`, `[...prefix name: 1]`, `([...args] args)`}
	for i := range texts {
		r := expr.Parse(t.Allocator(), "invalid.km", texts[i])
		if len(r.Diagnostics) == 0 {
			t.Error("invalid spread accepted: " + texts[i])
		}
		r.Free()
	}
}
