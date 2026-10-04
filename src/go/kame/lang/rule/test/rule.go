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

func TestNamedTaskArgumentsParseFormatAndValidate(t *testing.T) {
	a := t.Allocator()
	text := "task deploy {environment} {region=us-east} : configure\n\tship @(environment) @(region)"
	parsed := rule.ParseRule(a, "arguments.kmk", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || parsed.Rule.Kind != rule.CachedTaskRule || len(parsed.Rule.Outputs) != 1 || len(parsed.Rule.Arguments) != 2 {
		t.Error("named task arguments did not parse")
		return
	}
	if parsed.Rule.Arguments[0].Name != "environment" || parsed.Rule.Arguments[0].Optional || parsed.Rule.Arguments[1].Name != "region" || !parsed.Rule.Arguments[1].Optional || parsed.Rule.Arguments[1].Default != "us-east" {
		t.Error("required or optional target argument metadata was lost")
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	if formatted != text {
		t.Errorf("FormatRule() = %q", formatted)
	}
	mem.FreeString(a, formatted)

	required := rule.ParseRule(a, "required.kmk", "deploy {region} : configure")
	defer required.Free()
	if len(required.Diagnostics) != 0 || len(required.Rule.Arguments) != 1 || required.Rule.Arguments[0].Name != "region" || required.Rule.Arguments[0].Optional {
		t.Error("required-only target argument did not parse")
	}
	legacyTemplate := rule.ParseRule(a, "template.kmk", "task {name} : configure")
	defer legacyTemplate.Free()
	if len(legacyTemplate.Diagnostics) != 0 || len(legacyTemplate.Rule.Arguments) != 0 || len(legacyTemplate.Rule.Outputs) != 1 || !legacyTemplate.Rule.Outputs[0].Template {
		t.Error("legacy prefixed target-template output changed interpretation")
	}

	invalid := []string{
		"deploy {name} {name=default} : dep",
		"./out {name} : ./input",
		"deploy {bad name} : dep",
		"deploy {name=two words} : dep",
	}
	for i := range invalid {
		result := rule.ParseRule(a, "invalid.kmk", invalid[i])
		if len(result.Diagnostics) == 0 {
			t.Errorf("invalid target argument declaration accepted: %q", invalid[i])
		}
		result.Free()
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

func TestAlwaysPreservesFileKindOutputsAndFormatting(t *testing.T) {
	a := t.Allocator()
	parsed := rule.ParseRule(a, "always.kmk", "always ./one ./two : ./input\n  cp @< @>\n")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || !parsed.Rule.Always || parsed.Rule.Kind != rule.FileRule || len(parsed.Rule.Outputs) != 2 {
		t.Error("always did not preserve file-rule semantics")
		return
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	if formatted != "always ./one ./two : ./input\n\tcp @< @>" {
		t.Error("always formatter lost the prefix or outputs")
	}
	mem.FreeString(a, formatted)
	bad := rule.ParseRule(a, "bad.kmk", "always named :\n\techo forbidden\n")
	defer bad.Free()
	if len(bad.Diagnostics) == 0 {
		t.Error("always accepted a named task")
	}
	ordinary := rule.ParseRule(a, "ordinary.kmk", "always :\n\techo normal\n")
	defer ordinary.Free()
	if len(ordinary.Diagnostics) != 0 || ordinary.Rule.Always || ordinary.Rule.Kind != rule.TaskRule {
		t.Error("the ordinary target named always stopped working")
	}
}

func TestRuleEnvironmentLiteralMetadataAndRoundTrip(t *testing.T) {
	a := t.Allocator()
	text := "task build : ./input ; env \"MODE=debug\" \"MESSAGE=spaces; equal=ok\\n\" \"MODE=release\"\n\tprintf done"
	parsed := rule.ParseRule(a, "environment.kmk", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Rule.Inputs) != 1 || len(parsed.Rule.Environment) != 3 {
		t.Error("environment metadata was not separated from dependencies")
		return
	}
	if parsed.Rule.Environment[0].Value != "MODE=debug" || parsed.Rule.Environment[1].Value != "MESSAGE=spaces; equal=ok\n" || parsed.Rule.Environment[2].Value != "MODE=release" {
		t.Error("environment literals lost escaping or assignment order")
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	if formatted != text {
		t.Error("environment metadata failed canonical round trip")
	}
	mem.FreeString(a, formatted)
}

func TestRuleEnvironmentRejectsInvalidOrComputedAssignments(t *testing.T) {
	texts := []string{"x : ; env", "x : ; other \"MODE=debug\"", "x : ; env MODE", "x : ; env \"1MODE=debug\"", "x : ; env \"MODE\"", "x : ; env \"MODE=@(value)\"", "x : ; env \"MODE=\\u0000\""}
	for i := range texts {
		parsed := rule.ParseRule(t.Allocator(), "invalid.kmk", texts[i])
		if len(parsed.Diagnostics) == 0 {
			t.Error("invalid environment metadata was accepted")
		}
		parsed.Free()
	}
}
