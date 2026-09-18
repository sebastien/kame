package diagnostic_test

import (
	"littlemake/diagnostic"
	"solod.dev/so/testing"
)

func TestFreeAcceptsBorrowedDiagnostic(t *testing.T) {
	d := diagnostic.Diagnostic{Code: "FEATURE_UNSUP", Severity: diagnostic.Error, Message: "unsupported"}
	d.Free(t.Allocator())
}
