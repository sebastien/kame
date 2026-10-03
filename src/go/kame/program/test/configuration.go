package program_test

import (
	"kame/core"
	"kame/host/posix"
	"kame/lang/definition"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

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
	if parsed.Items[0].Definition.ValueKind != definition.ValueTemplate || !parsed.Items[0].Definition.Default {
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
