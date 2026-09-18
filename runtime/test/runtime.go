package program_test

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lib"
	"littlemake/lang/eval"
	"littlemake/lang/script"
	"littlemake/runtime"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
)

func observeDependency(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	_ = values
	key := core.ResourceKey{Kind: core.ResourceTarget, Name: "input"}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func observeFileDependency(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if len(values) != 1 || values[0].Kind != core.String { return eval.Result{Diagnostic: coreDiagnostic("invalid file dependency")} }
	key := core.ResourceKey{Kind: core.ResourceFile, Name: values[0].Text}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func coreDiagnostic(message string) diagnostic.Diagnostic { return diagnostic.Diagnostic{Code: "EXPR_INVALID", Severity: diagnostic.Error, Message: message} }

func TestLiteralSelectionAndFreshness(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "./out/result : ./input\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	plan := compiled.Program.Plan("out/result")
	if plan.Diagnostic.Code != "" || plan.Plan.Rule == nil || len(plan.Plan.Outputs) != 1 { t.Error("relative file target did not select literal rule") }
	plan.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMaterializeWritesOutput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "./out/result :\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("out/result")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/out/result")
	if readErr != nil || string(data) != "result" { t.Error("recipe did not write declared output") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMissingDeclaredOutputFails(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "./out/result :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("out/result")
	if result.Diagnostic.Code != "OUTPUT_MISSING" { t.Error("missing output did not fail") }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestSiblingOutputsShareOneExecution(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./out/a ./out/b : ./input\n\tprintf a > @>0\n\tprintf b > @>1\n\tprintf x >> file-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	left := compiled.Program.Materialize("out/a")
	right := compiled.Program.Materialize("out/b")
	if left.Diagnostic.Code != "" || right.Diagnostic.Code != "" || right.Path != "out/b" { t.Error("sibling output materialization failed") }
	left.Free(a); right.Free(a)
	file, fileErr := os.ReadFile(a, dir+"/file-log")
	if fileErr != nil || string(file) != "x" { t.Error("sibling outputs did not share execution") }
	mem.FreeSlice(a, file)
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
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
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
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	planned := compiled.Program.Plan("out/demo")
	if planned.Diagnostic.Code != "TGT_AMBIG" { t.Error("matching templates were not ambiguous") }
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
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./input")
	if result.Diagnostic.Code != "" || !result.Fresh || result.Path != "./input" { t.Error("existing explicit file was not materialized") }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBareTaskRuns(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	first := compiled.Program.Materialize("run")
	second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("bare task failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Error("bare task did not rerun") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskRunsUntilCachingExists(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	first := compiled.Program.Materialize("run")
	second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("cached task failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Error("cached task did not rerun") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDiamondDependencySharesPrerequisite(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tprintf x >> task-log\nleft : leaf\nright : leaf\nroot : left right\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("root")
	if result.Diagnostic.Code != "" { t.Errorf("diamond dependency failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "x" { t.Error("shared prerequisite did not execute once") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestFreshFileSkipsRecipe(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output : ./input\n\tcp @< @>\n\tprintf x >> recipe-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 1 || planned.Plan.Inputs[0] != "./input" { t.Error("plan did not flatten expression input") }
	planned.Plan.Free(a)
	first := compiled.Program.Materialize("./output")
	second := compiled.Program.Materialize("./output")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" || !second.Fresh { t.Errorf("fresh file materialization failed: %s %s %t", first.Diagnostic.Code, second.Diagnostic.Code, second.Fresh) }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/recipe-log")
	if readErr != nil || string(data) != "x" { t.Error("fresh file reran recipe") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestNewerInputRebuildsFile(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("first"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output : ./input\n\tcp @< @>\n\tprintf x >> recipe-log\nwait :\n\tsleep 1\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	first := compiled.Program.Materialize("./output")
	wait := compiled.Program.Materialize("wait")
	if os.WriteFile(dir+"/input", []byte("second"), 0o644) != nil { t.Fatal("input update failed"); return }
	second := compiled.Program.Materialize("./output")
	if first.Diagnostic.Code != "" || wait.Diagnostic.Code != "" || second.Diagnostic.Code != "" || second.Fresh { t.Error("newer input did not rebuild output") }
	first.Free(a); wait.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/recipe-log")
	if readErr != nil || string(data) != "xx" { t.Error("newer input did not rerun recipe") }
	mem.FreeSlice(a, data)
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
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
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

func TestYieldWritesFileOutput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "./output :\n\t@(yield \"yielded\")\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("yield failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "yielded" { t.Error("yield did not write output") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestOutAndErrEmitEvents(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "run :\n\t@(out \"out\")\n\t@(err \"err\")\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("effects failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	seenOut, seenErr := false, false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.Kind == program.Stdout && next.Event.Target == "run" && string(next.Event.Data) == "out" { seenOut = true }
		if next.Event.Kind == program.Stderr && next.Event.Target == "run" && string(next.Event.Data) == "err" { seenErr = true }
		next.Event.Free(a)
	}
	if !seenOut || !seenErr { t.Error("out and err events were not emitted with target identity") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestOperationDependencyEmitsEvent(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "input :\n\ttrue\noutput : input\n\t@(depends \"x\")\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "depends", Call: observeDependency, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { if len(compiled.Diagnostics) != 0 { t.Errorf("compile failed: %s %s", compiled.Diagnostics[0].Code, compiled.Diagnostics[0].Message) } else { t.Error("compile failed") }; return }
	result := compiled.Program.Materialize("output")
	if result.Diagnostic.Code != "" { t.Errorf("dependency operation failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	seen := false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.Kind == program.DependencyDiscovered && next.Event.Target == "output" && next.Event.DependencyKey.Name == "input" { seen = true }
		next.Event.Free(a)
	}
	if !seen { t.Error("dependency operation did not emit discovery event") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func containsSubstring(text string, sub string) bool {
	if len(sub) == 0 || len(sub) > len(text) { return false }
	for i := 0; i+len(sub) <= len(text); i++ {
		match := true
		for j := 0; j < len(sub); j++ { if text[i+j] != sub[j] { match = false; break } }
		if match { return true }
	}
	return false
}

func TestYieldRejectsShellCommand(t *testing.T) {
	a := t.Allocator()
	source := "./output :\n\t@(yield \"content\")\n\ttrue\n"
	parsed := script.Parse(a, "test.lmk", source)
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "OUTPUT_CONFLICT" { t.Errorf("yield conflict = %s", result.Diagnostic.Code) }
	if result.Diagnostic.Span.Start <= 0 || result.Diagnostic.Span.End > len(source) || !containsSubstring(source[result.Diagnostic.Span.Start:result.Diagnostic.Span.End], "yield") {
		t.Errorf("yield conflict did not record the yield source span: %d..%d", result.Diagnostic.Span.Start, result.Diagnostic.Span.End)
	}
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBuildEffectInExpressionInputIsPhaseInvalid(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "NAME = ./input\n./output : @(out NAME)\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "PHASE_INVALID" { t.Errorf("planning effect diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestServiceIsExplicitlyUnsupported(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "service daemon :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("daemon")
	if result.Diagnostic.Code != "FEATURE_UNSUP" { t.Errorf("service diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMissingBareInputFails(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "run : missing\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "TGT_NO_RULE" { t.Errorf("missing input diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestFileDependencyRunsProducerBeforeDependent(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/src", []byte("source"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./mid : ./src\n\tcp @< @>\n./out : ./mid\n\tcp @< @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./out")
	if result.Diagnostic.Code != "" { t.Errorf("dependency materialization failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	seenDependency := false
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; if next.Event.Kind == program.DependencyDiscovered { seenDependency = true }; next.Event.Free(a) }
	if !seenDependency { t.Error("dependency discovery event was not emitted") }
	data, readErr := os.ReadFile(a, dir+"/out")
	if readErr != nil || string(data) != "source" { t.Error("file producer was not scheduled before dependent") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestEmptyFileRecipeMissingOutputFails(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "./missing :\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./missing")
	if result.Diagnostic.Code != "OUTPUT_MISSING" { t.Errorf("empty recipe diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestRenderedDuplicateOutputsAreRejected(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "./out/{name} ./out/{name} :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./out/x")
	if planned.Diagnostic.Code != "PARSE_ERR" { t.Errorf("duplicate output diagnostic = %s", planned.Diagnostic.Code) }
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestRecipeFailureRetainsBodySpan(t *testing.T) {
	a := t.Allocator()
	source := "./output :\n\tfalse\n"
	parsed := script.Parse(a, "test.lmk", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "RECIPE_FAIL" || result.Diagnostic.Span.Start <= 0 || result.Diagnostic.Span.End > len(source) { t.Errorf("recipe failure span = %s %d..%d", result.Diagnostic.Code, result.Diagnostic.Span.Start, result.Diagnostic.Span.End) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanFlattensComputedInputExpression(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "SRC = ./src\nOTHER = ./other\n./output : @((list SRC (list OTHER :nil)))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
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
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 1 || planned.Plan.Inputs[0] != "./src" { t.Error("plan evaluated an unselected fallback input") }
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMaterializeReevaluatesComputedInputsWithoutDuplication(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/src", []byte("a"), 0o644) != nil || os.WriteFile(dir+"/other", []byte("b"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "SRC = ./src\nOTHER = ./other\n./output : @((list SRC OTHER))\n\tcat @<* > @>\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "ab" { t.Error("computed inputs were not flattened for execution") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanReportsDefinitionCycleInInput(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "A = \"@(B)\"\nB = \"@(A)\"\n./output : @(A)\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "DEP_CYCLE" { t.Errorf("cycle diagnostic = %s", planned.Diagnostic.Code) }
	planned.Diagnostic.Free(a)
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestTargetEventsCarrySharedIdentity(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "run :\n\t@(nop \"\")\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	var nodeID int64
	started, value, completed := false, false, false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.NodeID == 0 || next.Event.Key.Name == "" { t.Error("event lacked node identity") }
		if nodeID == 0 { nodeID = next.Event.NodeID }
		if next.Event.NodeID != nodeID { t.Error("target events did not share node identity") }
		if next.Event.Kind == program.TargetStarted { started = true }
		if next.Event.Kind == program.TargetValue { value = true }
		if next.Event.Kind == program.TargetCompleted { completed = true }
		next.Event.Free(a)
	}
	if !started || !value || !completed { t.Error("target lifecycle events were incomplete") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCompileManyQualifiesDiagnostics(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	compiled := program.CompileMany(a, []program.CompileSource{{Name: "first.lmk", Text: "value = \"ok\""}, {Name: "second.lmk", Text: "(broken"}}, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Source != "second.lmk" { t.Error("multi-source diagnostic lost its source name") }
	if compiled.Program != nil { compiled.Program.Free() }
	compiled.Free(a)
	registry.Free()
}

func TestSharedHandlesCancelOnlyAfterLastRelease(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "run :\n\tsleep 1\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Start("run")
	second := compiled.Program.Start("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Fatal("start failed"); return }
	compiled.Program.Tick(0)
	first.Handle.Cancel()
	compiled.Program.Tick(10)
	if second.Handle.Node.State == core.NodeCancelled { t.Error("releasing one shared handle cancelled the process") }
	second.Handle.Cancel()
	for i := 0; i < 30 && compiled.Program.Host.Active() != 0; i++ { compiled.Program.Tick(10) }
	if compiled.Program.Host.Active() != 0 { t.Error("last handle did not cancel the process group") }
	first.Handle.Free(); second.Handle.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDynamicExternalFileDependencyIsCurrent(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output :\n\t@(depends-file \"./input\")\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "depends-file", Call: observeFileDependency, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("dynamic file dependency failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	second := compiled.Program.Materialize("./output")
	if second.Diagnostic.Code != "" || !second.Fresh { t.Errorf("dynamic file dependency did not preserve freshness: %s %t", second.Diagnostic.Code, second.Fresh) }
	second.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}
