package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/testing"
)

func TestNewerInputSelectorIsCompleteAndInputOnly(t *testing.T) {
	if !expr.ValidSelector("@<?") || expr.ValidSelector("@>?") || expr.ValidSelector("@?") || expr.ValidSelector("@<?1") {
		t.Error("newer selector validation")
	}
	parsed := expr.Parse(t.Allocator(), "newer.km", "(count @<?)")
	if len(parsed.Diagnostics) != 0 {
		t.Error("newer selector application failed to parse")
	}
	parsed.Free()
}
