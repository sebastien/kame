package program_test

import (
	"littlemake/host/posix"
	"littlemake/lang/eval"
	"littlemake/lang/script"
	"littlemake/operations"
	"littlemake/program"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
)

func TestLiteralSelectionAndFreshness(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "./out/result : ./input\n\tprintf result > @>\n")
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
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil { t.Fatal("source directory failed"); return }
	if os.WriteFile(dir+"/src/demo", []byte("captured"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./out/{name} : ./src/{name}\n\tcp @< @>\n")
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
	parsed := script.Parse(a, "test.lmk", "./out/{name} :\n\ttrue\n./out/{other} :\n\ttrue\n")
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
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "")
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
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "SRC = ./input\n./output : @(SRC)\n\tcp @< @>\n\tprintf x >> recipe-log\n")
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
	parsed := script.Parse(a, "test.lmk", "NAME = ./input\n./output : @(out NAME)\n\ttrue\n")
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
	parsed := script.Parse(a, "test.lmk", "run : missing\n\ttrue\n")
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
	parsed := script.Parse(a, "test.lmk", "./out/{name} ./out/{name} :\n\ttrue\n")
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
	parsed := script.Parse(a, "test.lmk", "SRC = ./src\nOTHER = ./other\n./output : @((list SRC (list OTHER :nil)))\n\ttrue\n")
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
	parsed := script.Parse(a, "test.lmk", "SRC = ./src\n./output : @((? SRC missing))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 1 || planned.Plan.Inputs[0] != "./src" { t.Error("plan evaluated an unselected fallback input") }
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanAndBuildInputLambdaReleasesItsScope(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "sources = [\"source-a\" \"source-b\"]\ntask source-a :\ntask source-b :\ntask run : @((map ([s] s) sources))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil { t.Fatal("source directory failed"); return }
	if os.WriteFile(dir+"/src/a.c", []byte("a"), 0o644) != nil || os.WriteFile(dir+"/src/b.c", []byte("b"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.lmk", "sources = [\"./src/a.c\" \"./src/b.c\"]\nobjects = (replace ./src/{name:*}.c ./build/{name}.o sources)\n./build/marker : @(objects)\n\tcat @<* > @>\n./build/{name}.o : ./src/{name}.c\n\tcp @< @>\n")
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
	parsed := script.Parse(a, "test.lmk", "A = \"@(B)\"\nB = \"@(A)\"\n./output : @(A)\n\ttrue\n")
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
	compiled := program.CompileMany(a, []program.CompileSource{{Name: "first.lmk", Text: "value = \"ok\""}, {Name: "second.lmk", Text: "(broken"}}, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Source != "second.lmk" { t.Error("multi-source diagnostic lost its source name") }
	if compiled.Program != nil { compiled.Program.Free() }
	compiled.Free(a)
	registry.Free()
}
