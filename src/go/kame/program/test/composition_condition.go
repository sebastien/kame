package programcheck

import (
	"kame/diagnostic"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
	"strings"
)

func TestCompositionConditionUsesOrdinaryTruthAndLazyConfiguration(t *testing.T) {
	a := t.Allocator()
	predicates := []string{":nil", ":false", ":true", "0", "0.0", `""`, "[]", "[key: 1]", "(if :false 1 0 2 3)", "(selected)", "mode"}
	prefix := "mode ?= \"debug\"\n(selected) = mode\nunused = (read \"never read\")\n"
	for i := range predicates {
		result := program.CompositionCondition(a, "condition.kmk", prefix, predicates[i], []string{"mode=release", "future=later"}, []string{"KAME_mode=environment"}, "")
		if result.Diagnostic.Code != "" || result.Selected != (i >= 2) {
			t.Error("condition " + predicates[i] + ": " + result.Diagnostic.Code + " " + result.Diagnostic.Message)
		}
		result.Diagnostic.Free(a)
	}
	result := program.CompositionCondition(a, "condition.kmk", prefix, `(eq mode "environment")`, nil, []string{"KAME_mode=environment"}, "")
	if result.Diagnostic.Code != "" || !result.Selected {
		t.Error("condition lost environment override")
	}
	result.Diagnostic.Free(a)
}

func TestCompositionConditionUsesEvaluationDirectory(t *testing.T) {
	a := t.Allocator()
	prefix := "root = (abspath ./)\n(helper) = (abspath ./child/..)\n"
	predicates := []string{`(eq (abspath ./) "/workspace/project")`, `(eq root "/workspace/project")`, `(eq (helper) "/workspace/project")`}
	for i := range predicates {
		result := program.CompositionConditionSources(a, []program.CompileSource{{Name: "/sdk/config.kmk", Text: prefix}}, program.CompileSource{Name: "/sdk/main.kmk", Text: predicates[i]}, nil, nil, "/workspace/project")
		if result.Diagnostic.Code != "" || !result.Selected {
			t.Error("condition lost evaluation directory: " + predicates[i] + " " + result.Diagnostic.Code)
		}
		result.Diagnostic.Free(a)
	}
	result := program.DeclarationPredicate(a, "config.kmk", prefix, predicates[1], nil, nil, "/workspace/project")
	if result.Diagnostic.Code != "" || !result.Selected {
		t.Error("declaration predicate lost evaluation directory")
	}
	result.Diagnostic.Free(a)
}

func TestCompositionConditionSourcesLargePrefix(t *testing.T) {
	a := t.Allocator()
	b := strings.Builder{}
	defer b.Reset()
	b.WriteByte('#')
	for i := 0; i < 70000; i++ {
		b.WriteByte('x')
	}
	b.WriteString("\nvalue = (first 1)")
	sources := []program.CompileSource{{Name: "large.km", Text: b.String()}}
	result := program.CompositionConditionSources(a, sources, program.CompileSource{Name: "main.km", Text: "value", Offset: 10}, nil, nil, "")
	defer result.Diagnostic.Free(a)
	if result.Diagnostic.Code != "EXPR_INVALID" || result.Diagnostic.Source != "large.km" || result.Diagnostic.Span.Start != 70017 {
		t.Error("large configuration prefix lost its authored origin")
	}
}

func TestCompositionConditionSourcesRetainAuthoredLocations(t *testing.T) {
	a := t.Allocator()
	sources := []program.CompileSource{
		{Name: "modules.km", Text: "AVAILABLE = (sub {_0} \"x\" \"y\")", Offset: 40, Includes: []diagnostic.Frame{{Kind: "include", Label: "modules.km", Source: "main.km", Span: diagnostic.Span{Start: 5, End: 25}}}},
		{Name: "modules.km", Text: "MODULES = AVAILABLE", Offset: 80},
		{Name: "helpers.km", Text: "(selected) = MODULES", Offset: 12},
	}
	result := program.CompositionConditionSources(a, sources, program.CompileSource{Name: "main.km", Text: "(selected)", Offset: 100}, nil, nil, "")
	defer result.Diagnostic.Free(a)
	d := result.Diagnostic
	if d.Code != "PAT_INVALID" || d.Source != "modules.km" || d.Span.Start != 57 {
		t.Error("condition did not retain the failing operation's authored origin: " + d.Code + " " + d.Source)
	}
	if len(d.Frames) != 6 {
		t.Error("condition lost operation, definitions, call, include or selecting condition")
		return
	}
	labels := []string{"sub", "AVAILABLE", "MODULES", "selected", "source selection", "modules.km"}
	names := []string{"modules.km", "modules.km", "modules.km", "main.km", "main.km", "main.km"}
	starts := []int{52, 40, 80, 100, 100, 5}
	for i := range d.Frames {
		f := d.Frames[i]
		if f.Label != labels[i] || f.Source != names[i] || f.Span.Start != starts[i] {
			t.Error("incorrect authored backtrace frame: " + f.Label + " " + f.Source)
		}
	}
}

func TestCompositionConditionSourcesKeepPredicateAndDeclarationOffsetsSeparate(t *testing.T) {
	a := t.Allocator()
	sources := []program.CompileSource{{Name: "first.km", Text: "unused = 42", Offset: 20}}
	result := program.CompositionConditionSources(a, sources, program.CompileSource{Name: "second.km", Text: "(first 1)", Offset: 70}, nil, nil, "")
	if result.Diagnostic.Code != "EXPR_INVALID" || result.Diagnostic.Source != "second.km" || result.Diagnostic.Span.Start != 77 || result.Diagnostic.Span.End != 78 {
		t.Error("predicate operand resolved against prefix offsets")
	}
	result.Diagnostic.Free(a)
	result = program.CompositionConditionSources(a, nil, program.CompileSource{Name: "second.km", Text: "(read ./blocked)", Offset: 90}, nil, nil, "")
	if result.Diagnostic.Code != "CAP_DENIED" || result.Diagnostic.Source != "second.km" || result.Diagnostic.Span.Start != 90 || len(result.Diagnostic.Tips) != 1 {
		t.Error("direct pure-condition failure lost origin or guidance")
	}
	result.Diagnostic.Free(a)
	result = program.CompositionConditionSources(a, sources, program.CompileSource{Name: "second.km", Text: "(", Offset: 70}, nil, nil, "")
	if result.Diagnostic.Code != "PARSE_ERR" || result.Diagnostic.Source != "second.km" || result.Diagnostic.Span.Start < 70 {
		t.Error("predicate parse error lost authored origin")
	}
	result.Diagnostic.Free(a)
	sources = []program.CompileSource{{Name: "first.km", Text: "value = 1", Offset: 20}, {Name: "second.km", Text: "value = 2", Offset: 70}}
	result = program.CompositionConditionSources(a, sources, program.CompileSource{Name: "third.km", Text: "value", Offset: 100}, nil, nil, "")
	if result.Diagnostic.Code != "DEF_INVALID" || result.Diagnostic.Source != "second.km" || result.Diagnostic.Span.Start != 70 || len(result.Diagnostic.Related) != 1 || result.Diagnostic.Related[0].Source != "first.km" {
		t.Error("condition compilation conflict lost authored origins")
	}
	result.Diagnostic.Free(a)
}

func TestCompositionConditionRejectsEffectsCyclesAndResidualSources(t *testing.T) {
	a := t.Allocator()
	prefixes := []string{"a = b\nb = a\n", "", "", "", "", "(include ./never.km)\n", "(if :true (include ./never.km))\n"}
	predicates := []string{"a", `(sh! "never execute")`, `(out! "never publish")`, `(read "never read")`, "missing", ":true", ":true"}
	codes := []string{"DEP_CYCLE", "CAP_DENIED", "PHASE_INVALID", "CAP_DENIED", "REF_MISSING", "FEATURE_UNSUP", "FEATURE_UNSUP"}
	for i := range prefixes {
		result := program.CompositionCondition(a, "condition.kmk", prefixes[i], predicates[i], nil, nil, "")
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
	result := program.CompositionCondition(a, "main.km", "(helper) = (read ./blocked.txt)\n", "(helper)", nil, nil, "")
	defer result.Diagnostic.Free(a)
	if result.Diagnostic.Code != "CAP_DENIED" || len(result.Diagnostic.Frames) != 2 {
		t.Error("condition lost operation and helper context")
		return
	}
	if result.Diagnostic.Source != "" || result.Diagnostic.Span.Start != 0 || result.Diagnostic.Span.End != 0 {
		t.Error("synthetic configuration primary claimed an authored location")
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
