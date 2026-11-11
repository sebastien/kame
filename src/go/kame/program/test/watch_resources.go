package programcheck

import (
	"kame/core"
	"kame/host"
	"kame/host/wasm"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
)

func TestModuleWatchStartsFromAcceptedSourceBytes(t *testing.T) {
	a := t.Allocator()
	memory := wasm.NewMemoryHost(a)
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	compiled := program.CompileMany(a, []program.CompileSource{{Name: "/project/watch.kmk", SourceDirectory: "/project", Text: "module = (kash ./main.kash)\nresult = module.value\n"}}, registry, program.Options{Host: memory, Directory: "/project", ForwardRequests: true})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Fatal("module watch program compile failed")
		return
	}
	p := compiled.Program
	defer p.Free()
	root := p.Start("result").Handle
	defer root.Free()
	settled := false
	for tick := 0; tick < 100; tick++ {
		p.Tick(0)
		for {
			next := p.NextOutbound()
			if !next.OK {
				break
			}
			if next.Request.Kind != host.RequestLoadSource {
				t.Error("unexpected module watch request")
			}
			p.Complete(next.Request, core.NewBytes(a, []byte("value = 42\n")), core.Diagnostic{})
			next.Request.Free(a)
		}
		polled := root.PollRetained()
		settled = polled.Done
		if settled && (polled.Result.Diagnostic.Code != "" || polled.Result.Value.Int != 42) {
			t.Errorf("module watch root failed: %s %s", polled.Result.Diagnostic.Code, polled.Result.Diagnostic.Message)
		}
		polled.Result.Free(a)
		if settled {
			break
		}
	}
	if !settled {
		t.Fatal("module watch root did not settle")
		return
	}
	key := core.ResourceKey{Kind: core.ResourceFile, Name: "/project/main.kash"}
	if p.Engine.Lookup(key) != nil {
		t.Error("source load unexpectedly created a resource node")
	}
	keys := p.FilesystemResources()
	found := false
	for i := range keys {
		if keys[i].Kind == key.Kind && keys[i].Name == key.Name {
			found = true
		}
		keys[i].Free(a)
	}
	mem.FreeSlice(nil, keys)
	if !found {
		t.Error("accepted module source missing from watch resources")
	}
	memory.SetFile("/project/main.kash", []byte("value = 42\n"))
	expected := p.ResourceFingerprint(key)
	defer mem.FreeString(a, expected)
	before := p.ResourceAcceptedFingerprint(key)
	defer mem.FreeString(a, before)
	// The embedding's live bytes change before watch first subscribes.
	memory.SetFile("/project/main.kash", []byte("value = 99\n"))
	after := p.ResourceAcceptedFingerprint(key)
	defer mem.FreeString(a, after)
	live := p.ResourceFingerprint(key)
	defer mem.FreeString(a, live)
	if before != expected || before != after || after == live {
		t.Error("new watch swallowed an edit after accepted source publication")
	}
}

func TestSourceValidationUsesSourceAuthorityOnlyForLoadedContent(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	text := "result = 42\n"
	compiled := program.CompileMany(a, []program.CompileSource{{Name: "/project/main.kmk", SourceDirectory: "/project", Text: text, Signature: core.ContentSignature([]byte(text))}}, registry, program.Options{Host: wasm.NewMemoryHost(a), Directory: "/project", ForwardRequests: true})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Fatal("source authority program failed to compile")
		return
	}
	p := compiled.Program
	defer p.Free()
	for i := 0; i < 3; i++ {
		name, op := "/project/main.kmk", host.OpFileContent
		if i == 1 {
			name = "/project/unloaded.kmk"
		}
		if i == 2 {
			op = host.OpRead
		}
		p.Outbound = append(p.Outbound, host.Request{Kind: host.RequestReadFile, Payload: host.FilePayload(a, op, name)})
		next := p.NextOutbound()
		if i == 0 {
			if next.Request.Kind != host.RequestLoadSource || next.Request.Payload.Kind != core.String || next.Request.Payload.Text != name {
				t.Error("loaded source validation required an ordinary read grant")
			}
		} else if next.Request.Kind != host.RequestReadFile || host.PayloadText(next.Request.Payload, host.FieldOp) != op {
			t.Error("source validation widened ordinary read authority")
		}
		next.Request.Free(a)
	}
}
