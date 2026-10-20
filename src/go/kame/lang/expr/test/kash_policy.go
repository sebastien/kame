package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/testing"
)

func TestLossyPoliciesAreClosedAndUnambiguous(t *testing.T) {
	invalid := []string{
		"$(printf a |(broadcast :items 1) (cat, cat))",
		"$(printf a |(broadcast :overflow block :items 1) (cat, cat))",
		"$(printf a |(broadcast :overflow latest :items 2) (cat, cat))",
		"$(printf a |(broadcast :overflow drop-old :items 1025) (cat, cat))",
		"$(printf a |(broadcast :overflow drop-old :items 0) (cat, cat))",
		"$(printf a |(broadcast :overflow random) (cat, cat))",
		"$(printf a |(balance :overflow drop-old) (cat, cat))",
		"$((printf a, printf b) |(merge :overflow drop-new) cat)",
		"$(printf a |(broadcast :overflow drop-old :overflow latest) (cat, cat))",
		"$(printf a |(broadcast :overflow (read ./policy)) (cat, cat))",
	}
	for i := range invalid {
		r := expr.Parse(t.Allocator(), "invalid.km", invalid[i])
		if len(r.Diagnostics) == 0 {
			t.Errorf("invalid lossy policy accepted: %s", invalid[i])
		}
		r.Free()
	}
}
