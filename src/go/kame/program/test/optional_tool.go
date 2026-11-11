package programcheck

import (
	"kame/core"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
)

var optionalToolMode int

func optionalToolResolver(a mem.Allocator, name, cwd string, environment []string) string {
	_, _, _ = name, cwd, environment
	path := ""
	if optionalToolMode == 2 {
		path = "/bin/sh"
	}
	if optionalToolMode == 3 {
		path = "/bin/cat"
	}
	if path == "" {
		return ""
	}
	return core.NewString(a, path).Text
}

func TestOptionalToolAbsenceAndResolutionAreRevalidated(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "optional.km", "available = (tool \"chosen\" | ?)\n")
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), ResolveTool: optionalToolResolver})
	if compiled.Program == nil {
		t.Fatal("compile optional lookup")
		return
	}
	p := compiled.Program
	paths := []string{"", "", "/bin/sh", "/bin/cat", ""}
	var previous core.Signature
	for i := range paths {
		optionalToolMode = i
		if node := p.Engine.Lookup(core.ResourceKey{Kind: core.ResourceTool, Name: "chosen"}); node != nil {
			p.Engine.Invalidate(node)
		}
		r := p.Materialize("available")
		if r.Diagnostic.Code != "" || (paths[i] == "" && r.Value.Kind != core.Nil) || (paths[i] != "" && r.Value.Text != paths[i]) {
			t.Error("optional tool observation was stale or converted an error to absence")
		}
		r.Free(a)
		node := p.Engine.Lookup(core.ResourceKey{Kind: core.ResourceTool, Name: "chosen"})
		if node == nil {
			t.Error("lookup did not track resolver identity")
		} else {
			if i == 1 && !node.Signature.Equal(previous) {
				t.Error("unchanged absence changed value identity")
			}
			if i > 1 && node.Signature.Equal(previous) {
				t.Error("changed resolution did not change value identity")
			}
			previous = node.Signature
		}
	}
	p.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}
