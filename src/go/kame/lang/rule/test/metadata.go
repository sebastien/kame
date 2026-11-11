package rulecheck

import (
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/expr"
	"kame/lang/rule"
)

func TestRuleMetadataUsesKameRecordExpressions(t *testing.T) {
	a := t.Allocator()
	parsed := rule.ParseRule(a, "test.kmk", "build : ./input ; [shell: kash env: [MODE: (str \"de\" \"bug\")]]\n\tprintf hello\n")
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || parsed.Rule.Metadata == nil || parsed.Rule.Metadata.Kind != expr.Record || len(parsed.Rule.Metadata.Fields) != 2 {
		t.Error("record metadata was not preserved")
		return
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	defer mem.FreeString(a, formatted)
	again := rule.ParseRule(a, "formatted.kmk", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || again.Rule.Metadata == nil || len(again.Rule.Metadata.Fields) != 2 || len(again.Rule.Inputs) != 1 {
		t.Error("formatting changed header metadata or inputs")
	}
}

func TestRuleMetadataAllowsCustomFieldsAndRejectsDuplicates(t *testing.T) {
	accepted := []string{
		`build : ; [name: "cloudflare-login" kind: "task" summary: "Log in to wrangler" custom: [note: "kept"]]`,
		`service daemon : ; [summary: "Run daemon" custom: "value"]`,
	}
	for i := range accepted {
		parsed := rule.ParseRule(t.Allocator(), "test.kmk", accepted[i])
		if len(parsed.Diagnostics) != 0 {
			t.Error("custom metadata field was rejected")
		}
		parsed.Free()
	}
	invalid := []string{"build : ; [shell: kash shell: kash]", "build : ; [summary: \"one\" summary: \"two\"]", "build : ; [kash]", "build : ; [shell: kash] extra"}
	for i := range invalid {
		parsed := rule.ParseRule(t.Allocator(), "test.kmk", invalid[i])
		if len(parsed.Diagnostics) == 0 {
			t.Error("invalid metadata was accepted")
		}
		parsed.Free()
	}
}

func TestServiceMetadataPreservesTypedLifecycleSettings(t *testing.T) {
	a := t.Allocator()
	source := "service daemon : ; [ready: [argv: [\"./daemon\" \"ready\"] interval-ms: 100 timeout-ms: 5000] health: [argv: [\"./daemon\" \"health\"] failures: 2] restart: [attempts: 4 backoff-ms: 50] stop: [grace-ms: 100] log-bytes: 4096]\n\ttrue\n"
	parsed := rule.ParseRule(a, "test.kmk", source)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || parsed.Rule.Kind != rule.ServiceRule || parsed.Rule.Metadata == nil {
		t.Error("typed service lifecycle metadata was rejected")
		return
	}
	formatted := rule.FormatRule(a, parsed.Rule)
	defer mem.FreeString(a, formatted)
	again := rule.ParseRule(a, "formatted.kmk", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || again.Rule.Kind != rule.ServiceRule || again.Rule.Metadata == nil {
		t.Error("formatting changed service lifecycle metadata")
	}
}

func TestServiceMetadataAllowsDocumentationFields(t *testing.T) {
	source := "service daemon : ; [summary: \"Run daemon\" kind: \"service\" name: \"cloudflare-dev\" ready: [argv: [\"./daemon\" \"ready\"]]]\n\ttrue\n"
	parsed := rule.ParseRule(t.Allocator(), "test.kmk", source)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 || parsed.Rule.Kind != rule.ServiceRule {
		t.Error("documentation metadata on a service was rejected")
	}
}
