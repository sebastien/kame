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

func TestRuleEnvironmentsInheritOverrideAndDoNotLeak(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-env-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	parsed := script.Parse(a, "environment.kmk", "root : child local ; env \"MODE=first\" \"MODE=debug\"\nchild :\n\tprintf %s \"$MODE\" >> child-log\nlocal : ; env \"MODE=release\"\n\tprintf %s \"$MODE\" >> local-log\nother : child ; env \"MODE=release\"\nplain :\n\tprintf %s \"$MODE\" > plain-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "MODE=ambient"}})
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
	child, childErr := os.ReadFile(a, dir+"/child-log")
	local, localErr := os.ReadFile(a, dir+"/local-log")
	plain, plainErr := os.ReadFile(a, dir+"/plain-log")
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
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-env-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	parsed := script.Parse(a, "cache-environment.kmk", "debug : cached ; env \"MODE=debug\"\nrelease : cached ; env \"MODE=release\"\ntask cached :\n\tprintf %s \"$MODE\" >> runs\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "MODE=ambient"}})
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
	data, readErr := os.ReadFile(a, dir+"/runs")
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
	var dirBuffer [os.MaxPathLen]byte
	dir, err := os.MkdirTemp(dirBuffer[:], "", "kame-kash-env-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	parsed := script.Parse(a, "shared-environment.kmk", "SHELL = kash\ndefault : ./value reader\n./value :\n\tsleep 0.05\n\tprintenv MODE > ./value\nreader :\n\t@(out (text (read ./value)))\n")
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "MODE=preserved"}, Grants: []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}}})
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
	data, readErr := os.ReadFile(a, dir+"/value")
	if readErr != nil || string(data) != "preserved\n" {
		t.Error("suspended recipe lost its environment")
	}
	mem.FreeSlice(a, data)
}
