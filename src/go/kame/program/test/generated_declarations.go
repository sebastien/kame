package programcheck

import (
	"kame/host/posix"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"os"
	"slices"
	"strconv"
	"strings"
)

const generatedRuleLimitForTest = 4096
const generatedListItemLimitForTest = 4096

func TestGeneratedTargetsPlanAndMaterializeWithDefinitionDependencies(t *testing.T) {
	a := t.Allocator()

	dir, err := os.MkdirTemp("", "kame-generated-task-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	source := "MODULES = [\"core\", \"cli\"]\ndef check module =\n\ttask check-${module}\n\t\t(write! ./generated-${module} \"\")\ngenerate map check MODULES\n"
	parsed := script.ParseKameFile(a, "generated.km", source)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Fatal("operation registration failed")
		return
	}
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Write}}})
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
	if planned.Diagnostic.Code != "" || planned.Plan.Generator != "generate" || !slices.Contains(planned.Plan.GeneratorDependencies, "MODULES") {
		t.Error("generated rule plan omitted its generator dependency")
	}
	planned.Plan.Free(a)
	planned.Diagnostic.Free(a)
	result := compiled.Program.Materialize("check-cli")
	if result.Diagnostic.Code != "" {
		t.Error("generated task did not materialize: " + result.Diagnostic.Code)
	}
	result.Free(a)
	data, readErr := os.ReadFile(dir + "/generated-cli")
	if readErr != nil || len(data) != 0 {
		t.Error("generated recipe did not retain its computed module value")
	}
	mem.FreeSlice(a, data)
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestGeneratedFileRuleWritesItsDeclaredOutput(t *testing.T) {
	a := t.Allocator()

	dir, err := os.MkdirTemp("", "kame-generated-rule-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	source := "def output () =\n\trule ./generated.txt\n\t\treturn \"generated\"\ngenerate output ()\n"
	parsed := script.ParseKameFile(a, "generated-file.km", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Write}}})
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
	data, readErr := os.ReadFile(dir + "/generated.txt")
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
	source := "generate [(task valid), \"invalid\"]\n"
	parsed := script.ParseKameFile(a, "broken-generated.km", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "EXPR_INVALID" || compiled.Diagnostics[0].Source != "broken-generated.km" || compiled.Diagnostics[0].Span.Start != 0 || compiled.Diagnostics[0].Span.End != len(source)-1 {
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
	source := "task existing\ngenerate (task existing)\n"
	parsed := script.ParseKameFile(a, "duplicate-generated.km", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "TGT_AMBIG" {
		t.Error("generated target overlapping an authored target was not rejected")
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestGeneratedAndAuthoredPatternsReportAmbiguousSelection(t *testing.T) {
	a := t.Allocator()
	source := "task existing-{suffix}\ngenerate (task existing-{name})\n"
	parsed := script.ParseKameFile(a, "duplicate-pattern-generated.km", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program == nil {
		t.Error("generated pattern failed to register")
	} else {
		planned := compiled.Program.Plan("existing-core")
		if planned.Diagnostic.Code != "TGT_AMBIG" {
			t.Error("overlapping patterns did not report ambiguous selection")
		}
		planned.Plan.Free(a)
		planned.Diagnostic.Free(a)
		compiled.Program.Free()
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestGeneratedDeclarationsRejectBuildEffects(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	if !operations.Register(registry) {
		t.Fatal("library registration failed")
	}
	effects := []string{
		`write! ./generated.txt "content"`,
		`run! "printf" "content"`,
		`$(printf content)`,
		`read ./generated.txt`,
	}
	grants := []eval.Grant{{Capability: eval.Write}, {Capability: eval.Run}, {Capability: eval.Read}}
	for _, effect := range effects {
		parsed := script.ParseKameFile(a, "effect-generated.km", "generate "+effect+"\n")
		compiled := program.Compile(a, parsed, registry, program.Options{Directory: t.TempDir(), Host: posix.New(a), Grants: grants})
		if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "PHASE_INVALID" {
			t.Errorf("generate %s returned %v, want PHASE_INVALID", effect, compiled.Diagnostics)
		}
		if compiled.Program != nil {
			compiled.Program.Free()
		}
		compiled.Free(a)
		parsed.Free()
	}
}

func TestGeneratedDeclarationRejectsDefinitionCycles(t *testing.T) {
	a := t.Allocator()
	text := "MODULES = OTHER\nOTHER = MODULES\ndef make-task module = (task ${module})\ngenerate map make-task MODULES\n"
	parsed := script.ParseKameFile(a, "cycle-generated.km", text)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Fatal("library registration failed")
		return
	}
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 || compiled.Diagnostics[0].Code != "DEP_CYCLE" {
		message := ""
		for i := range compiled.Diagnostics {
			message += compiled.Diagnostics[i].Code + ":" + compiled.Diagnostics[i].Message + " "
		}
		t.Error("generated declaration did not reject a definition cycle: " + message)
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func checkGeneratedLimit(t *testing.T, name, source string, excess int, expectedMessage string) {
	a := t.Allocator()
	parsed := script.ParseKameFile(a, name+".km", source)
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	compiled := program.Compile(a, parsed, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Program != nil {
		defer compiled.Program.Free()
	}
	if excess == 0 {
		if compiled.Program == nil || len(compiled.Diagnostics) != 0 {
			t.Errorf("%s failed at its limit: %v", name, compiled.Diagnostics)
		}
		return
	}
	if compiled.Program != nil || len(compiled.Diagnostics) != 1 {
		t.Errorf("%s did not reject the whole batch: %v", name, compiled.Diagnostics)
		return
	}
	d := compiled.Diagnostics[0]
	if d.Code != "EXPR_INVALID" || !strings.Contains(d.Message, expectedMessage) ||
		d.Source != name+".km" || d.Span.Start != 0 || d.Span.End != len(source)-1 {
		t.Errorf("%s lost its generate location or limit diagnostic: %v", name, d)
	}
}

func TestGeneratedDeclarationEnforcesBatchRuleLimit(t *testing.T) {
	for excess := range 2 {
		var source strings.Builder
		source.WriteString("generate [")
		for i := range generatedRuleLimitForTest + excess {
			if i != 0 {
				source.WriteString(", ")
			}
			source.WriteString("(task generated-")
			source.WriteString(strconv.Itoa(i))
			source.WriteByte(')')
		}
		source.WriteString("]\n")
		checkGeneratedLimit(t, "rule-count", source.String(), excess, "declaration limit")
	}
}

func TestGeneratedDeclarationEnforcesBatchListItemLimit(t *testing.T) {
	for excess := range 2 {
		// Both normal and order-only items count toward the same limit.
		source := "generate (task many :" + strings.Repeat(" ./input", generatedListItemLimitForTest-1+excess) + " | ./order)\n"
		checkGeneratedLimit(t, "input-count", source, excess, "input limit")
	}
}

func TestGeneratedDeclarationEnforcesTextLimits(t *testing.T) {
	const fieldLimit = 64 << 10
	const batchLimit = 1 << 20
	const target = "batch"
	for excess := range 2 {
		source := "generate (task " + strings.Repeat("x", fieldLimit+excess) + ")\n"
		checkGeneratedLimit(t, "target-size", source, excess, "retained data")
		path := "./" + strings.Repeat("x", fieldLimit-2+excess)
		checkGeneratedLimit(t, "output-size", "generate (rule "+path+")\n", excess, "retained data")
		checkGeneratedLimit(t, "input-size", "generate (task consume : "+path+")\n", excess, "retained data")
		const metadata = `{description: ""}`
		fieldText := strings.Repeat("x", fieldLimit-len(metadata)+excess)
		source = "generate (task describe ; {description: \"" + fieldText + "\"})\n"
		checkGeneratedLimit(t, "metadata-size", source, excess, "retained data")
		field := " ./" + strings.Repeat("x", fieldLimit-2)
		firstField := field[:len(field)-len(target)+excess]
		source = "generate (task " + target + " :" + firstField + strings.Repeat(field, batchLimit/fieldLimit-1) + ")\n"
		checkGeneratedLimit(t, "retained-size", source, excess, "retained data")
	}
}

func TestGeneratedTargetSetIsReplacedWhenSourceChanges(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Fatal("library registration failed")
		return
	}
	firstSource := "MODULES = [\"first\"]\ndef make-task module = (task ${module})\ngenerate map make-task MODULES\n"
	firstParsed := script.ParseKameFile(a, "first.km", firstSource)
	first := program.Compile(a, firstParsed, registry, program.Options{Host: posix.New(a)})
	if first.Program == nil || len(first.Diagnostics) != 0 {
		t.Error("first generated source failed to compile")
		first.Free(a)
		firstParsed.Free()
		registry.Free()
		return
	}
	firstPlan := first.Program.Plan("first")
	if firstPlan.Plan.Rule == nil || firstPlan.Diagnostic.Code != "" {
		t.Error("first generated target is missing")
	}
	firstPlan.Plan.Free(a)
	firstPlan.Diagnostic.Free(a)
	first.Program.Free()
	first.Free(a)
	firstParsed.Free()
	secondSource := "MODULES = [\"second\"]\ndef make-task module = (task ${module})\ngenerate map make-task MODULES\n"
	secondParsed := script.ParseKameFile(a, "second.km", secondSource)
	second := program.Compile(a, secondParsed, registry, program.Options{Host: posix.New(a)})
	if second.Program == nil || len(second.Diagnostics) != 0 {
		t.Error("replacement generated source failed to compile")
		second.Free(a)
		secondParsed.Free()
		registry.Free()
		return
	}
	secondPlan := second.Program.Plan("second")
	oldPlan := second.Program.Plan("first")
	if secondPlan.Plan.Rule == nil || secondPlan.Diagnostic.Code != "" || oldPlan.Diagnostic.Code != "TGT_NO_RULE" {
		t.Error("recompiled program retained a stale generated target")
	}
	secondPlan.Plan.Free(a)
	secondPlan.Diagnostic.Free(a)
	oldPlan.Plan.Free(a)
	oldPlan.Diagnostic.Free(a)
	second.Program.Free()
	second.Free(a)
	secondParsed.Free()
	registry.Free()
}
