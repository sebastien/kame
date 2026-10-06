package program_test

import (
	"kame/core"
	"kame/host"
	"kame/host/wasm"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

// The forwarded ProgramHost deliberately contains unrelated bytes. Its
// signature must come from outbound host completions, never MemoryHost.
func externalFileSignature(t *testing.T, forwarded bool, content core.Value) core.Signature {
	a := t.Allocator()
	memory := wasm.NewMemoryHost(a)
	if forwarded {
		memory.SetFile("input", []byte("decoy"))
	} else if content.Kind == core.Bytes {
		memory.SetFile("input", content.Bytes)
	} else if content.Kind == core.Bool {
		memory.Mkdir("input", 0o755)
	}
	parsed := script.Parse(a, "test.kmk", "value = (exists? \"./input\")\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: memory, Directory: ".", ForwardRequests: forwarded, Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil {
		t.Fatal("file signature program failed to compile")
		return core.Signature{}
	}
	p := compiled.Program
	started := p.Start("value")
	if started.Handle == nil {
		t.Fatal("file signature root failed to start")
		return core.Signature{}
	}
	done := false
	for i := 0; i < 30 && !done; i++ {
		p.Tick(0)
		for {
			next := p.NextOutbound()
			if !next.OK {
				break
			}
			request := next.Request
			op := host.PayloadText(request.Payload, host.FieldOp)
			if op == host.OpFileContent {
				p.Complete(request, content.Clone(a), core.Diagnostic{})
			} else if op == host.OpExists {
				p.Complete(request, core.Value{Kind: core.Bool, Bool: content.Kind != core.Nil}, core.Diagnostic{})
			} else {
				t.Error("unexpected external file request")
				p.Complete(request, core.Value{}, core.Diagnostic{Code: "HOST_FAIL"})
			}
			request.Free(a)
		}
		result := started.Handle.Poll()
		done = result.Done
		if result.Result.Diagnostic.Code != "" {
			t.Error(result.Result.Diagnostic.Code)
		}
		result.Result.Free(a)
	}
	if !done {
		t.Error("external file did not complete")
	}
	node := p.Engine.Lookup(core.ResourceKey{Kind: core.ResourceFile, Name: "input"})
	signature := core.Signature{}
	if node == nil || !node.Current {
		t.Error("external file has no accepted signature")
	} else {
		signature = node.Signature
		if content.Kind == core.Nil && node.Latest.Kind != core.Nil {
			t.Error("missing file published a path")
		}
		if content.Kind != core.Nil && node.Latest.Kind != core.String {
			t.Error("present file lost its path value")
		}
	}
	started.Handle.Free()
	p.Free()
	compiled.Free(a)
	registry.Free()
	parsed.Free()
	return signature
}

func TestExternalFileContentSignaturesMatchNativeAndForwardedHosts(t *testing.T) {
	values := []core.Value{{Kind: core.Bytes, Bytes: []byte("A")}, {Kind: core.Bytes, Bytes: []byte("B")}, {Kind: core.Bytes}, {Kind: core.Nil}, {Kind: core.Bool}}
	for i := range values {
		native := externalFileSignature(t, false, values[i])
		forwarded := externalFileSignature(t, true, values[i])
		if values[i].Kind == core.Bool {
			if native.Equal(native) || forwarded.Equal(forwarded) {
				t.Error("non-regular files have reusable content identity")
			}
		} else if !native.Equal(forwarded) {
			t.Error("native and forwarded content signatures differ")
		}
		if values[i].Kind == core.Bytes && !native.Equal(core.ContentSignature(values[i].Bytes)) {
			t.Error("file path or metadata replaced content identity")
		}
		if values[i].Kind == core.Nil && native.Mode != core.SignatureMissing {
			t.Error("missing file has ordinary value identity")
		}
	}
	unreadable := externalFileSignature(t, true, core.Value{Kind: core.Bool, Bool: true})
	if unreadable.Equal(unreadable) || unreadable.Mode != core.SignatureUnavailable {
		t.Error("read failure supplied a reusable signature")
	}
}
