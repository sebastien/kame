package evalcheck

import (
	"kame/core"
	"kame/host"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/lang/source"
	"kame/operations"
)

func compileImportedModules(t *testing.T, text string) (*core.Engine, *eval.Program, func()) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.ParseKameFile(a, "/project/main.km", text)
	eval.SetScriptSourceOrigin(parsed, 0, len(parsed.Source.Text), 1)
	compiled := eval.CompileCheckedForProgram(a, engine, parsed, registry)
	defer compiled.Free(a)
	program := compiled.Program
	if program == nil {
		engine.Free()
		parsed.Free()
		registry.Free()
		t.Fatalf("import fixture did not compile: %v", compiled.Diagnostics)
	}
	program.AddSourcePart(parsed.Source.Name, 0, len(parsed.Source.Text), 0)
	program.SetSourcePartDirectory("/project")
	d := program.RegisterImports()
	if len(parsed.Diagnostics) != 0 || d.Code != "" {
		t.Errorf("import fixture did not compile: %v %s", parsed.Diagnostics, d.Code)
	}
	d.Free(a)
	return engine, program, func() {
		engine.Free()
		program.Free()
		parsed.Free()
		registry.Free()
	}
}

// Module sources arrive through the same completion boundary as a hosted build.
func moduleSourceRequest(engine *core.Engine, program *eval.Program) host.Request {
	for tick := 0; tick < 25; tick++ {
		engine.Step()
		if request := program.Requests.Next(); request.OK {
			return request.Request
		}
	}
	return host.Request{}
}

func TestModuleSourceIdentityAndResumedLexicalScope(t *testing.T) {
	a := t.Allocator()
	engine, program, free := compileImportedModules(t, "import ./a.kash\nimport ./b.kash\nx = 99\nresult = [a/value, b/value, x]\n")
	defer free()
	sources := map[string]string{
		"/project/a.kash":     "import ./a/one.kash\ndef value = one/value\nexport value\n",
		"/project/b.kash":     "import ./b/one.kash\ndef value = one/value\nexport value\n",
		"/project/a/one.kash": "def x = 1\ndef value = x\nexport value\n",
		"/project/b/one.kash": "def x = 2\ndef value = x\nexport value\n",
	}
	node := program.Definition("result")
	engine.Request(node)
	for tick := 0; tick < 100 && !node.Current && node.State != core.NodeFailed; tick++ {
		engine.Step()
		request := program.Requests.Next()
		if !request.OK {
			continue
		}
		name := host.PayloadPath(request.Request.Payload)
		text := sources[name]
		if text == "" || request.Request.Kind != host.RequestLoadSource {
			t.Errorf("unexpected source request: %s", name)
		}
		delete(sources, name)
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, HasValue: true, Value: core.NewBytes(a, []byte(text))})
		request.Request.Free(a)
	}
	if !node.Current || node.Latest.Kind != core.List || len(node.Latest.List) != 3 ||
		node.Latest.List[0].Int != 1 || node.Latest.List[1].Int != 2 || node.Latest.List[2].Int != 99 ||
		len(sources) != 0 {
		t.Error("relative module identity or lexical isolation failed: " + node.Diagnostic.Code)
	}
}

func TestModuleSuspendsLoadsAndKeepsIdentity(t *testing.T) {
	a := t.Allocator()
	engine, program, free := compileImportedModules(t, "import ./lib/main.kash as lib\nfirst = lib/value\nsecond = lib/value\n")
	defer free()
	first := program.Definition("first")
	engine.Request(first)
	request := moduleSourceRequest(engine, program)
	defer request.Free(a)
	if request.ID == 0 || request.Kind != host.RequestLoadSource || host.PayloadPath(request.Payload) != "/project/lib/main.kash" {
		t.Fatal("module did not submit its authored source path")
		return
	}
	if first.Current {
		t.Error("value published before source completion")
	}
	engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, HasValue: true, Value: core.NewBytes(a, []byte("def value = 42\ndef unused = fail! \"unreached\"\nexport value, unused\n"))})
	for _, name := range []string{"first", "second"} {
		node := program.Definition(name)
		engine.Request(node)
		for i := 0; i < 25 && !node.Current && node.State != core.NodeFailed; i++ {
			engine.Step()
		}
		if !node.Current || node.Latest.Kind != core.Int || node.Latest.Int != 42 {
			t.Errorf("module field did not materialize: %s", node.Diagnostic.Code)
		}
	}
	if extra := program.Requests.Next(); extra.OK {
		extra.Request.Free(a)
		t.Error("replayed a completed module load")
	}
	value := program.Modules[0].Value()
	clone := value.Clone(a)
	value.Free(a)
	again := program.Modules[0].Value()
	if clone.Module != again.Module || !core.ValueSignature(clone).Equal(core.ValueSignature(again)) {
		t.Error("repeated module references lost their identity or content signature")
	}
	foreign := eval.Context{Program: &eval.Program{}, Run: a}
	field := foreign.ModuleField(clone, "value", source.Span{})
	if field.Result.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("another invocation accessed this module")
	}
	field.Result.Free(a)
	clone.Free(a)
}

func TestModuleHostCanRejectSourceLoad(t *testing.T) {
	a := t.Allocator()
	engine, program, free := compileImportedModules(t, "from ./denied.kash import value\nresult = value\n")
	defer free()
	node := program.Definition("result")
	engine.Request(node)
	request := moduleSourceRequest(engine, program)
	defer request.Free(a)
	if request.ID == 0 || request.Kind != host.RequestLoadSource {
		t.Fatal("module did not submit a source request to the embedding host")
		return
	}
	engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, Diagnostic: core.Diagnostic{Code: "FEATURE_UNSUP"}})
	for i := 0; i < 25 && node.State != core.NodeFailed; i++ {
		engine.Step()
	}
	if node.State != core.NodeFailed || node.Diagnostic.Code != "FEATURE_UNSUP" {
		t.Errorf("unsupported embedding did not fail explicitly: %s", node.Diagnostic.Code)
	}
	if extra := program.Requests.Next(); extra.OK {
		extra.Request.Free(a)
		t.Error("replayed a rejected module load")
	}
}

func TestModuleCancellationReleasesPartialNestedLoad(t *testing.T) {
	a := t.Allocator()
	engine, program, free := compileImportedModules(t, "from ./root.kash import value\nresult = value\n")
	defer free()
	node := program.Definition("result")
	interest := engine.RequestRoot(node)
	root := moduleSourceRequest(engine, program)
	defer root.Free(a)
	if root.ID == 0 || root.Kind != host.RequestLoadSource || host.PayloadPath(root.Payload) != "/project/root.kash" {
		t.Fatal("root source load missing")
		return
	}
	engine.Complete(core.Completion{NodeID: root.NodeID, Generation: root.Generation, Attempt: root.Attempt, RequestID: root.ID, HasValue: true, Value: core.NewBytes(a, []byte("include ./child.kash\ndef value = helper\nexport value\n"))})
	child := moduleSourceRequest(engine, program)
	defer child.Free(a)
	if child.ID == 0 || child.Kind != host.RequestLoadSource || host.PayloadPath(child.Payload) != "/project/child.kash" {
		t.Fatal("nested load did not suspend")
		return
	}
	engine.Release(interest)
	engine.Invalidate(node)
	engine.Complete(core.Completion{NodeID: child.NodeID, Generation: child.Generation, Attempt: child.Attempt, RequestID: child.ID, HasValue: true, Value: core.NewBytes(a, []byte("def helper = 1\n"))})
	if node.Current {
		t.Error("discarded module load published a value")
	}
}
