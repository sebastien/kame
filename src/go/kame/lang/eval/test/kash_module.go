package evalcheck

import (
	"kame/core"
	"kame/host"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
)

func TestModuleSourceIdentityAndResumedLexicalScope(t *testing.T) {
	texts := []string{
		"a = (kash ./a.kash)\nb = (kash ./b.kash)\nresult = (eq? [a.child.value b.child.value] [1 2])\n",
		"result = (let [m (kash ./scope.kash) x 42] (eq? m.value 42))\n",
		"result = (let [m (kash ./scope.kash) n (kash ./one.kash) x 42] (eq? m.value 42))\n",
	}
	for i := range texts {
		a := t.Allocator()
		engine := core.NewEngine(a)
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		parsed := script.Parse(a, "/project/main.km", texts[i])
		eval.SetScriptSourceOrigin(parsed, 0, len(parsed.Source.Text), 1)
		program := eval.Compile(a, engine, parsed, registry)
		program.AddSourcePart(parsed.Source.Name, 0, len(parsed.Source.Text), 0)
		program.SetSourcePartDirectory("/project")
		node := program.Definition("result")
		engine.Request(node)
		for tick := 0; tick < 100 && !node.Current && node.State != core.NodeFailed; tick++ {
			engine.Step()
			request := program.Requests.Next()
			if !request.OK {
				continue
			}
			name := host.PayloadPath(request.Request.Payload)
			text := ""
			switch name {
			case "/project/a.kash":
				text = "child = (kash ./one.kash)\n"
			case "/project/b.kash":
				text = "child = (kash ./two.kash)\n"
			case "/project/one.kash":
				text = "value = 1\n"
			case "/project/two.kash":
				text = "value = 2\n"
			case "/project/scope.kash":
				text = "value = x\n"
			}
			if text == "" || request.Request.Kind != host.RequestLoadSource {
				t.Error("unexpected source request: " + name)
			}
			engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, HasValue: true, Value: core.NewBytes(a, []byte(text))})
			request.Request.Free(a)
		}
		if !node.Current || node.Latest.Kind != core.Bool || !node.Latest.Bool {
			t.Error("module source identity or resumed lexical scope failed: " + texts[i] + node.Diagnostic.Code)
		}
		engine.Free()
		program.Free()
		parsed.Free()
		registry.Free()
	}
}

func TestModuleSuspendsLoadsAndKeepsIdentity(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "/project/main.km", "module = (kash ./lib/main.kash)\nresult = module.value\n")
	eval.SetScriptSourceOrigin(parsed, 0, len(parsed.Source.Text), 1)
	program := eval.Compile(a, engine, parsed, registry)
	program.AddSourcePart(parsed.Source.Name, 0, len(parsed.Source.Text), 0)
	program.SetSourcePartDirectory("/project")
	module := program.Definition("module")
	engine.Request(module)
	engine.Step()
	request := program.Requests.Next()
	if !request.OK || request.Request.Kind != host.RequestLoadSource || host.PayloadPath(request.Request.Payload) != "/project/lib/main.kash" {
		t.Error("module did not submit its authored source path")
	}
	if request.OK {
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, HasValue: true, Value: core.NewBytes(a, []byte("value = 42\nunused = (fail! \"unreached\")\n"))})
		request.Request.Free(a)
	}
	for i := 0; i < 5 && !module.Current; i++ {
		engine.Step()
	}
	if !module.Current || module.Latest.Kind != core.Module || core.ValueSignature(module.Latest).Mode != core.SignatureContent {
		t.Error("module failed to publish with a stable identity")
	}
	result := program.Definition("result")
	engine.Request(result)
	for i := 0; i < 8 && !result.Current; i++ {
		engine.Step()
	}
	if !result.Current || result.Latest.Kind != core.Int || result.Latest.Int != 42 || program.Requests.Next().OK {
		t.Error("lazy module field failed or replayed a completed load")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestModuleHostCanRejectSourceLoad(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "/project/main.km", "module = (kash ./denied.kash)\n")
	eval.SetScriptSourceOrigin(parsed, 0, len(parsed.Source.Text), 1)
	program := eval.Compile(a, engine, parsed, registry)
	program.AddSourcePart(parsed.Source.Name, 0, len(parsed.Source.Text), 0)
	program.SetSourcePartDirectory("/project")
	node := program.Definition("module")
	engine.Request(node)
	engine.Step()
	request := program.Requests.Next()
	if !request.OK || request.Request.Kind != host.RequestLoadSource {
		t.Error("module did not submit a source request to the embedding host")
	}
	if request.OK {
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, Diagnostic: core.Diagnostic{Code: "FEATURE_UNSUP"}})
		request.Request.Free(a)
	}
	for i := 0; i < 5 && node.State != core.NodeFailed; i++ {
		engine.Step()
	}
	if node.State != core.NodeFailed || node.Diagnostic.Code != "FEATURE_UNSUP" || program.Requests.Next().OK {
		t.Error("unsupported embedding did not fail explicitly and release its load")
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}

func TestModuleCancellationReleasesPartialNestedLoad(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "/project/main.km", "module = (kash ./root.kash)\n")
	eval.SetScriptSourceOrigin(parsed, 0, len(parsed.Source.Text), 1)
	program := eval.Compile(a, engine, parsed, registry)
	program.AddSourcePart(parsed.Source.Name, 0, len(parsed.Source.Text), 0)
	program.SetSourcePartDirectory("/project")
	node := program.Definition("module")
	engine.Request(node)
	engine.Step()
	root := program.Requests.Next()
	if !root.OK || root.Request.Kind != host.RequestLoadSource {
		t.Error("root source load missing")
	}
	if root.OK {
		engine.Complete(core.Completion{NodeID: root.Request.NodeID, Generation: root.Request.Generation, Attempt: root.Request.Attempt, RequestID: root.Request.ID, HasValue: true, Value: core.NewBytes(a, []byte("(include ./child.kmk)\nvalue = 1\n"))})
		root.Request.Free(a)
	}
	child := program.Requests.Next()
	for i := 0; i < 8 && !child.OK; i++ {
		engine.Step()
		child = program.Requests.Next()
	}
	if !child.OK || child.Request.Kind != host.RequestLoadSource || host.PayloadPath(child.Request.Payload) != "/project/child.kmk" {
		t.Error("nested load did not suspend")
	}
	engine.Invalidate(node)
	if child.OK {
		engine.Complete(core.Completion{NodeID: child.Request.NodeID, Generation: child.Request.Generation, Attempt: child.Request.Attempt, RequestID: child.Request.ID, HasValue: true, Value: core.NewBytes(a, []byte("helper = 1\n"))})
		child.Request.Free(a)
	}
	engine.Free()
	program.Free()
	parsed.Free()
	registry.Free()
}
