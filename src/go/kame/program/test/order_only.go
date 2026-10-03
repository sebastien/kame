package program_test

import (
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
)

func TestOrderOnlyBareTaskRerunsWithoutRebuildingFile(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-order-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil {
		t.Fatal("input creation failed")
		return
	}
	parsed := script.Parse(a, "order.kmk", "./out : ./input | prepare\n\tcp @< @>; printf x >> runs\nprepare :\n\tprintf p >> prepares\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin"}})
	if compiled.Program == nil || len(compiled.Diagnostics) != 0 {
		t.Fatal("compile failed")
		return
	}
	for i := 0; i < 2; i++ {
		result := compiled.Program.Materialize("./out")
		if result.Diagnostic.Code != "" || (i == 1 && !result.Fresh) {
			t.Error("order-only task changed file freshness between root epochs")
		}
		result.Free(a)
	}
	runs, runErr := os.ReadFile(a, dir+"/runs")
	prepares, prepareErr := os.ReadFile(a, dir+"/prepares")
	if runErr != nil || prepareErr != nil || string(runs) != "x" || string(prepares) != "pp" {
		t.Error("ordering work did not rerun independently of the file recipe")
	}
	mem.FreeSlice(a, runs)
	mem.FreeSlice(a, prepares)
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}
