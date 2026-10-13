package plugins_test

import (
	"kame/lang/eval"
	"kame/plugins"
	"solod.dev/so/testing"
)

func TestPluginVersionPairsCannotCollide(t *testing.T) {
	a := t.Allocator()
	first := eval.NewRegistry(a)
	second := eval.NewRegistry(a)
	defer first.Free()
	defer second.Free()
	left := []plugins.Plugin{{Name: "example", Version: "a/b", Operations: []plugins.Operation{{Name: "example-run", Version: "c", MinArity: 0, MaxArity: 0}}}}
	right := []plugins.Plugin{{Name: "example", Version: "a", Operations: []plugins.Operation{{Name: "example-run", Version: "b/c", MinArity: 0, MaxArity: 0}}}}
	if !plugins.Register(a, first, left) || !plugins.Register(a, second, right) {
		t.Fatal("version declarations were rejected")
		return
	}
	firstKey := first.TaskKey(a, "task", "example-run")
	secondKey := second.TaskKey(a, "task", "example-run")
	defer firstKey.Key.Free(a)
	defer secondKey.Key.Free(a)
	if !firstKey.OK || !secondKey.OK || firstKey.Key.Name == secondKey.Key.Name {
		t.Error("different plugin/operation versions share a task identity")
	}
}

func TestPluginJSONRejectsDuplicateOptionalFields(t *testing.T) {
	a := t.Allocator()
	invalid := []string{
		`[{"name":"example","version":"1","timeoutMS":1,"timeoutMS":2,"operations":[{"name":"example-run","version":"1","minArity":0,"maxArity":0}]}]`,
		`[{"name":"example","version":"1","maxRequestBytes":64,"maxRequestBytes":128,"operations":[{"name":"example-run","version":"1","minArity":0,"maxArity":0}]}]`,
		`[{"name":"example","version":"1","maxResponseBytes":64,"maxResponseBytes":128,"operations":[{"name":"example-run","version":"1","minArity":0,"maxArity":0}]}]`,
		`[{"name":"example","version":"1","operations":[{"name":"example-run","version":"1","minArity":0,"maxArity":0,"timeoutMS":1,"timeoutMS":2}]}]`,
		`[{"name":"example","version":"1","operations":[{"name":"example-run","version":"1","minArity":0,"maxArity":0,"maxRequestBytes":64,"maxRequestBytes":128}]}]`,
		`[{"name":"example","version":"1","operations":[{"name":"example-run","version":"1","minArity":0,"maxArity":0,"maxResponseBytes":64,"maxResponseBytes":128}]}]`,
	}
	for i := range invalid {
		registry := eval.NewRegistry(a)
		accepted := plugins.RegisterJSON(a, registry, []byte(invalid[i]))
		if accepted || registry.HasOperation("example-run") {
			t.Error("duplicate optional field was accepted or partially registered")
		}
		registry.Free()
	}
}
