package rule_test

import (
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestOrderOnlyInputsPreservePurposeAndQuotedPipes(t *testing.T) {
	a := t.Allocator()
	result := rule.ParseRule(a, "order.kmk", "./out : \"./input|literal\" | prepare @([./directory]) ; env \"MODE=debug\"\n\tcat @<* > @>\n")
	if len(result.Diagnostics) != 0 || len(result.Rule.Inputs) != 3 {
		t.Fatal("order-only rule failed to parse")
		result.Free()
		return
	}
	if result.Rule.Inputs[0].OrderOnly || !result.Rule.Inputs[1].OrderOnly || !result.Rule.Inputs[2].OrderOnly {
		t.Error("input purpose was not preserved")
	}
	formatted := rule.FormatRule(a, result.Rule)
	again := rule.ParseRule(a, "formatted.kmk", formatted)
	if len(again.Diagnostics) != 0 || len(again.Rule.Inputs) != 3 || !again.Rule.Inputs[2].OrderOnly {
		t.Error("formatter lost ordering syntax")
	}
	again.Free()
	mem.FreeString(a, formatted)
	result.Free()
}
func TestOrderOnlySeparatorRejectsEmptyAndRepeatedSections(t *testing.T) {
	a := t.Allocator()
	texts := []string{"./out : ./input |\n", "./out : ./input | prepare | other\n"}
	for i := range texts {
		result := rule.ParseRule(a, "invalid.kmk", texts[i])
		if len(result.Diagnostics) == 0 {
			t.Error("malformed order-only section was accepted")
		}
		result.Free()
	}
}

func TestAlternativeBuildSeparatorParsesAndFormatsCanonically(t *testing.T) {
	a := t.Allocator()
	result := rule.ParseRule(a, "arrow.kmk", "./out <- ./input | prepare\n\tcat @<* > @>\n")
	if len(result.Diagnostics) != 0 || len(result.Rule.Outputs) != 1 || len(result.Rule.Inputs) != 2 || !result.Rule.Inputs[1].OrderOnly {
		t.Fatal("alternative rule separator failed to preserve rule structure")
		result.Free()
		return
	}
	formatted := rule.FormatRule(a, result.Rule)
	if formatted != "./out : ./input | prepare\n\tcat @<* > @>" {
		t.Error("formatter did not canonicalize the alternative separator")
	}
	colon := rule.ParseRule(a, "colon.kmk", formatted)
	if len(colon.Diagnostics) != 0 || len(colon.Rule.Inputs) != len(result.Rule.Inputs) {
		t.Error("canonical rule did not round-trip")
	}
	colon.Free()
	mem.FreeString(a, formatted)
	result.Free()
}
