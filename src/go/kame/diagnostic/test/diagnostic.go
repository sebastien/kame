package diagnostic_test

import (
	"kame/diagnostic"
	"solod.dev/so/testing"
)

func TestFreeAcceptsBorrowedDiagnostic(t *testing.T) {
	d := diagnostic.Diagnostic{Code: "FEATURE_UNSUP", Severity: diagnostic.Error, Message: "unsupported"}
	d.Free(t.Allocator())
}

func TestCloneOwnsStructuredDiagnosticContext(t *testing.T) {
	a := t.Allocator()
	d := diagnostic.Diagnostic{
		Source:      "Makefile.kmk",
		Code:        "RECIPE_FAIL",
		Severity:    diagnostic.Error,
		Message:     "recipe exited unsuccessfully",
		Span:        diagnostic.Span{Start: 3, End: 7},
		Target:      "build",
		Notes:       []string{"output was not produced"},
		Related:     []diagnostic.Related{{Message: "declared here", Source: "Makefile.kmk", Span: diagnostic.Span{Start: 1, End: 2}}},
		Frames:      []diagnostic.Frame{{Kind: "rule", Label: "build", Source: "Makefile.kmk", Span: diagnostic.Span{Start: 1, End: 7}}},
		TargetStack: []string{"all", "build"},
		Tips:        []string{"check the compiler"},
		Cause:       diagnostic.Cause{Kind: "process", Message: "shell exited", Program: "sh", Status: 127, HasStatus: true, StderrTruncated: true, StderrLimit: 64},
	}
	copy := d.Clone(a)
	if !copy.Owned || len(copy.Related) != 1 || copy.Frames[0].Kind != "rule" || len(copy.TargetStack) != 2 || copy.Cause.Status != 127 {
		t.Error("clone lost structured diagnostic context")
	}
	copy.Free(a)
}
