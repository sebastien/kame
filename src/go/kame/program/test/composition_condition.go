package program_test

import (
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

func TestCompositionConditionUsesOrdinaryTruthAndLazyConfiguration(t *testing.T) {
	a := t.Allocator()
	predicates := []string{":nil", ":false", ":true", "0", "0.0", `""`, "[]", "[key: 1]", "(if :false 1 0 2 3)", "(selected)", "mode"}
	prefix := "mode ?= \"debug\"\n(selected) = mode\nunused = (read \"never read\")\n"
	for i := range predicates {
		result := program.CompositionCondition(a, "condition.kmk", prefix, predicates[i], []string{"mode=release", "future=later"}, []string{"KAME_mode=environment"})
		if result.Diagnostic.Code != "" || result.Selected != (i >= 2) {
			t.Error("condition " + predicates[i] + ": " + result.Diagnostic.Code + " " + result.Diagnostic.Message)
		}
		result.Diagnostic.Free(a)
	}
	result := program.CompositionCondition(a, "condition.kmk", prefix, `(eq mode "environment")`, nil, []string{"KAME_mode=environment"})
	if result.Diagnostic.Code != "" || !result.Selected {
		t.Error("condition lost environment override")
	}
	result.Diagnostic.Free(a)
}

func TestCompositionConditionRejectsEffectsCyclesAndResidualSources(t *testing.T) {
	a := t.Allocator()
	prefixes := []string{"a = b\nb = a\n", "", "", "", "", "(include ./never.km)\n", "(if :true (include ./never.km))\n"}
	predicates := []string{"a", `(sh! "never execute")`, `(out! "never publish")`, `(read "never read")`, "missing", ":true", ":true"}
	codes := []string{"DEP_CYCLE", "CAP_DENIED", "PHASE_INVALID", "CAP_DENIED", "REF_MISSING", "FEATURE_UNSUP", "FEATURE_UNSUP"}
	for i := range prefixes {
		result := program.CompositionCondition(a, "condition.kmk", prefixes[i], predicates[i], nil, nil)
		if result.Diagnostic.Code != codes[i] {
			t.Error("condition " + predicates[i] + ": expected " + codes[i] + " got " + result.Diagnostic.Code)
		}
		if codes[i] == "PHASE_INVALID" && (result.Diagnostic.Message != "configuration conditions must be pure; runtime effects are not allowed" || len(result.Diagnostic.Tips) != 1 || result.Diagnostic.Tips[0] != "move effects into runtime statements or rule recipes") {
			t.Error("effectful condition omitted pure-phase recovery guidance")
		}
		result.Diagnostic.Free(a)
	}
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	defer registry.Free()
	for i := 5; i < len(prefixes); i++ {
		compiled := program.CompileSession(a, []program.Fragment{{Name: "condition.km", Lang: "km", Text: prefixes[i]}}, registry, program.Options{})
		if compiled.Session != nil {
			compiled.Session.Free()
			t.Error("session accepted residual source composition")
		}
		if len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Code != "FEATURE_UNSUP" {
			t.Error("session lost residual composition diagnostic")
		}
		compiled.Free(a)
	}
}

func TestCompositionConditionFramesDoNotClaimSyntheticAuthoredLocations(t *testing.T) {
	a := t.Allocator()
	result := program.CompositionCondition(a, "main.km", "(helper) = (read ./blocked.txt)\n", "(helper)", nil, nil)
	defer result.Diagnostic.Free(a)
	if result.Diagnostic.Code != "CAP_DENIED" || len(result.Diagnostic.Frames) != 2 {
		t.Error("condition lost operation and helper context")
		return
	}
	for i := range result.Diagnostic.Frames {
		frame := result.Diagnostic.Frames[i]
		if frame.Source != "" || frame.Span.Start != 0 || frame.Span.End != 0 {
			t.Error("synthetic configuration offsets were reported as authored")
		}
	}
	if result.Diagnostic.Frames[0].Label != "read" || result.Diagnostic.Frames[1].Label != "helper" {
		t.Error("unqualified frames lost useful labels")
	}
	if result.Diagnostic.Message != "configuration conditions cannot use host capabilities or execute processes" || len(result.Diagnostic.Tips) != 1 || result.Diagnostic.Tips[0] != "select source files using literal configuration and pure expressions; runtime grants do not apply to conditions" {
		t.Error("pure condition failure suggested ineffective runtime grants")
	}
}
