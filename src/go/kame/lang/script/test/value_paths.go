package script_test

import (
	"kame/lang/expr"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestKashLeadingValuePathsKeepExpressionBoundaries(t *testing.T) {
	a := t.Allocator()
	texts := []string{
		"root = \"./work\"; path = @{root}/config.json; (nop path)",
		"(root) = \"./work\"; path = @(root)/config.json; (nop path)",
		"path = @(nop\n \"./work\")/config.json; (nop path)",
		"path = @{root}/semi\\;colon; (nop path)",
		"path = @(nop \"./semi;colon\")/file; (nop path)",
		"path = (nop @{root}/semi;colon); (nop path)",
		"for file in @{root}/*.txt\n\tprintf $file\n",
		"def status\n\treturn @{root}/status\n",
	}
	for i := range texts {
		s := script.ParseKash(a, "paths.kash", texts[i])
		if len(s.Diagnostics) != 0 {
			for j := range s.Diagnostics {
				t.Error(s.Diagnostics[j].Message)
			}
			t.Error("valid value path did not parse: " + texts[i])
			s.Free()
			continue
		}
		formatted := script.Format(a, s)
		copy := script.ParseKash(a, "formatted.kash", formatted)
		if len(copy.Diagnostics) != 0 {
			t.Error("formatted value path did not parse")
		}
		again := script.Format(a, copy)
		if formatted != again {
			t.Error("value-path formatting is not idempotent")
		}
		mem.FreeString(a, again)
		copy.Free()
		mem.FreeString(a, formatted)
		if s.Items[0].Definition != nil {
			cloned := expr.Clone(a, s.Items[0].Definition.Expression)
			expr.Free(a, cloned)
		}
		s.Free()
	}
}
