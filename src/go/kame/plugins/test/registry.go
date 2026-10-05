package plugins_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/plugins"
	"solod.dev/so/testing"
)

func TestRegisterAddsVersionedOperations(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	declarations := []plugins.Plugin{{Name: "example", Version: "2", Operations: []plugins.Operation{{Name: "example-run", Version: "3", MinArity: 1, MaxArity: 1}}}}
	if !plugins.Register(a, registry, declarations) || !registry.HasOperation("example-run") {
		t.Error("valid plugin operation was not registered")
	}
	key := registry.TaskKey(a, "task", "example-run")
	if !key.OK || key.Key.Kind != core.ResourceTask || key.Key.Name != "task\x00example-run\x002/3" {
		t.Error("plugin and operation versions were not included in operation identity")
	}
	key.Key.Free(a)
	registry.Free()
}

func TestRegisteredOperationUsesCorrelatedPluginRequest(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	declarations := []plugins.Plugin{{Name: "example", Version: "2", Operations: []plugins.Operation{{Name: "example-run", Version: "3", MinArity: 1, MaxArity: 1}}}}
	if !plugins.Register(a, registry, declarations) { t.Error("plugin registration failed") }
	engine := core.NewEngine(a)
	parsed := script.Parse(a, "plugin.km", "result = (example-run \"hello\")")
	program := eval.Compile(a, engine, parsed, registry)
	definition := program.Definition("result")
	if definition == nil { t.Error("plugin test definition did not compile") }
	engine.Request(definition)
	request := program.Requests.Next()
	for step := 0; step < 5 && !request.OK; step++ { engine.Step(); request = program.Requests.Next() }
	if !request.OK && definition.State == core.NodeFailed { t.Error("plugin operation failed before request: " + definition.Diagnostic.Code + ": " + definition.Diagnostic.Message) }
	if !request.OK || request.Request.Kind != host.RequestPlugin {
		t.Error("plugin operation did not submit a plugin host request")
	} else {
		if host.PayloadText(request.Request.Payload, "plugin") != "example" || host.PayloadText(request.Request.Payload, "pluginVersion") != "2" || host.PayloadText(request.Request.Payload, "operation") != "example-run" || host.PayloadText(request.Request.Payload, "operationVersion") != "3" {
			t.Error("plugin request did not preserve operation identity")
		}
		args := host.PayloadList(request.Request.Payload, "args")
		if len(args) != 1 || args[0].Kind != core.String || args[0].Text != "hello" { t.Error("plugin request did not carry its positional argument") }
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, Value: core.Value{Kind: core.Int, Int: 42}, HasValue: true})
		request.Request.Free(a)
		engine.Step()
		engine.Step()
		if !definition.Current || definition.Latest.Kind != core.Int || definition.Latest.Int != 42 { t.Error("plugin completion did not resume with the returned value") }
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestRegisteredOperationCapabilityIsCheckedBeforeRequest(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	declarations := []plugins.Plugin{{Name: "example", Version: "2", Operations: []plugins.Operation{{Name: "example-run", Version: "3", MinArity: 0, MaxArity: 0, Capabilities: []eval.Capability{eval.Run}}}}}
	if !plugins.Register(a, registry, declarations) { t.Error("plugin registration failed") }
	engine := core.NewEngine(a)
	parsed := script.Parse(a, "plugin.km", "result = (example-run)")
	program := eval.Compile(a, engine, parsed, registry)
	definition := program.Definition("result")
	engine.Request(definition)
	request := program.Requests.Next()
	for step := 0; step < 5 && definition.State != core.NodeFailed && !request.OK; step++ { engine.Step(); request = program.Requests.Next() }
	if definition.State != core.NodeFailed || definition.Diagnostic.Code != "CAP_DENIED" {
		t.Error("missing operation capability was not denied")
	}
	if request.OK {
		request.Request.Free(a)
		t.Error("capability denial still submitted a plugin request")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestRegisterRejectsWholeInvalidDeclarationSet(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	invalid := []plugins.Plugin{
		{Name: "first", Version: "1", Operations: []plugins.Operation{{Name: "first-run", Version: "1", MinArity: 0, MaxArity: 0}}},
		{Name: "second", Version: "1", Operations: []plugins.Operation{{Name: "second-run", Version: "1", MinArity: 0, MaxArity: 0}}},
	}
	invalid[1].Name = invalid[0].Name
	if plugins.Register(a, registry, invalid) || registry.HasOperation("first-run") || registry.HasOperation("second-run") {
		t.Error("duplicate plugin names were accepted or partially registered")
	}
	invalid[1].Name = "second"
	invalid[1].Operations[0].MaxResponseBytes = plugins.DefaultMaxResponseBytes + 1
	if plugins.Register(a, registry, invalid) || registry.HasOperation("first-run") || registry.HasOperation("second-run") {
		t.Error("an oversized declaration was accepted or partially registered")
	}
	registry.Free()
}

func TestRegisterJSONValidatesExplicitDeclarations(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	valid := `[{"name":"example","version":"1","operations":[{"name":"example-run","version":"1","minArity":0,"maxArity":0,"capabilities":["run"]}]}]`
	if !plugins.RegisterJSON(a, registry, []byte(valid)) || !registry.HasOperation("example-run") {
		t.Error("valid JSON plugin declarations were rejected")
	}
	invalid := `[{"name":"duplicate","version":"1","operations":[{"name":"first-run","version":"1","minArity":0,"maxArity":0}]},{"name":"duplicate","version":"1","operations":[{"name":"second-run","version":"1","minArity":0,"maxArity":0}]}]`
	if plugins.RegisterJSON(a, registry, []byte(invalid)) || registry.HasOperation("first-run") || registry.HasOperation("second-run") {
		t.Error("invalid JSON declarations were accepted or partially registered")
	}
	registry.Free()
}
