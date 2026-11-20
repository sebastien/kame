package programcheck

import (
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
)

func TestComputedOutputsRetainAuthoredSyntaxAndResolveCaptures(t *testing.T) {
	a := t.Allocator()
	parsed := script.ParseKameFile(a, "computed.km", "ROOT = \"./build\"\nrule ${ROOT}/{name}.o : ./src/{name}.c\n\tcat $inputs.0 > $outputs.0\n")
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
		if parsed.Items[1].Rule.Outputs[0].Text != "${ROOT}/{name}.o" {
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

func TestComputedPrerequisitePathsResolveCaptures(t *testing.T) {
	a := t.Allocator()
	parsed := script.ParseKameFile(a, "computed.km", "ROOT = \"./src\"\nrule ./build/{path:**}.js : ${ROOT}/js/{path}.js, ${nop ${ROOT}/meta/${path}.json} | ${ROOT}/ready/{_0}\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{})
	if compiled.Program == nil {
		t.Error("computed prerequisites did not compile")
	} else {
		planned := compiled.Program.Plan("./build/widgets/button.js")
		if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 3 {
			t.Error("computed prerequisites did not resolve")
		} else if planned.Plan.Inputs[0] != "./src/js/widgets/button.js" || planned.Plan.Inputs[1] != "./src/meta/widgets/button.json" || planned.Plan.Inputs[2] != "./src/ready/widgets/button" {
			t.Error("computed prerequisites retained literal capture text")
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
		"rule ${nop [./one, ./one]}\n",
		"rule ${nop []}\n",
		"ROOT = (out! \"forbidden\")\nrule ${ROOT}/out\n",
		"rule ${str \"./one\"}\nrule ./one\n",
		"rule ${nop true}\n",
	}
	for i := range fixtures {
		parsed := script.ParseKameFile(a, "invalid.km", fixtures[i])
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
