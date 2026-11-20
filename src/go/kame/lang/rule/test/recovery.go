package rulecheck

import (
	"kame/internal/testing"
	"kame/lang/expr"
	"kame/lang/rule"
)

func TestDuplicateTargetArgumentDiagnosticIsStable(t *testing.T) {
	parsed := rule.ParseRule(t.Allocator(), "duplicate.km", "task deploy {name} {name=default} : dep")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 1 || parsed.Diagnostics[0].Message != "duplicate target argument" || parsed.Diagnostics[0].Span.Start != 19 || parsed.Diagnostics[0].Span.End != 33 {
		t.Error("duplicate target argument diagnostic lost its literal message or authored span")
	}
}

func TestRecipeDocumentPreservesNewerInputSelector(t *testing.T) {
	parsed := rule.ParseRule(t.Allocator(), "selector.km", "task build : dep\n\t@{changed-inputs}\n\t@if true\n\tok\n\t@end if\n")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || parsed.Rule == nil || parsed.Rule.BodyDoc == nil {
		t.Fatal("recipe did not select document lowering")
		return
	}
	found := false
	for i := range parsed.Rule.BodyDoc.Root.Items {
		item := parsed.Rule.BodyDoc.Root.Items[i]
		if item.Kind == expr.Name && item.Text == "changed-inputs" {
			found = true
			if item.Span.Start != 20 || item.Span.End != 34 {
				t.Errorf("unexpected changed-inputs reference: %q %+v", item.Text, item.Span)
			}
		}
	}
	if !found {
		t.Error("recipe document lost its changed-inputs reference")
	}
}
