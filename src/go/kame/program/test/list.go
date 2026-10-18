package program_test

import (
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/testing"
)

func TestListTargetsCatalogueAndMatching(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "list.kmk", "ROOT = \"./build\"\nVALUE = 42\n@{ROOT}/{name}.o : ./src/{name}.c\n./build/app ./build/app.map : ./missing-input\n\ttouch forbidden\nbuild : missing-target\nbuilder :\nrebuild :\nservice daemon :\ntask test {mode} :\n./café :\ngenerate extra = [[kind: \"task\" target: \"generated\" inputs: [] order-only: [] recipe: []]]\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil {
		for i := range compiled.Diagnostics { t.Error(compiled.Diagnostics[i].Message) }
		compiled.Free(a); parsed.Free(); registry.Free()
		return
	}
	queries := []string{"", "build", "biuld", "buld", "buuild", "builx", "buidlre", "apl", "build/apq", "cafe", "cafè", "completely-unrelated", "VALUE"}
	expected := []string{
		`["./build/app","./build/app.map","./build/{name}.o","./café","build","builder","daemon","generated","rebuild","test"]`,
		`["build","./build/app","./build/app.map","./build/{name}.o","builder","rebuild"]`,
		`["build"]`, `["build"]`, `["build"]`, `["build"]`, `["builder"]`,
		`["./build/app"]`, `["./build/app"]`, `["./café"]`, `["./café"]`, `[]`, `[]`,
	}
	for i := range queries {
		var buffer = bytes.NewBuffer(a, nil)
		compiled.Program.WriteTargetList(&buffer, queries[i])
		if buffer.String() != expected[i] { t.Error("unexpected list for '"+queries[i]+"': "+buffer.String()) }
		buffer.Free()
	}
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestListTargetsDeduplicatesPatternOutputs(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "list.kmk", "./build/{name}.o : ./src/{name}.c\n./build/{name}.o : ./other/{name}.c\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{})
	if compiled.Program == nil { t.Error("compile failed") } else {
		var buffer = bytes.NewBuffer(a, nil)
		compiled.Program.WriteTargetList(&buffer, "")
		if buffer.String() != `["./build/{name}.o"]` { t.Error("duplicate output was listed") }
		buffer.Free()
		compiled.Program.Free()
	}
	compiled.Free(a); parsed.Free(); registry.Free()
}
