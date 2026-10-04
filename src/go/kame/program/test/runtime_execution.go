package program_test

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/operations"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
)

func TestMaterializeWritesOutput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "./out/result :\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("out/result")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/out/result")
	if readErr != nil || string(data) != "result" { t.Error("recipe did not write declared output") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestNamedTargetArgumentsBindDefaultsAndSeparateInstances(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-target-arguments-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "arguments.kmk", "prepare {region=west} {zone=global} :\n\tprintf @(region) >> ./dependency-log\ndeploy {region=west} {zone=global} : \"prepare region=@(region) zone=@(zone)\"\n\tprintf @(region) >> ./deploy-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	defaultPlan := compiled.Program.Plan("deploy")
	if defaultPlan.Diagnostic.Code != "" || len(defaultPlan.Plan.Arguments) != 2 || defaultPlan.Plan.Arguments[0].Value != "west" || defaultPlan.Plan.Arguments[1].Value != "global" { t.Error("optional defaults were not bound in the plan") }
	customPlan := compiled.Program.Plan("deploy region=east zone=eu")
	reorderedPlan := compiled.Program.Plan("deploy zone=eu region=east")
	if customPlan.Diagnostic.Code != "" || len(customPlan.Plan.Arguments) != 2 || customPlan.Plan.Arguments[0].Value != "east" || customPlan.Plan.Arguments[1].Value != "eu" { t.Error("named values were not bound in declaration order") }
	if defaultPlan.Plan.Key.Name == customPlan.Plan.Key.Name { t.Error("different argument values shared an instance key") }
	if customPlan.Plan.Key.Name != reorderedPlan.Plan.Key.Name { t.Error("assignment order changed target identity") }
	defaultPlan.Plan.Free(a); customPlan.Plan.Free(a); reorderedPlan.Plan.Free(a)
	targets := []string{"deploy", "deploy region=east zone=eu"}
	for i := range targets {
		target := targets[i]
		result := compiled.Program.Materialize(target)
		if result.Diagnostic.Code != "" { t.Error("materialize failed: " + result.Diagnostic.Code + " " + result.Diagnostic.Message) }
		result.Free(a)
	}
	data, readErr := os.ReadFile(a, dir+"/deploy-log")
	if readErr != nil || string(data) != "westeast" { t.Error("recipe scope did not receive default and supplied values") }
	mem.FreeSlice(a, data)
	dependencies, dependencyErr := os.ReadFile(a, dir+"/dependency-log")
	if dependencyErr != nil || string(dependencies) != "westeast" { t.Error("argument values did not flow into dependency target selection") }
	mem.FreeSlice(a, dependencies)
	invalid := []string{"deploy unknown=west", "deploy region=a region=b", "deploy region"}
	for i := range invalid {
		target := invalid[i]
		result := compiled.Program.Plan(target)
		if result.Diagnostic.Code != "TGT_ARGUMENT" { t.Error("invalid invocation " + target + " returned " + result.Diagnostic.Code) }
		result.Diagnostic.Free(a); result.Plan.Free(a)
	}
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestNamedTargetArgumentCannotShadowDefinition(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "arguments.kmk", "region = \"global\"\ndeploy {region=west} :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
	if compiled.Program != nil || len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Code != "TGT_ARGUMENT" { t.Error("target argument shadowing was not rejected") }
	compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCaptureRedirectionDependenciesAndEffects(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-redirection-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "./input :\n\tprintf source > @>\nrun :\n\t@(nop $(cat < input > output))\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	grants := []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}}
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: grants})
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("redirection failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "source" { t.Error("write effect overwrote process output") }
	mem.FreeSlice(a, data)
	dependency, started := false, false
	writes := 0
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.Kind == program.DependencyDiscovered && next.Event.DependencyKey.Kind == core.ResourceFile && next.Event.DependencyKey.Name == dir+"/input" {
			if !dependency && started { t.Error("process started before discovering its input") }
			dependency = true
		}
		if next.Event.Kind == program.ProcessStarted { started = true }
		if next.Event.Kind == program.Effect && next.Event.Effect == "process-write" {
			if string(next.Event.Data) != dir+"/output" { t.Error("write effect omitted its path") }
			writes++
		}
		next.Event.Free(a)
	}
	if !dependency || !started || writes != 1 { t.Error("redirection omitted dependency, process event, or unique write effect") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMissingDeclaredOutputFails(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "./out/result :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("out/result")
	if result.Diagnostic.Code != "OUTPUT_MISSING" { t.Error("missing output did not fail") }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestSiblingOutputsShareOneExecution(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./out/a ./out/b : ./input\n\tprintf a > @>0\n\tprintf b > @>1\n\tprintf x >> file-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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

func TestBareTaskRuns(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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

func TestRepeatedProgramMaterializationReleasesRunState(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-leak-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Fatal("compile failed"); return }
	for i := 0; i < 64; i++ {
		result := compiled.Program.Materialize("run")
		if result.Diagnostic.Code != "" { result.Free(a); t.Fatalf("materialization %d failed: %s", i, result.Diagnostic.Code) }
		result.Free(a)
	}
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || len(data) != 64 { t.Errorf("repeated task did not complete: %d bytes", len(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBareTaskDependencyPreventsCachedHit(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "leaf :\n\tprintf l >> task-log\ntask run : leaf\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("task materialization failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "lrlr" { t.Errorf("bare dependency did not rerun: %s", string(data)) }
	mem.FreeSlice(a, data)
	entries, cacheErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if cacheErr == nil {
		if len(entries) != 0 { t.Error("bare dependency committed a cache record") }
		os.FreeDirEntry(a, entries)
	}
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBareTaskRerunsForEachRoot(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "leaf :\n\tprintf l >> task-log\nleft : leaf\nright : leaf\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("left")
	second := compiled.Program.Materialize("right")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("root materialization failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "ll" { t.Errorf("bare task did not run once per root: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestTransitiveBareTaskReruns(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "leaf :\n\tprintf l >> task-log\ntask mid : leaf\n\tprintf m >> task-log\ntask run : mid\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run")
	second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("transitive materialization failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "lmrlmr" { t.Errorf("transitive bare task did not rerun: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestConcurrentRootsShareBareTask(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "leaf :\n\tprintf x >> task-log\nleft : leaf\nright : leaf\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	left := compiled.Program.Start("left")
	right := compiled.Program.Start("right")
	if left.Diagnostic.Code != "" || right.Diagnostic.Code != "" || left.Handle == nil || right.Handle == nil { t.Fatal("start failed"); return }
	leftDone, rightDone := false, false
	for i := 0; i < 40 && (!leftDone || !rightDone); i++ {
		compiled.Program.Tick(10)
		if !leftDone {
			polled := left.Handle.Poll()
			if polled.Done {
				leftDone = true
				if polled.Result.Diagnostic.Code != "" { t.Error("left failed") }
				polled.Result.Free(a)
			}
		}
		if !rightDone {
			polled := right.Handle.Poll()
			if polled.Done {
				rightDone = true
				if polled.Result.Diagnostic.Code != "" { t.Error("right failed") }
				polled.Result.Free(a)
			}
		}
	}
	if !leftDone || !rightDone { t.Error("concurrent roots did not finish") }
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "x" { t.Errorf("concurrent roots did not share bare task: %s", string(data)) }
	mem.FreeSlice(a, data)
	left.Handle.Free(); right.Handle.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCancelBareRerunPreservesCachedRecord(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task keep :\n\tprintf k >> task-log\nleaf :\n\tsleep 1\ntask run : leaf\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	kept := compiled.Program.Materialize("keep")
	if kept.Diagnostic.Code != "" { t.Fatal("cached task failed"); return }
	kept.Free(a)
	entries, readErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if readErr != nil || len(entries) != 1 { t.Fatal("cache record was not written"); return }
	before, beforeErr := os.ReadFile(a, dir+"/.kame/cache/tasks/"+entries[0].Name)
	os.FreeDirEntry(a, entries)
	if beforeErr != nil { t.Fatal("cache record unreadable"); return }
	first := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" { mem.FreeSlice(a, before); t.Fatal("first run failed"); return }
	first.Free(a)
	rerun := compiled.Program.Start("run")
	if rerun.Diagnostic.Code != "" || rerun.Handle == nil { mem.FreeSlice(a, before); t.Fatal("rerun start failed"); return }
	submitted := false
	for i := 0; i < 40 && !submitted; i++ {
		compiled.Program.Tick(10)
		if compiled.Program.Host.Active() != 0 { submitted = true }
	}
	if !submitted { mem.FreeSlice(a, before); rerun.Handle.Free(); t.Fatal("bare rerun did not submit"); return }
	rerun.Handle.Cancel()
	for i := 0; i < 40 && compiled.Program.Host.Active() != 0; i++ { compiled.Program.Tick(10) }
	if compiled.Program.Host.Active() != 0 { t.Error("cancelled bare rerun did not stop") }
	rerun.Handle.Free()
	later, laterErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if laterErr != nil || len(later) != 1 { mem.FreeSlice(a, before); t.Error("cancellation changed cache records"); return }
	after, afterErr := os.ReadFile(a, dir+"/.kame/cache/tasks/"+later[0].Name)
	os.FreeDirEntry(a, later)
	if afterErr != nil || string(before) != string(after) { t.Error("cancellation replaced cached-task record") }
	mem.FreeSlice(a, before)
	if afterErr == nil { mem.FreeSlice(a, after) }
	again := compiled.Program.Materialize("keep")
	if again.Diagnostic.Code != "" { t.Error("cached task failed after cancellation") }
	again.Free(a)
	data, dataErr := os.ReadFile(a, dir+"/task-log")
	if dataErr != nil || string(data) != "kr" { t.Errorf("cancellation replaced cached work: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDiscoveredBareTaskRerunsForNewRoot(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "leaf :\n\tprintf l >> task-log\nrun :\n\t@(depends \"leaf\")\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "depends", Call: dependOnNamedTarget, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	primed := compiled.Program.Materialize("leaf")
	if primed.Diagnostic.Code != "" { t.Fatal("leaf failed"); return }
	primed.Free(a)
	reached := compiled.Program.Materialize("run")
	if reached.Diagnostic.Code != "" { t.Errorf("discovered dependency failed: %s", reached.Diagnostic.Code) }
	reached.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "llr" { t.Errorf("discovered bare task was reused: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestFailedBareTaskRerunsForNewRoot(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "leaf :\n\tprintf l >> task-log\n\tfalse\nrun : leaf\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run")
	if first.Diagnostic.Code == "" { t.Fatal("failing bare task succeeded"); return }
	first.Free(a)
	second := materializeWithin(compiled.Program, "run", 40)
	if second.Diagnostic.Code == "" || second.Diagnostic.Code == "TEST_TIMEOUT" { t.Errorf("failed bare task was not rerun: %s", second.Diagnostic.Code) }
	second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "ll" { t.Errorf("failed bare task was reused: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCancelledBareTaskRerunsForNewRoot(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "leaf :\n\tsleep 1\n\tprintf l >> task-log\nrun : leaf\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	started := compiled.Program.Start("run")
	if started.Diagnostic.Code != "" || started.Handle == nil { t.Fatal("start failed"); return }
	submitted := false
	for i := 0; i < 40 && !submitted; i++ {
		compiled.Program.Tick(10)
		if compiled.Program.Host.Active() != 0 { submitted = true }
	}
	if !submitted { started.Handle.Free(); t.Fatal("bare task did not submit"); return }
	started.Handle.Cancel()
	for i := 0; i < 40 && compiled.Program.Host.Active() != 0; i++ { compiled.Program.Tick(10) }
	started.Handle.Free()
	second := materializeWithin(compiled.Program, "run", 200)
	if second.Diagnostic.Code != "" { t.Errorf("cancelled bare task was not rerun: %s", second.Diagnostic.Code) }
	second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "lr" { t.Errorf("cancelled bare task was reused: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func materializeWithin(p *program.Program, target string, ticks int) program.Result {
	started := p.Start(target)
	if started.Diagnostic.Code != "" || started.Handle == nil { return program.Result{Diagnostic: started.Diagnostic} }
	for i := 0; i < ticks; i++ {
		p.Tick(10)
		polled := started.Handle.Poll()
		if polled.Done { started.Handle.Free(); return polled.Result }
	}
	started.Handle.Cancel()
	started.Handle.Free()
	return program.Result{Diagnostic: diagnostic.Diagnostic{Code: "TEST_TIMEOUT", Severity: diagnostic.Error, Message: "materialization did not finish"}}
}

func TestDiamondDependencySharesPrerequisite(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "leaf :\n\tprintf x >> task-log\nleft : leaf\nright : leaf\nroot : left right\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("root")
	if result.Diagnostic.Code != "" { t.Errorf("diamond dependency failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "x" { t.Error("shared prerequisite did not execute once") }
	mem.FreeSlice(a, data)
	second := compiled.Program.Materialize("root")
	if second.Diagnostic.Code != "" { t.Errorf("second diamond root failed: %s", second.Diagnostic.Code) }
	second.Free(a)
	again, againErr := os.ReadFile(a, dir+"/task-log")
	if againErr != nil || string(again) != "xx" { t.Error("shared prerequisite did not rerun once for the second root") }
	mem.FreeSlice(a, again)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestFreshFileSkipsRecipe(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output : ./input\n\tcp @< @>\n\tprintf x >> recipe-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("first"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output : ./input\n\tcp @< @>\n\tprintf x >> recipe-log\nwait :\n\tsleep 1\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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

func TestDefinitionHostReadFailureReleasesDiagnosticOnce(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "value = (read \"missing-file\")\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("value")
	if result.Diagnostic.Code != "FS_ERR" { t.Errorf("diagnostic = %s, want FS_ERR", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestYieldWritesFileOutput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "./output :\n\t@(yield \"yielded\")\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("yield failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "yielded" { t.Error("yield did not write output") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestYieldRejectsShellCommand(t *testing.T) {
	a := t.Allocator()
	source := "./output :\n\t@(yield \"content\")\n\ttrue\n"
	parsed := script.Parse(a, "test.kmk", source)
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "OUTPUT_CONFLICT" { t.Errorf("yield conflict = %s", result.Diagnostic.Code) }
	if result.Diagnostic.Span.Start <= 0 || result.Diagnostic.Span.End > len(source) || !containsSubstring(source[result.Diagnostic.Span.Start:result.Diagnostic.Span.End], "yield") {
		t.Errorf("yield conflict did not record the yield source span: %d..%d", result.Diagnostic.Span.Start, result.Diagnostic.Span.End)
	}
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestServiceIsExplicitlyUnsupported(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "service daemon : ; [ready: [argv: [\"./daemon\" \"ready\"]]]\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("daemon")
	if result.Diagnostic.Code != "FEATURE_UNSUP" { t.Errorf("service diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestInvalidServiceConfigFailsBeforeExecution(t *testing.T) {
	invalid := []string{
		"service daemon : ; [ready: [argv: [\"probe\"] interval-ms: :true]]\n\ttrue\n",
		"service daemon : ; [ready: [argv: [\"probe\"] unexpected: 1]]\n\ttrue\n",
	}
	for i := range invalid {
		a := t.Allocator()
		parsed := script.Parse(a, "test.kmk", invalid[i])
		registry := eval.NewRegistry(a)
		compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
		if len(compiled.Diagnostics) != 0 || compiled.Program == nil {
			t.Error("invalid service settings failed during compilation")
			compiled.Free(a); parsed.Free(); registry.Free()
			continue
		}
		result := compiled.Program.Materialize("daemon")
		if result.Diagnostic.Code != "EXPR_INVALID" { t.Errorf("invalid service settings diagnostic = %s", result.Diagnostic.Code) }
		result.Free(a)
		compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
	}
}

func TestFileDependencyRunsProducerBeforeDependent(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/src", []byte("source"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./mid : ./src\n\tcp @< @>\n./out : ./mid\n\tcp @< @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	parsed := script.Parse(a, "test.kmk", "./missing :\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./missing")
	if result.Diagnostic.Code != "OUTPUT_MISSING" { t.Errorf("empty recipe diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMaterializeReevaluatesComputedInputsWithoutDuplication(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/src", []byte("a"), 0o644) != nil || os.WriteFile(dir+"/other", []byte("b"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "SRC = ./src\nOTHER = ./other\n./output : @((list SRC OTHER))\n\tcat @<* > @>\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "ab" { t.Error("computed inputs were not flattened for execution") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestSharedHandlesCancelOnlyAfterLastRelease(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "run :\n\tsleep 1\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output :\n\t@(depends-file \"./input\")\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "depends-file", Call: observeFileDependency, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("dynamic file dependency failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	second := compiled.Program.Materialize("./output")
	if second.Diagnostic.Code != "" || !second.Fresh { t.Errorf("dynamic file dependency did not preserve freshness: %s %t", second.Diagnostic.Code, second.Fresh) }
	second.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestWriteOperationDefersUntilExecution(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "./output :\n\t@(write \"./output\" \"written\")\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Write, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("write failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "written" { t.Error("write did not commit") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestWriteOperationCoercesValue(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "./output :\n\t@(write \"./output\" 42)\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Write, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("write failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "42" { t.Error("write did not coerce its value") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}


func TestReadOperationResumesDuringRendering(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("abc"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output :\n\t@(yield (str (count (read \"./input\"))))\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("read failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "3" { t.Error("read result did not resume into yield") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestWildcardRelativePathDoesNotLeak(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("abc"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output :\n\t@(yield (str (wildcard \"./*\")))\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("wildcard failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "[\"./input\"]" { t.Errorf("wildcard result = %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestRenderFreesLineSpansWhenLaterLineWaits(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("abc"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output :\n\tkept\n\t@(yield (str (count (read \"./input\"))))\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "OUTPUT_CONFLICT" { t.Errorf("yield conflict = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDefinitionMaterializationReturnsCurrentValue(t *testing.T) {
 a := t.Allocator()
 parsed := script.Parse(a, "value.kmk", "answer = [1 2]\n")
 registry := eval.NewRegistry(a)
 compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a)})
 if compiled.Program == nil { t.Error("compile definition"); compiled.Free(a); parsed.Free(); registry.Free(); return }
 result := compiled.Program.Materialize("answer")
 if result.Diagnostic.Code != "" || result.Value.Kind != core.List || len(result.Value.List) != 2 || result.Value.List[1].Int != 2 { t.Error("current definition was not materialized") }
 result.Free(a)
 compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestAlwaysFileRerunsOncePerRootEpochAndRemainsAnArtifact(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-always-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "always.kmk", "always ./output : ./input\n\tcp @< @>\n\tprintf x >> recipe-log\nleft : ./output\nright : ./output\nroot : left right\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 { t.Fatal("always compile failed"); return }
	for cycle := 0; cycle < 3; cycle++ {
		result := compiled.Program.Materialize("root")
		if result.Diagnostic.Code != "" { t.Error("always diamond root failed") }
		result.Free(a)
	}
	data, readErr := os.ReadFile(a, dir+"/recipe-log")
	if readErr != nil || string(data) != "xxx" { t.Error("always file did not rerun exactly once per diamond root") }
	mem.FreeSlice(a, data)
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" || result.Fresh || result.Path != "./output" { t.Error("always target lost artifact semantics or reported fresh") }
	result.Free(a)
	data, readErr = os.ReadFile(a, dir+"/recipe-log")
	if readErr != nil || string(data) != "xxxx" { t.Error("direct always request did not rerun") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}
