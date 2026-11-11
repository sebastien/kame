package programcheck

import (
	"kame/core"
	"kame/host"
	"kame/host/wasm"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"path"
	"strconv"
	"strings"
)

func inputSnapshotAllocations(t *testing.T, count int) uint64 {
	tracker := mem.Tracker{Allocator: t.Allocator()}
	var a mem.Allocator = &tracker
	memory := wasm.NewMemoryHost(a)
	memory.Mkdir("inputs", 0o755)
	memory.SetFile("extra", []byte("A"))
	source := strings.Builder{}
	defer source.Reset()
	source.WriteString("INPUTS = [")
	for i := 0; i < count; i++ {

		name := core.NewString(a, "inputs/"+strconv.Itoa(i))
		memory.SetFile(name.Text, []byte("source"))
		source.WriteString(" ./" + name.Text)
		name.Free(a)
	}
	source.WriteString("]\n./output : @{INPUTS} ; [shell: kame]\n\t(text (read ./extra))\n")
	parsed := script.Parse(a, "snapshot.kmk", source.String())
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	defer registry.Free()
	allocations := uint64(0)
	for phase := 0; phase < 4; phase++ {
		if phase == 2 {
			memory.SetFile("extra", []byte("B"))
		}
		if phase == 3 {
			memory.SetFile("inputs/0", []byte("changed"))
		}
		compiled := program.Compile(a, parsed, registry, program.Options{Host: memory, Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
		if compiled.Program == nil {
			memory.Free()
			t.Fatal("snapshot program compile failed")
			return 0
		}
		before := tracker.Stats()
		result := compiled.Program.Materialize("./output")
		after := tracker.Stats()
		if phase == 1 {
			allocations = after.Mallocs - before.Mallocs
		}
		if result.Diagnostic.Code != "" || result.Fresh != (phase == 1) {
			t.Errorf("snapshot phase %d: %s, fresh=%t", phase, result.Diagnostic.Code, result.Fresh)
		}
		want := "A"
		if phase >= 2 {
			want = "B"
		}
		output, err := memory.ReadFile(a, "output")
		if err != nil || string(output) != want {
			t.Error("snapshot hid a changed input")
		}
		mem.FreeSlice(a, output)
		result.Free(a)
		var next *wasm.MemoryHost
		if phase < 3 {
			// A new Program must revalidate persisted proofs, not reuse live nodes.
			next = wasm.NewMemoryHost(a)
			dirs := []string{".", "inputs", ".kame/cache/file-context"}
			for _, dir := range dirs {
				next.Mkdir(dir, 0o755)
				entries, readErr := memory.ReadDir(a, dir)
				if readErr != nil {
					t.Error("snapshot directory missing")
				}
				for i := range entries {
					if !entries[i].IsDir {
						name := path.Join(dir, entries[i].Name)
						data, fileErr := memory.ReadFile(a, name)
						if fileErr != nil {
							t.Error("snapshot file missing")
						}
						next.SetFile(name, data)
						mem.FreeSlice(a, data)
						mem.FreeString(a, name)
					}
					mem.FreeString(a, entries[i].Name)
				}
				mem.FreeSlice(a, entries)
			}
		}
		compiled.Program.Free()
		compiled.Free(a)
		memory = next
	}
	return allocations
}

func TestFileContextInputSnapshotAvoidsRepeatedGraphCollection(t *testing.T) {
	small := inputSnapshotAllocations(t, 32)
	large := inputSnapshotAllocations(t, 128)
	// Four times the inputs must not produce quadratic graph-copy allocations.
	if small == 0 || large > 5*small {
		t.Errorf("freshness graph collection allocations grew from %d to %d", small, large)
	}
}

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

func TestForwardedContentDigestMatchesByteContent(t *testing.T) {
	fields := []core.RecordField{{Key: "sha256", Value: core.Value{Kind: core.String, Text: "559aead08264d5795d3909718cdd05abd49572e84fe55590eef31a88a08fdffd"}}}
	actual := externalFileSignature(t, true, core.Value{Kind: core.Record, Record: fields})
	if !actual.Equal(core.ContentSignature([]byte("A"))) {
		t.Error("forwarded SHA-256 record changed content identity")
	}
	fields[0].Value.Text = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	actual = externalFileSignature(t, true, core.Value{Kind: core.Record, Record: fields})
	if !actual.Equal(core.ContentSignature(nil)) {
		t.Error("empty file digest was treated as missing")
	}
}
