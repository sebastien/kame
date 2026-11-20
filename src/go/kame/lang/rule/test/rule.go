package rulecheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/rule"
	"kame/lang/source"
)

func sameSpan(span source.Span, start int, end int) bool {
	return span.Start == start && span.End == end
}

func TestComputedSequenceGroups(t *testing.T) {
	a := t.Allocator()
	text := "task a : ${PREP}, ${BUILD} | ${TOOLS}, ./ready"
	parsed := rule.ParseRule(a, "groups.km", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Rule.Inputs) != 4 {
		t.Error("computed sequence did not parse")
		return
	}
	if !parsed.Rule.Inputs[0].SequenceEnd || parsed.Rule.Inputs[1].SequenceEnd || !parsed.Rule.Inputs[2].SequenceEnd || !parsed.Rule.Inputs[3].OrderOnly {
		t.Error("computed sequence lost boundaries/purpose")
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	if formatted != text {
		t.Error("computed sequence did not round trip")
	}
	mem.FreeString(a, formatted)
}

func TestComputedPrerequisitePathsRoundTrip(t *testing.T) {
	a := t.Allocator()
	text := "rule ./build/{path:**}.js : ${ROOT}/js/{path}.js, ./meta/${MODE}/{path}.json | ${str ROOT \"/ready\"}/{_0}"
	parsed := rule.ParseRule(a, "paths.km", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Rule.Inputs) != 3 {
		t.Error("computed prerequisite paths did not parse")
		return
	}
	for i := range parsed.Rule.Inputs {
		if parsed.Rule.Inputs[i].Kind != rule.InputExpression {
			t.Error("computed path bypassed expression parsing")
		}
	}
	if !parsed.Rule.Inputs[0].SequenceEnd || !parsed.Rule.Inputs[2].OrderOnly {
		t.Error("computed path lost prerequisite grouping")
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	if formatted != text {
		t.Error("computed prerequisite paths did not round trip")
	}
	mem.FreeString(a, formatted)
}

func TestComputedOutputsPreserveExpressionsAndFormatting(t *testing.T) {
	a := t.Allocator()
	text := "rule ${ROOT}/{name}.o ${OUTPUTS} (output-path name) : ./seed\n\tcat $inputs.0 > $outputs.0"
	parsed := rule.ParseRule(a, "computed.km", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Rule.Outputs) != 3 {
		t.Error("computed outputs did not parse")
		return
	}
	if parsed.Rule.Outputs[0].Kind != rule.TargetString || parsed.Rule.Outputs[1].Kind != rule.TargetExpression || parsed.Rule.Outputs[0].Expansion == nil || parsed.Rule.Outputs[1].Expansion == nil {
		t.Error("output interpolation and list-splicing expressions were not distinguished")
	}
	parenthesized := parsed.Rule.Outputs[2]
	if parenthesized.Kind != rule.TargetExpression || parenthesized.Expansion == nil || parenthesized.Expansion.Parts[0].Expr == nil {
		t.Error("parenthesized output did not retain its expression")
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	if formatted != text {
		t.Error("formatting changed authored output expressions")
	}
	mem.FreeString(a, formatted)
	for _, invalid := range []string{"rule ()", "rule (name).path", "rule (name)(other)", "task (name)", "service (name)"} {
		parsed := rule.ParseRule(a, "invalid.km", invalid)
		if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("expected a parse error for %q", invalid)
		}
		parsed.Free()
	}
}

func TestFileRuleParsesRecipeIndentation(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "rule ./out : ./in dep\n\techo ${inputs.0}\n\t  nested")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Rule == nil || result.Rule.Kind != rule.FileRule || len(result.Rule.Outputs) != 1 || len(result.Rule.Inputs) != 2 || len(result.Rule.Body) != 2 {
		t.Error("file rule did not parse header and recipe body")
		return
	}
	if result.Rule.Body[0].Text != "echo ${inputs.0}" || result.Rule.Body[1].Text != "  nested" || !sameSpan(result.Rule.Body[0].Span, 23, 39) {
		t.Error("recipe prefix removal or line span was incorrect")
	}
}

func TestNamedAndPrefixedRulesClassify(t *testing.T) {
	task := rule.ParseRule(t.Allocator(), "test.km", "task default : dep")
	defer task.Free()
	cached := rule.ParseRule(t.Allocator(), "test.km", "task cached build : dep")
	defer cached.Free()
	service := rule.ParseRule(t.Allocator(), "test.km", "service dev : dep")
	defer service.Free()
	if task.Rule.Kind != rule.TaskRule || cached.Rule.Kind != rule.CachedTaskRule || service.Rule.Kind != rule.ServiceRule {
		t.Error("named, cached, and service rules were not classified")
	}
}

func TestNamedTaskArgumentsParseFormatAndValidate(t *testing.T) {
	a := t.Allocator()
	text := "task cached deploy {environment} {region=us-east} : configure\n\tship ${environment} ${region}"
	parsed := rule.ParseRule(a, "arguments.km", text)
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

	required := rule.ParseRule(a, "required.km", "task deploy {region} : configure")
	defer required.Free()
	if len(required.Diagnostics) != 0 || len(required.Rule.Arguments) != 1 || required.Rule.Arguments[0].Name != "region" || required.Rule.Arguments[0].Optional {
		t.Error("required-only target argument did not parse")
	}
	templated := rule.ParseRule(a, "template.km", "task {name} : configure")
	defer templated.Free()
	if len(templated.Diagnostics) != 0 || len(templated.Rule.Arguments) != 0 || len(templated.Rule.Outputs) != 1 || !templated.Rule.Outputs[0].Template {
		t.Error("target-template output changed interpretation")
	}

	serviceText := "service dev {port=8080} : configure"
	service := rule.ParseRule(a, "service.km", serviceText)
	defer service.Free()
	if len(service.Diagnostics) != 0 || service.Rule.Kind != rule.ServiceRule || len(service.Rule.Arguments) != 1 || service.Rule.Arguments[0].Default != "8080" {
		t.Errorf("service arguments did not parse: %v", service.Diagnostics)
	}
	serviceFormat := rule.FormatRule(a, service.Rule)
	defer mem.FreeString(a, serviceFormat)
	if serviceFormat != serviceText {
		t.Errorf("service argument formatting changed: %q", serviceFormat)
	}

	invalid := []string{
		"task deploy {name} {name=default} : dep",
		"rule ./out {name} : ./input",
		"task deploy {bad name} : dep",
		"task deploy {name=two words} : dep",
	}
	for i := range invalid {
		result := rule.ParseRule(a, "invalid.km", invalid[i])
		if len(result.Diagnostics) == 0 {
			t.Errorf("invalid target argument declaration accepted: %q", invalid[i])
		}
		result.Free()
	}
}

func TestFormatRuleCanonicalizesWhitespace(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "rule ./out : ./in\n  echo ok")
	defer result.Free()
	formatted := rule.FormatRule(t.Allocator(), result.Rule)
	if formatted != "rule ./out : ./in\n\techo ok" {
		t.Errorf("FormatRule() = %q", formatted)
	}
	mem.FreeString(t.Allocator(), formatted)
}

func TestFormatRulePreservesRecipeBlankLines(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "rule ./out : ./in\n  echo one\n \t\n  echo two")
	defer result.Free()
	if len(result.Diagnostics) != 0 {
		t.Fatalf("parse diagnostics: %d", len(result.Diagnostics))
	}
	formatted := rule.FormatRule(t.Allocator(), result.Rule)
	if formatted != "rule ./out : ./in\n\techo one\n\n\techo two" {
		t.Errorf("FormatRule() = %q", formatted)
	}
	mem.FreeString(t.Allocator(), formatted)
	if len(result.Rule.Body) != 3 || result.Rule.Body[1].Text != "" || result.Rule.Body[1].Template != nil || !sameSpan(result.Rule.Body[1].Span, 29, 31) {
		t.Errorf("blank recipe line body=%d text=%q template=%t span=%d:%d", len(result.Rule.Body), result.Rule.Body[1].Text, result.Rule.Body[1].Template != nil, result.Rule.Body[1].Span.Start, result.Rule.Body[1].Span.End)
	}
}

func TestRuleHeaderBalancesQuotedAndExpressionInputs(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "rule \"./build dir/out\" : \"literal input\" ${files ./src/*.c}")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Rule.Kind != rule.FileRule || len(result.Rule.Inputs) != 2 || result.Rule.Inputs[0].Kind != rule.InputString || result.Rule.Inputs[0].Template == nil || result.Rule.Inputs[1].Kind != rule.InputExpression || result.Rule.Inputs[1].Template == nil {
		t.Error("rule header split quoted or expression input")
	}
}

func TestQuotedOperandsDecodeWithoutChangingAuthoredText(t *testing.T) {
	a := t.Allocator()
	text := "rule \"./out \\\"quote\" : \"./in\\tfile\""
	parsed := rule.ParseRule(a, "quoted.km", text)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || len(parsed.Rule.Outputs) != 1 || len(parsed.Rule.Inputs) != 1 {
		t.Fatal("quoted operands did not parse")
		return
	}
	output := parsed.Rule.Outputs[0]
	if !output.Path || output.Expansion == nil || output.Expansion.Parts[0].Text != "./out \"quote" {
		t.Error("quoted output lost its decoded path")
	}
	input, literal := rule.LiteralInput(parsed.Rule.Inputs[0])
	if !literal || input != "./in\tfile" {
		t.Error("quoted prerequisite was not recognized as decoded literal text")
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	defer mem.FreeString(a, formatted)
	if formatted != text {
		t.Error("formatting changed quoted spelling")
	}
	dynamic := rule.ParseRule(a, "dynamic.km", "rule ./out : \"${ROOT}/input\"")
	defer dynamic.Free()
	if _, literal := rule.LiteralInput(dynamic.Rule.Inputs[0]); literal {
		t.Error("interpolated prerequisite was treated as static")
	}
}

func TestQuotedCapturesKeepDecodedBackslashesAndAuthoredSpans(t *testing.T) {
	a := t.Allocator()
	texts := []string{`rule "./out\\dir/{name}.o"`, `rule "./out\t{name}.o"`, `rule "./out\\{name}.o"`, `rule "./out\"/{name:~[a-z]+}.o"`}
	targets := []string{"./out\\dir/demo.o", "./out\tdemo.o", "./out\\demo.o", "./out\"/demo.o"}
	starts := []int{12, 8, 8, 9}
	ends := []int{18, 14, 14, 23}
	for i := range texts {
		parsed := rule.ParseRule(a, "quoted.km", texts[i])
		if len(parsed.Diagnostics) != 0 || len(parsed.Rule.Outputs) != 1 || parsed.Rule.Outputs[0].TargetForm == nil {
			t.Error("quoted capture did not parse")
			parsed.Free()
			continue
		}
		form := parsed.Rule.Outputs[0].TargetForm
		// Target spans are relative to the authored quoted content, not decoded text.
		if !sameSpan(form.Parts[1].Span, starts[i]-1, ends[i]-1) {
			t.Errorf("capture span = %d:%d", form.Parts[1].Span.Start+1, form.Parts[1].Span.End+1)
		}
		match := form.MatchTarget(a, targets[i])
		if match == nil || len(match.Captures) != 1 || match.Captures[0].Text != "demo" {
			t.Error("quoted capture changed decoded matching text")
		}
		match.Free(a)
		formatted := rule.FormatRule(a, parsed.Rule)
		if formatted != texts[i] {
			t.Error("quoted capture did not retain authored formatting")
		}
		mem.FreeString(a, formatted)
		parsed.Free()
	}
	invalid := rule.ParseRule(a, "invalid.km", `rule "./out\t{name"`)
	defer invalid.Free()
	if len(invalid.Diagnostics) != 1 || !sameSpan(invalid.Diagnostics[0].Span, 13, 18) {
		t.Error("quoted capture diagnostic lost authored coordinates")
	}
}

func TestRecipeTemplateRecoversMalformedExpansion(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "task default : dep\n\techo @{name")
	defer result.Free()
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Severity != source.Error || result.Rule.Body[0].Template == nil {
		t.Error("recipe template did not recover malformed interpolation")
	}
}

func TestRuleRetainsTargetFormAndNormalizesCRLFRecipe(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "rule ./{name:*}.o : ./{name:*}.c\r\n\techo $inputs.0\r\n")
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Rule.Outputs[0].TargetForm == nil || result.Rule.Inputs[0].TargetForm == nil || len(result.Rule.Body) != 1 || result.Rule.Body[0].Text != "echo $inputs.0" {
		t.Errorf("rule forms=%t/%t body=%d diagnostics=%d", result.Rule.Outputs[0].TargetForm != nil, result.Rule.Inputs[0].TargetForm != nil, len(result.Rule.Body), len(result.Diagnostics))
	}
}

func TestWildcardInputRetainsAuthoredToken(t *testing.T) {
	result := rule.ParseRule(t.Allocator(), "test.km", "task default : ./src/**/*.c \"./literal*.c\"\n\techo ok")
	defer result.Free()
	if len(result.Diagnostics) != 0 || len(result.Rule.Inputs) != 2 || result.Rule.Inputs[0].Kind != rule.InputWildcard || result.Rule.Inputs[0].Template == nil || result.Rule.Inputs[1].Kind != rule.InputString {
		t.Error("wildcard input classification or quoted literal changed")
	}
}

func TestAlwaysPreservesFileKindOutputsAndFormatting(t *testing.T) {
	a := t.Allocator()
	parsed := rule.ParseRule(a, "always.km", "rule always ./one ./two : ./input\n  cp $inputs.0 $outputs.0\n")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || !parsed.Rule.Always || parsed.Rule.Kind != rule.FileRule || len(parsed.Rule.Outputs) != 2 {
		t.Error("always did not preserve file-rule semantics")
		return
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	if formatted != "rule always ./one ./two : ./input\n\tcp $inputs.0 $outputs.0" {
		t.Error("always formatter lost the prefix or outputs")
	}
	mem.FreeString(a, formatted)
	bad := rule.ParseRule(a, "bad.km", "rule always named :\n\techo forbidden\n")
	defer bad.Free()
	if len(bad.Diagnostics) == 0 {
		t.Error("always accepted a named task")
	}
	ordinary := rule.ParseRule(a, "ordinary.km", "task always\n\techo normal\n")
	defer ordinary.Free()
	if len(ordinary.Diagnostics) != 0 || ordinary.Rule.Always || ordinary.Rule.Kind != rule.TaskRule {
		t.Error("the ordinary target named always stopped working")
	}
}

func TestRuleRejectsNonRecordEnvironmentSuffix(t *testing.T) {
	texts := []string{"task x ; env", "task x ; other \"MODE=debug\"", "task x ; env MODE", "task x ; env \"1MODE=debug\"", "task x ; env \"MODE\"", "task x ; env \"MODE=${value ()}\"", "task x ; env \"MODE=\\u0000\""}
	for i := range texts {
		parsed := rule.ParseRule(t.Allocator(), "invalid.km", texts[i])
		if len(parsed.Diagnostics) == 0 {
			t.Error("invalid environment metadata was accepted")
		}
		parsed.Free()
	}
}
