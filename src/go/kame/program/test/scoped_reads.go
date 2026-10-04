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
	checkScopedDefinitionReads(t, nil, "debug|drelease|releasedebug|d")
}

func TestExplicitDefinitionsOverrideScopedEnvironment(t *testing.T) {
	checkScopedDefinitionReads(t, []string{"SETTING=explicit"}, "debug|explicitrelease|explicitdebug|explicit")
}

func checkScopedDefinitionReads(t *testing.T, defines []string, expected string) {
	a := t.Allocator()
	var directoryBuffer [os.MaxPathLen]byte
	directory, err := os.MkdirTemp(directoryBuffer[:], "", "kame-scoped-read-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(directory)
	parsed := script.Parse(a, "scoped.kmk", "SETTING = \"authored\"\nNEXT = (env \"NEXT\")\nMODE = (env \"MODE\")\n(mode) = (str MODE)\nVALUE = (mode)\ndebug : @(NEXT) ; env \"NEXT=cached\" \"MODE=debug\" \"KAME_SETTING=d\"\nrelease : @(NEXT) ; env \"NEXT=cached\" \"MODE=release\" \"KAME_SETTING=release\"\ntask cached :\n\tprintf '%s|%s' @(VALUE) @(SETTING) >> runs\n")
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: directory, Defines: defines, Environment: []string{"PATH=/usr/bin:/bin", "MODE=ambient", "KAME_SETTING=ambient"}, Grants: []eval.Grant{{Capability: eval.Env, Names: []string{"MODE", "NEXT"}}}})
	defer compiled.Free(a)
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
	if readErr != nil || string(data) != expected {
		t.Error("scoped definition bytes: "+string(data)+"; expected: "+expected)
	}
	mem.FreeSlice(a, data)
	compiled.Program.Free()
}
