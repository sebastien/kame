package program_test

import (
	"kame/core"
	"kame/host/posix"
	"kame/lang/expr"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

func TestDefaultTargetNormalizationOwnsReplacementOperands(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "task default {mode=debug} :\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{})
	if compiled.Program == nil { t.Error("compile failed"); compiled.Free(a); parsed.Free(); registry.Free(); return }
	targets := compiled.Program.NormalizeTargetOperands([]string{"mode=release"})
	if len(targets) != 1 || targets[0] != "default mode=release" { t.Error("assignment-only invocation did not normalize to default") }
	program.FreeStrings(a, targets)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBuildConfigurationOverridesAreLiteralOwnedAndVisible(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "config.kmk", "mode ?= \"default\"\nmode ?= (read \"not-evaluated\")\ntask default :\n\t@(out mode)\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	overrides := []string{"mode=first", "mode=@(missing)=literal"}
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Defines: overrides, Environment: []string{"KAME_mode=environment"}})
	if compiled.Program == nil {
		t.Error("configuration compile failed")
		compiled.Free(a)
		parsed.Free()
		registry.Free()
		return
	}
	p := compiled.Program
	result := p.Materialize("mode")
	if result.Diagnostic.Code != "" || result.Value.Kind != core.String || result.Value.Text != "@(missing)=literal" {
		t.Error("configuration precedence or literal ownership failed")
	}
	result.Free(a)
	plan := p.Plan("default")
	if plan.Diagnostic.Code != "" || len(plan.Plan.Configuration) != 1 || plan.Plan.Configuration[0] != "mode=@(missing)=literal" {
		t.Error("effective configuration missing from plan")
	}
	plan.Plan.Free(a)
	if parsed.Items[0].Definition.Expression.Kind != expr.String || !parsed.Items[0].Definition.Default {
		t.Error("override mutated authored definition")
	}
	p.Free()
	compiled.Free(a)
	compiled = program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Environment: []string{"KAME_mode=environment", "KAME_unknown=ignored"}})
	if compiled.Program == nil {
		t.Error("environment override failed")
		compiled.Free(a)
		parsed.Free()
		registry.Free()
		return
	}
	result = compiled.Program.Materialize("mode")
	if result.Diagnostic.Code != "" || result.Value.Text != "environment" {
		t.Error("environment convention not applied")
	}
	result.Free(a)
	compiled.Program.Free()
	compiled.Free(a)
	compiled = program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Defines: []string{"unknown=value"}})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "DEF_INVALID" {
		t.Error("unknown explicit override accepted")
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestAssignmentShapedTargetsValidateAfterRegistration(t *testing.T) {
	a := t.Allocator()
	fixtures := []string{
		"@(\"./mode=debug\") :\n\ttrue\n",
		"generate outputs = [[kind: \"file\" target: \"./mode=debug\" inputs: [] order-only: [] recipe: [\"true\"]]]\n",
	}
	for i := range fixtures {
		parsed := script.Parse(a, "assignment-target.kmk", fixtures[i])
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		compiled := program.Compile(a, parsed, registry, program.Options{Parameters: []string{"./mode=debug"}})
		if compiled.Program == nil {
			t.Error("assignment-shaped target rejected: " + fixtures[i])
		} else {
			joined := compiled.Program.NormalizeTargetOperands([]string{"./mode=debug"})
			if len(joined) != 1 || joined[0] != "./mode=debug" {
				t.Error("assignment-shaped target was consumed as configuration")
			}
			program.FreeStrings(a, joined)
			planned := compiled.Program.Plan("./mode=debug")
			if planned.Diagnostic.Code != "" || len(planned.Plan.Outputs) != 1 || planned.Plan.Outputs[0] != "./mode=debug" {
				t.Error("registered assignment-shaped target did not plan")
			}
			planned.Plan.Free(a)
			planned.Diagnostic.Free(a)
			compiled.Program.Free()
		}
		compiled.Free(a)
		parsed.Free()
		registry.Free()
	}
}

func TestParameterOverridesApplyBeforeOutputAndGeneratorEvaluation(t *testing.T) {
	a := t.Allocator()
	fixtures := []string{
		"@(str \"./mode=\" mode) :\n\ttrue\n",
		"generate outputs = [[kind: \"file\" target: (str \"./mode=\" mode) inputs: [] order-only: [] recipe: [\"true\"]]]\n",
	}
	for i := range fixtures {
		parsed := script.Parse(a, "parameter-generation.kmk", "mode = (read \"not-evaluated\")\n"+fixtures[i])
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		parameters := []string{"mode=debug", "./mode=debug"}
		compiled := program.Compile(a, parsed, registry, program.Options{Defines: []string{"mode=explicit"}, Environment: []string{"KAME_mode=environment"}, Parameters: parameters})
		if compiled.Program == nil {
			t.Error("definition parameter was not applied before rule registration")
		} else {
			p := compiled.Program
			if !p.HasTarget("./mode=debug") || len(p.Configuration) != 1 || p.Configuration[0] != "mode=debug" {
				t.Error("definition parameter precedence was not visible during registration")
			}
			joined := p.NormalizeTargetOperands(parameters)
			if len(joined) != 1 || joined[0] != "./mode=debug" {
				t.Error("definition override and target operands were not separated")
			}
			program.FreeStrings(a, joined)
			p.Free()
		}
		compiled.Free(a)
		parsed.Free()
		registry.Free()
	}
}

func TestUnknownParameterFailsCompilationAfterGeneration(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "unknown-parameter.kmk", "mode = (read \"not-evaluated\")\ngenerate outputs = [[kind: \"file\" target: (str \"./mode=\" mode) inputs: [] order-only: [] recipe: [\"exit 99\"]]]\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Parameters: []string{"mode=debug", "./mode=debug", "unknown=value"}})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "TGT_ARGUMENT" || compiled.Diagnostics[0].Message != "unknown parameter: unknown" {
		t.Error("unknown parameter did not fail before exposing an executable program")
	}
	if compiled.Program != nil { compiled.Program.Free() }
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}
