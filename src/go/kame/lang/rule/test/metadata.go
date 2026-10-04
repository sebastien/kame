package rule_test

import (
	"kame/lang/expr"
	"kame/lang/rule"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestRuleMetadataUsesKameRecordExpressions(t *testing.T) {
	a := t.Allocator()
	parsed := rule.ParseRule(a, "test.kmk", "build : ./input ; [shell: kash env: [MODE: (cat \"de\" \"bug\")]]\n\tprintf hello\n")
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

func TestRuleMetadataRejectsUnknownAndDuplicateFields(t *testing.T) {
	invalid := []string{"build : ; [shell: kash shell: kash]", "build : ; [other: 1]", "build : ; [kash]", "build : ; [shell: kash] extra"}
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

func TestServiceMetadataRejectsUnknownAndNonServiceFields(t *testing.T) {
	invalid := []string{
		"service daemon : ; [unknown: 1]\n\ttrue\n",
		"build : ; [ready: [argv: [\"probe\"]]]\n\ttrue\n",
	}
	for i := range invalid {
		parsed := rule.ParseRule(t.Allocator(), "test.kmk", invalid[i])
		if len(parsed.Diagnostics) == 0 {
			t.Error("invalid service metadata was accepted")
		}
		parsed.Free()
	}
}
