package program_test

import (
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

func TestComputedOutputsRetainAuthoredSyntaxAndResolveCaptures(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "computed.kmk", "ROOT = \"./build\"\n@(ROOT)/{name}.o : ./src/{name}.c\n\tcat @< > @>\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{})
	if compiled.Program == nil {
		t.Error("computed output did not compile")
	} else {
		planned := compiled.Program.Plan("./build/demo.o")
		if planned.Diagnostic.Code != "" || len(planned.Plan.Outputs) != 1 || planned.Plan.Outputs[0] != "./build/demo.o" || len(planned.Plan.Inputs) != 1 || planned.Plan.Inputs[0] != "./src/demo.c" {
			t.Error("computed capture was not propagated to inputs and outputs")
		}
		if parsed.Items[1].Rule.Outputs[0].Text != "@(ROOT)/{name}.o" {
			t.Error("resolution overwrote authored syntax")
		}
		planned.Plan.Free(a)
		planned.Diagnostic.Free(a)
		compiled.Program.Free()
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestComputedOutputFailureCleanup(t *testing.T) {
	a := t.Allocator()
	fixtures := []string{
		"@([./one ./one]) :\n",
		"@([]) :\n",
		"ROOT = (out \"forbidden\")\n@(ROOT)/out :\n",
		"@(cat \"./one\") :\n./one :\n",
		"@(cat \"./\" \"{\" \"name\") :\n",
	}
	for i := range fixtures {
		parsed := script.Parse(a, "invalid.kmk", fixtures[i])
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		compiled := program.Compile(a, parsed, registry, program.Options{})
		if compiled.Program != nil || len(compiled.Diagnostics) == 0 {
			t.Error("invalid output exposed an executable program")
		}
		if compiled.Program != nil {
			compiled.Program.Free()
		}
		compiled.Free(a)
		parsed.Free()
		registry.Free()
	}
}
