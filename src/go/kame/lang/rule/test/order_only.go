package rulecheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/rule"
)

func TestOrderOnlyInputsPreservePurposeAndQuotedPipes(t *testing.T) {
	a := t.Allocator()
	result := rule.ParseRule(a, "order.km", "rule ./out : \"./input|literal\" | prepare ${nop [./directory]} ; {env: {MODE: \"debug\"}}\n\tcat $...inputs > $outputs.0\n")
	if len(result.Diagnostics) != 0 || len(result.Rule.Inputs) != 3 {
		t.Fatal("order-only rule failed to parse")
		result.Free()
		return
	}
	if result.Rule.Inputs[0].OrderOnly || !result.Rule.Inputs[1].OrderOnly || !result.Rule.Inputs[2].OrderOnly {
		t.Error("input purpose was not preserved")
	}
	formatted := rule.FormatRule(a, result.Rule)
	again := rule.ParseRule(a, "formatted.km", formatted)
	if len(again.Diagnostics) != 0 || len(again.Rule.Inputs) != 3 || !again.Rule.Inputs[2].OrderOnly {
		t.Error("formatter lost ordering syntax")
	}
	again.Free()
	mem.FreeString(a, formatted)
	result.Free()
}
func TestOrderOnlySeparatorRejectsEmptyAndRepeatedSections(t *testing.T) {
	a := t.Allocator()
	texts := []string{"rule ./out : ./input |\n", "rule ./out : ./input | prepare | other\n"}
	for i := range texts {
		result := rule.ParseRule(a, "invalid.km", texts[i])
		if len(result.Diagnostics) == 0 {
			t.Error("malformed order-only section was accepted")
		}
		result.Free()
	}
}

func TestRuleRequiresDeclarationKeywordAndColonSeparator(t *testing.T) {
	for _, text := range []string{"rule ./out <- ./input", "./out : ./input", "task first\ntask second"} {
		parsed := rule.ParseRule(t.Allocator(), "invalid.km", text)
		if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("invalid standalone declaration was accepted: %q", text)
		}
		parsed.Free()
	}
	for _, text := range []string{"rule ./out :", "task lint :", "service dev :"} {
		parsed := rule.ParseRule(t.Allocator(), "empty.km", text)
		if len(parsed.Diagnostics) != 0 || len(parsed.Rule.Inputs) != 0 {
			t.Errorf("declaration rejected an explicit empty prerequisite list: %q", text)
		}
		parsed.Free()
	}
}
