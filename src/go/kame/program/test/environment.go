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
)

func TestRuleEnvironmentsInheritOverrideAndDoNotLeak(t *testing.T) {
	a := t.Allocator()

	dir, err := os.MkdirTemp("", "kame-env-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	parsed := script.ParseKameFile(a, "environment.km", `SHELL = "/bin/sh"
task root : child local ; {env: {MODE: "debug"}}
task child
	printf %s "$MODE" >> child-log
task local ; {env: {MODE: "release"}}
	printf %s "$MODE" >> local-log
task other : child ; {env: {MODE: "release"}}
task plain
	printf %s "$MODE" > plain-log
`)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "MODE=ambient"}, Grants: []eval.Grant{{Capability: eval.Run}, {Capability: eval.Write}}})
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 {
		t.Fatal("compile failed")
		return
	}
	for i := 0; i < 2; i++ {
		result := compiled.Program.Materialize("root")
		if result.Diagnostic.Code != "" {
			t.Error("inherited environment materialization failed")
		}
		result.Free(a)
	}
	result := compiled.Program.Materialize("other")
	if result.Diagnostic.Code != "" {
		t.Error("released prerequisite could not bind a new environment")
	}
	result.Free(a)
	result = compiled.Program.Materialize("plain")
	if result.Diagnostic.Code != "" {
		t.Error("plain materialization failed")
	}
	result.Free(a)
	child, childErr := os.ReadFile(dir + "/child-log")
	local, localErr := os.ReadFile(dir + "/local-log")
	plain, plainErr := os.ReadFile(dir + "/plain-log")
	if childErr != nil || string(child) != "debugdebugrelease" || localErr != nil || string(local) != "releaserelease" || plainErr != nil || string(plain) != "ambient" {
		t.Error("inherited overrides or root isolation were incorrect")
	}
	mem.FreeSlice(a, child)
	mem.FreeSlice(a, local)
	mem.FreeSlice(a, plain)
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestCachedScopedEnvironmentChangesBetweenRoots(t *testing.T) {
	a := t.Allocator()

	dir, err := os.MkdirTemp("", "kame-cache-env-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	parsed := script.ParseKameFile(a, "cache-environment.km", `SHELL = "/bin/sh"
task debug : workload ; {env: {MODE: "debug"}}
task release : workload ; {env: {MODE: "release"}}
task cached workload
	printf %s "$MODE" >> runs
`)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "MODE=ambient"}, Grants: []eval.Grant{{Capability: eval.Run}, {Capability: eval.Write}}})
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 {
		t.Fatal("compile failed")
		return
	}
	targets := []string{"debug", "debug", "release", "debug"}
	for i := range targets {
		result := compiled.Program.Materialize(targets[i])
		if result.Diagnostic.Code != "" {
			t.Error("cached prerequisite environment rebinding failed")
		}
		result.Free(a)
	}
	data, readErr := os.ReadFile(dir + "/runs")
	if readErr != nil || string(data) != "debugreleasedebug" {
		t.Error("cached prerequisite ignored environment changes or repeated unchanged work")
	}
	mem.FreeSlice(a, data)
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestSharedDynamicReadPreservesSuspendedKashEnvironment(t *testing.T) {
	a := t.Allocator()

	dir, err := os.MkdirTemp("", "kame-kash-env-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	parsed := script.ParseKameFile(a, "shared-environment.km", "task default : ./value reader\nrule ./value\n\tsleep 0.05\n\tprintenv MODE > ./value\ntask reader\n\t(out! (text (read ./value)))\n")
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), ResolveTool: posix.ResolveTool, Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "MODE=preserved"}, Grants: []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}}})
	defer compiled.Free(a)
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 {
		t.Fatal("compile failed")
		return
	}
	defer compiled.Program.Free()
	result := compiled.Program.Materialize("default")
	if result.Diagnostic.Code != "" {
		t.Error("shared Kash environment failed: " + result.Diagnostic.Code)
	}
	result.Free(a)
	data, readErr := os.ReadFile(dir + "/value")
	if readErr != nil || string(data) != "preserved\n" {
		t.Error("suspended recipe lost its environment")
	}
	mem.FreeSlice(a, data)
}
