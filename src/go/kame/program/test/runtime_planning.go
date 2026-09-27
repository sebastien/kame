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

func TestLiteralSelectionAndFreshness(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "./out/result : ./input\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	plan := compiled.Program.Plan("out/result")
	if plan.Diagnostic.Code != "" || plan.Plan.Rule == nil || len(plan.Plan.Outputs) != 1 { t.Error("relative file target did not select literal rule") }
	plan.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestTemplateCapturesRenderInputsAndOutputs(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil { t.Fatal("source directory failed"); return }
	if os.WriteFile(dir+"/src/demo", []byte("captured"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./out/{name} : ./src/{name}\n\tcp @< @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("out/demo")
	if result.Diagnostic.Code != "" { t.Errorf("template materialization failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/out/demo")
	if readErr != nil || string(data) != "captured" { t.Error("captures did not render corresponding input and output") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMatchingTemplatesAreAmbiguous(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "./out/{name} :\n\ttrue\n./out/{other} :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	planned := compiled.Program.Plan("out/demo")
	if planned.Diagnostic.Code != "TGT_AMBIG" { t.Error("matching templates were not ambiguous") }
	planned.Diagnostic.Free(a)
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestExistingFileNeedsNoRule(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./input")
	if result.Diagnostic.Code != "" || !result.Fresh || result.Path != "./input" { t.Error("existing explicit file was not materialized") }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDefinitionExpressionSuppliesInput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "SRC = ./input\n./output : @(SRC)\n\tcp @< @>\n\tprintf x >> recipe-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	first := compiled.Program.Materialize("./output")
	second := compiled.Program.Materialize("./output")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" || !second.Fresh { t.Error("expression input freshness failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "source" { t.Error("expression input did not render") }
	mem.FreeSlice(a, data)
	log, logErr := os.ReadFile(a, dir+"/recipe-log")
	if logErr != nil || string(log) != "x" { t.Error("fresh expression input reran recipe") }
	mem.FreeSlice(a, log)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBuildEffectInExpressionInputIsPhaseInvalid(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "NAME = ./input\n./output : @(out NAME)\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "PHASE_INVALID" { t.Errorf("planning effect diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMissingBareInputFails(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "run : missing\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "TGT_NO_RULE" { t.Errorf("missing input diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestRenderedDuplicateOutputsAreRejected(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "./out/{name} ./out/{name} :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./out/x")
	if planned.Diagnostic.Code != "PARSE_ERR" { t.Errorf("duplicate output diagnostic = %s", planned.Diagnostic.Code) }
	planned.Diagnostic.Free(a)
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanFlattensComputedInputExpression(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "SRC = ./src\nOTHER = ./other\n./output : @((list SRC (list OTHER :nil)))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 2 || len(planned.Plan.ResourceInputs) != 2 || planned.Plan.Inputs[0] != "./src" || planned.Plan.Inputs[1] != "./other" { t.Error("plan did not flatten computed input expression") }
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanOnlyResolvesSelectedFallbackInput(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "SRC = ./src\n./output : @((? SRC missing))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 1 || planned.Plan.Inputs[0] != "./src" { t.Error("plan evaluated an unselected fallback input") }
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanDefersHostBackedInputWithoutPhaseFailure(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "./output : @((wildcard ./source/*))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || planned.Plan.Freshness != program.Unknown || len(planned.Plan.DynamicInputs) != 0 { t.Errorf("host-backed input plan = %s/%d/%d", planned.Diagnostic.Code, planned.Plan.Freshness, len(planned.Plan.DynamicInputs)) }
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestExpandPlanResolvesHostBackedInputsWithoutRunningRecipe(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-expand-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil || os.WriteFile(dir+"/src/one.c", []byte("one"), 0o644) != nil || os.WriteFile(dir+"/src/two.c", []byte("two"), 0o644) != nil { t.Fatal("source setup failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output : @((wildcard ./src/*.c))\n\tfalse\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	expanded := compiled.Program.ExpandPlan("./output")
	if expanded.Diagnostic.Code != "" || len(expanded.Plan.DynamicInputs) != 2 || expanded.Plan.DynamicInputs[0] != "./src/one.c" || expanded.Plan.DynamicInputs[1] != "./src/two.c" { t.Errorf("expanded inputs = %s/%v", expanded.Diagnostic.Code, expanded.Plan.DynamicInputs) }
	expanded.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestExpandPlanDoesNotReplaceMaterializationInstance(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-expand-materialize-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil || os.WriteFile(dir+"/src/input.txt", []byte("input"), 0o644) != nil { t.Fatal("source setup failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output : @((wildcard ./src/*.txt))\n\tprintf expanded > @>\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	expanded := compiled.Program.ExpandPlan("./output")
	if expanded.Diagnostic.Code != "" { t.Errorf("expand diagnostic: %s", expanded.Diagnostic.Code) }
	expanded.Plan.Free(a)
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("materialize diagnostic: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "expanded" { t.Errorf("output = %q", string(data)) }
	if len(data) != 0 { mem.FreeSlice(a, data) }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestExpandPlanDoesNotMaterializeRuleDependency(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-expand-read-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "./generated:\n\tprintf ./input > @>\n./output : @((read \"./generated\"))\n\tprintf output > @>\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	expanded := compiled.Program.ExpandPlan("./output")
	if expanded.Diagnostic.Code == "" { t.Error("expansion unexpectedly resolved generated rule") }
	expanded.Plan.Free(a); expanded.Diagnostic.Free(a)
	_, statErr := os.Stat(dir + "/generated")
	if statErr == nil { t.Error("span expansion executed the generated recipe") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMaterializeResumesHostBackedRuleInput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-resolve-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil || os.WriteFile(dir+"/src/input.txt", []byte("resolved\n"), 0o644) != nil { t.Fatal("source setup failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output : @((wildcard ./src/*.txt))\n\tcat @< > @>\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("materialize diagnostic: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "resolved\n" { t.Errorf("resolved output = %q", string(data)) }
	if len(data) != 0 { mem.FreeSlice(a, data) }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanAndBuildInputLambdaReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "sources = [\"source-a\" \"source-b\"]\ntask source-a :\ntask source-b :\ntask run : @((map ([s] s) sources))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("run")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 2 { t.Error("plan input lambda failed") }
	planned.Plan.Free(a)
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("build input lambda failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPatternReplaceComputesObjectInputs(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil { t.Fatal("source directory failed"); return }
	if os.WriteFile(dir+"/src/a.c", []byte("a"), 0o644) != nil || os.WriteFile(dir+"/src/b.c", []byte("b"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.kmk", "sources = [\"./src/a.c\" \"./src/b.c\"]\nobjects = (replace ./src/{name:*}.c ./build/{name}.o sources)\n./build/marker : @(objects)\n\tcat @<* > @>\n./build/{name}.o : ./src/{name}.c\n\tcp @< @>\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read}}})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./build/marker")
	if result.Diagnostic.Code != "" { t.Errorf("pattern replace build failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/build/marker")
	if readErr != nil || string(data) != "ab" { t.Error("pattern replace did not compute object inputs") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanReportsDefinitionCycleInInput(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "A = \"@(B)\"\nB = \"@(A)\"\n./output : @(A)\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "DEP_CYCLE" { t.Errorf("cycle diagnostic = %s", planned.Diagnostic.Code) }
	planned.Diagnostic.Free(a)
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCompileManyQualifiesDiagnostics(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	compiled := program.CompileMany(a, []program.CompileSource{{Name: "first.kmk", Text: "value = \"ok\""}, {Name: "second.kmk", Text: "(broken"}}, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Source != "second.kmk" { t.Error("multi-source diagnostic lost its source name") }
	if compiled.Program != nil { compiled.Program.Free() }
	compiled.Free(a)
	registry.Free()
}
