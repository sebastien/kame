package rule_test

import (
	"kame/lang/rule"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func sameSpan(span source.Span, start int, end int) bool {
	return span.Start == start && span.End == end
}

func TestFileRuleParsesRecipeIndentation(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "./out : ./in dep\n\tcommand @< @>\n\t  nested")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Rule == nil || result.Rule.Kind != rule.FileRule || len(result.Rule.Outputs) != 1 || len(result.Rule.Inputs) != 2 || len(result.Rule.Body) != 2 {
		t.Error("file rule did not parse header and recipe body")
		return
	}
	if result.Rule.Body[0].Text != "command @< @>" || result.Rule.Body[1].Text != "  nested" || !sameSpan(result.Rule.Body[0].Span, 18, 31) {
		t.Error("recipe prefix removal or line span was incorrect")
	}
}

func TestNamedAndPrefixedRulesClassify(t *testing.T) {
	task := rule.ParseRule(t.Allocator(), "test.km", "default : dep")
	defer task.Free()
	cached := rule.ParseRule(t.Allocator(), "test.km", "task build : dep")
	defer cached.Free()
	service := rule.ParseRule(t.Allocator(), "test.km", "service dev : dep")
	defer service.Free()
	if task.Rule.Kind != rule.TaskRule || cached.Rule.Kind != rule.CachedTaskRule || service.Rule.Kind != rule.ServiceRule {
		t.Error("named, cached, and service rules were not classified")
	}
}

func TestFormatRuleCanonicalizesWhitespace(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "./out : ./in\n  echo ok")
	defer result.Free()
	formatted := rule.FormatRule(t.Allocator(), result.Rule)
	if formatted != "./out : ./in\n\techo ok" {
		t.Errorf("FormatRule() = %q", formatted)
	}
	mem.FreeString(t.Allocator(), formatted)
}

func TestFormatRulePreservesRecipeBlankLines(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "./out : ./in\n  echo one\n \t\n  echo two")
	defer result.Free()
	if len(result.Diagnostics) != 0 {
		t.Fatalf("parse diagnostics: %d", len(result.Diagnostics))
	}
	formatted := rule.FormatRule(t.Allocator(), result.Rule)
	if formatted != "./out : ./in\n\techo one\n\n\techo two" {
		t.Errorf("FormatRule() = %q", formatted)
	}
	mem.FreeString(t.Allocator(), formatted)
	if len(result.Rule.Body) != 3 || result.Rule.Body[1].Text != "" || result.Rule.Body[1].Template != nil || !sameSpan(result.Rule.Body[1].Span, 24, 26) {
		t.Errorf("blank recipe line body=%d text=%q template=%t span=%d:%d", len(result.Rule.Body), result.Rule.Body[1].Text, result.Rule.Body[1].Template != nil, result.Rule.Body[1].Span.Start, result.Rule.Body[1].Span.End)
	}
}

func TestRuleHeaderBalancesQuotedAndExpressionInputs(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "\"./build dir/out\" : \"literal input\" @(wildcard ./src/*.c)")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Rule.Kind != rule.FileRule || len(result.Rule.Inputs) != 2 || result.Rule.Inputs[0].Kind != rule.InputString || result.Rule.Inputs[0].Template == nil || result.Rule.Inputs[1].Kind != rule.InputExpression || result.Rule.Inputs[1].Template == nil {
		t.Error("rule header split quoted or expression input")
	}
}

func TestRecipeTemplateRecoversMalformedExpansion(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "default : dep\n\techo @(name")
	defer result.Free()
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Severity != source.Warning || result.Rule.Body[0].Template == nil {
		t.Error("recipe template did not recover malformed interpolation")
	}
}

func TestRuleRetainsTargetFormAndNormalizesCRLFRecipe(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "./{name:*}.o : ./{name:*}.c\r\n\techo @<\r\n")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Rule.Outputs[0].TargetForm == nil || result.Rule.Inputs[0].TargetForm == nil || len(result.Rule.Body) != 1 || result.Rule.Body[0].Text != "echo @<" {
		t.Errorf("rule forms=%t/%t body=%d diagnostics=%d", result.Rule.Outputs[0].TargetForm != nil, result.Rule.Inputs[0].TargetForm != nil, len(result.Rule.Body), len(result.Diagnostics))
	}
}

func TestWildcardInputRetainsAuthoredToken(t *testing.T) {
 result := rule.ParseRule(t.Allocator(), "test.kmk", "default : ./src/**/*.c \"./literal*.c\"\n\techo ok")
 defer result.Free()
 if len(result.Diagnostics) != 0 || len(result.Rule.Inputs) != 2 || result.Rule.Inputs[0].Kind != rule.InputWildcard || result.Rule.Inputs[0].Template == nil || result.Rule.Inputs[1].Kind != rule.InputString {
  t.Error("wildcard input classification or quoted literal changed")
 }
}
