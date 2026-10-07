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

func TestInputlessFileMaterializeRunsForEachRootEpoch(t *testing.T) {
	a := t.Allocator()
	buffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(buffer, "", "kame-inputless-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	parsed := script.Parse(a, "inputless.kmk", "SHELL = kash\n./out :\n\tsh -c \"printf x >> runs; printf result > out\"\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}, {Capability: eval.Run}}})
	if compiled.Program == nil {
		t.Error("inputless file rule did not compile")
	} else {
		for i := 0; i < 2; i++ {
			result := compiled.Program.Materialize("./out")
			if result.Diagnostic.Code != "" || result.Fresh {
				t.Error("inputless file rule was reused or failed")
			}
			result.Free(a)
		}
		data, readErr := os.ReadFile(a, dir+"/runs")
		if readErr != nil || string(data) != "xx" {
			t.Error("separate root epochs did not rerun the inputless recipe")
		}
		mem.FreeSlice(a, data)
		compiled.Program.Free()
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
	os.Remove(dir + "/out")
	os.Remove(dir + "/runs")
	cleanupFingerprintSubdir(a, dir+"/.kame/cache/file-context")
	os.Remove(dir + "/.kame/cache")
	os.Remove(dir + "/.kame")
	os.Remove(dir)
}
