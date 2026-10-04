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

func TestScopedEnvironmentReadsRebindCachedPrerequisites(t *testing.T) {
	a := t.Allocator()
	var directoryBuffer [os.MaxPathLen]byte
	directory, err := os.MkdirTemp(directoryBuffer[:], "", "kame-scoped-read-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(directory)
	parsed := script.Parse(a, "scoped.kmk", "debug : cached ; env \"MODE=debug\"\nrelease : cached ; env \"MODE=release\"\ntask cached :\n\tprintf %s @(env \"MODE\") >> runs\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: directory, Environment: []string{"PATH=/usr/bin:/bin", "MODE=ambient"}, Grants: []eval.Grant{{Capability: eval.Env, Names: []string{"MODE"}}}})
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 {
		t.Fatal("compile failed")
		return
	}
	targets := []string{"debug", "debug", "release", "debug"}
	for i := range targets {
		result := compiled.Program.Materialize(targets[i])
		if result.Diagnostic.Code != "" {
			t.Error("scoped expression materialization failed")
		}
		result.Free(a)
	}
	data, readErr := os.ReadFile(a, directory+"/runs")
	if readErr != nil || string(data) != "debugreleasedebug" {
		t.Error("expression reads leaked ambient or cached configuration across roots")
	}
	mem.FreeSlice(a, data)
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}
