package program_test

import (
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
)

func TestGeneratedTargetsPlanAndMaterializeWithDefinitionDependencies(t *testing.T) {
	a := t.Allocator()
	source := "MODULES = [\"core\" \"cli\"]\ngenerate module-checks = (map ([module] [kind: \"task\" target: (join (list \"check-\" module) \"\") inputs: [] order-only: [] recipe: [\"true\"]]) MODULES)\n"
	parsed := script.Parse(a, "generated.kmk", source)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Fatal("operation registration failed")
		return
	}
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 {
		message := ""
		for i := range compiled.Diagnostics {
			message += compiled.Diagnostics[i].Code + ":" + compiled.Diagnostics[i].Message + " "
		}
		t.Error("generated rules did not compile: " + message)
		compiled.Free(a)
		parsed.Free()
		registry.Free()
		return
	}
	planned := compiled.Program.Plan("check-core")
	if planned.Diagnostic.Code != "" || planned.Plan.Generator != "module-checks" || len(planned.Plan.GeneratorDependencies) != 1 || planned.Plan.GeneratorDependencies[0] != "MODULES" {
		t.Error("generated rule plan omitted its generator dependency")
	}
	planned.Plan.Free(a)
	planned.Diagnostic.Free(a)
	result := compiled.Program.Materialize("check-cli")
	if result.Diagnostic.Code != "" {
		t.Error("generated task did not materialize: " + result.Diagnostic.Code)
	}
	result.Free(a)
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestGeneratedFileRuleWritesItsDeclaredOutput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-generated-rule-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	source := "generate outputs = [[kind: \"file\" target: \"./generated.txt\" inputs: [] order-only: [] recipe: [\"printf generated > @>\"]]]\n"
	parsed := script.Parse(a, "generated-file.kmk", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 {
		t.Error("generated file rule did not compile")
		compiled.Free(a)
		parsed.Free()
		registry.Free()
		return
	}
	result := compiled.Program.Materialize("./generated.txt")
	if result.Diagnostic.Code != "" {
		t.Error("generated file rule did not materialize: " + result.Diagnostic.Code)
	}
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/generated.txt")
	if readErr != nil || string(data) != "generated" {
		t.Error("generated file rule did not write its declared output")
	}
	mem.FreeSlice(a, data)
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestGeneratedBatchFailureRegistersNoExecutableProgram(t *testing.T) {
	a := t.Allocator()
	source := "generate broken = [[kind: \"task\" target: \"valid\" inputs: [] order-only: [] recipe: [\"true\"]] [kind: \"unknown\" target: \"invalid\" inputs: [] order-only: [] recipe: [\"true\"]]]\n"
	parsed := script.Parse(a, "broken-generated.kmk", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "EXPR_INVALID" || compiled.Diagnostics[0].Source != "broken-generated.kmk" || len(compiled.Diagnostics[0].Notes) != 1 {
		message := ""
		for i := range compiled.Diagnostics {
			message += compiled.Diagnostics[i].Code + ":" + compiled.Diagnostics[i].Message + " "
		}
		t.Error("invalid generated batch did not fail atomically: " + message)
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestGeneratedTargetCannotOverlapAnAuthoredRule(t *testing.T) {
	a := t.Allocator()
	source := "existing :\n\ttrue\ngenerate duplicate = [[kind: \"task\" target: \"existing\" inputs: [] order-only: [] recipe: [\"true\"]]]\n"
	parsed := script.Parse(a, "duplicate-generated.kmk", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "TGT_AMBIG" {
		t.Error("generated target overlapping an authored target was not rejected")
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestGeneratedTargetCannotOverlapAnAuthoredPattern(t *testing.T) {
	a := t.Allocator()
	source := "existing-{suffix} :\n\ttrue\ngenerate duplicate = [[kind: \"task\" target: \"existing-core\" inputs: [] order-only: [] recipe: [\"true\"]]]\n"
	parsed := script.Parse(a, "duplicate-pattern-generated.kmk", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "TGT_AMBIG" {
		t.Error("generated target overlapping an authored pattern was not rejected")
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestGeneratedDeclarationsRejectBuildEffects(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "effect-generated.kmk", "generate forbidden = (write \"generated.txt\" \"content\")\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Fatal("library registration failed")
		return
	}
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Grants: []eval.Grant{{Capability: eval.Write}}})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "PHASE_INVALID" {
		message := ""
		for i := range compiled.Diagnostics {
			message += compiled.Diagnostics[i].Code + ":" + compiled.Diagnostics[i].Message + " "
		}
		t.Error("generated declaration accepted an effectful expression: " + message)
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}
