package plugins_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/plugins"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestPluginRegistrationFreesThroughItsAllocator(t *testing.T) {
	a := t.Allocator()
	registrationAlloc := mem.Tracker{Allocator: a}
	registry := eval.NewRegistry(a)
	declarations := []plugins.Plugin{{Name: "example", Version: "1", Operations: []plugins.Operation{{Name: "example-run", Version: "1", MinArity: 0, MaxArity: 0}}}}
	if !plugins.Register(&registrationAlloc, registry, declarations) {
		registry.Free()
		t.Fatal("plugin registration failed")
		return
	}
	if registrationAlloc.Stats().Alloc == 0 {
		t.Error("registration did not use its supplied allocator")
	}
	registry.Free()
	if registrationAlloc.Stats().Alloc != 0 {
		t.Error("registry disposal bypassed the registration allocator")
	}
}

func TestPluginWireCompletionRespectsResponseLimit(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	declarations := []plugins.Plugin{{Name: "example", Version: "1", Operations: []plugins.Operation{{Name: "example-run", Version: "1", MinArity: 0, MaxArity: 0, MaxResponseBytes: 8}}}}
	if !plugins.Register(a, registry, declarations) {
		t.Fatal("plugin registration failed")
		return
	}
	parsed := script.Parse(a, "plugin.km", "result = (example-run)")
	defer parsed.Free()
	engine := core.NewEngine(a)
	p := eval.Compile(a, engine, parsed, registry)
	defer p.Free()
	defer engine.Free()
	node := p.Definition("result")
	if node == nil {
		t.Fatal("plugin definition did not compile")
		return
	}
	engine.Request(node)
	request := p.Requests.Next()
	for i := 0; i < 10 && !request.OK; i++ {
		engine.Step()
		request = p.Requests.Next()
	}
	if !request.OK {
		t.Fatal("plugin request was not submitted")
		return
	}
	defer request.Request.Free(a)
	if request.Request.Kind != host.RequestPlugin {
		t.Fatal("unexpected host request")
		return
	}
	engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, Value: core.NewString(a, `{"kind":"int","data":42}`), HasValue: true})
	for i := 0; i < 10 && node.State != core.NodeFailed && !node.Current; i++ {
		engine.Step()
	}
	if node.State != core.NodeFailed || node.Diagnostic.Code != "PLUGIN_LIMIT" {
		t.Error("oversized wire completion bypassed the plugin limit")
	}
}
