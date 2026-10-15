package template_test

import (
	"kame/lang/expr"
	"kame/lang/source"
	"kame/lang/template"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestCaptureGlobValidationRetainsDiagnostics(t *testing.T) {
	a := t.Allocator()
	patterns := []string{"*", "**", "[a-z]", "[!a-c-a]", `\:`, "", "[]", "[!]", "[z-a]", ":"}
	for i := range patterns {
		pattern := patterns[i]
		text := "{name:" + pattern + "}"
		parsed := expr.ParsePatternText(a, text, 13)
		target := template.ParseTarget(a, "glob", text)
		if i < 5 {
			if len(parsed.Diagnostics) != 0 || len(target.Diagnostics) != 0 { t.Errorf("valid capture glob %q was rejected", pattern) }
		} else if len(parsed.Diagnostics) == 0 || len(target.Diagnostics) != 1 {
			t.Errorf("invalid capture glob %q was accepted", pattern)
		} else {
			d, td := parsed.Diagnostics[0], target.Diagnostics[0]
			if d.Code != "PARSE_ERR" || td.Code != d.Code || d.Message != "invalid capture pattern" || td.Message != d.Message || d.Severity != source.Error || td.Severity != source.Error || !sameSpan(d.Span, 19, 19+len(pattern)) || !sameSpan(td.Span, 6, 6+len(pattern)) {
				t.Errorf("capture glob %q diagnostic changed", pattern)
			}
		}
		parsed.Pattern.Free(a)
		slices.Free(a, parsed.Diagnostics)
		target.Free()
	}
}
