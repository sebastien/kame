package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestValuePipeCloneAndFormattingPreserveAuthoredStructure(t *testing.T) {
	a := t.Allocator()
	inputs := []string{
		`(value | first | second b)`,
		`(value | pair _ _)`,
		`(value | transform a _ b)`,
		`(value | map ([item] (item | transform)) | join " ")`,
		`(join " " (map transform value))`,
		`"@(value | transform _ b)"`,
		`$(printf %s prefix-(value | transform _ b).txt)`,
	}
	for i := range inputs {
		parsed := expr.Parse(a, "pipe.km", inputs[i])
		if len(parsed.Diagnostics) != 0 { t.Error("pipe source did not parse"); parsed.Free(); continue }
		cloned := expr.Clone(a, parsed.Expr)
		parsed.Free()
		compact := expr.Compact(a, cloned)
		if compact != inputs[i] { t.Errorf("authored structure changed: %s", compact) }
		formatted := expr.Format(a, cloned)
		expr.Free(a, cloned)
		again := expr.Parse(a, "formatted.km", formatted)
		canonical := expr.Format(a, again.Expr)
		if len(again.Diagnostics) != 0 || canonical != formatted { t.Error("pipe format did not round trip") }
		mem.FreeString(a, compact)
		mem.FreeString(a, formatted)
		mem.FreeString(a, canonical)
		again.Free()
	}
}
