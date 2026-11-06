package program_test

import (
	"kame/core"
	"kame/host"
	"kame/host/wasm"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestProductionInputChangesWarnAndInvalidateDerivedRecords(t *testing.T) {
	a := t.Allocator()
	for mode := 0; mode < 5; mode++ {
		memory := wasm.NewMemoryHost(a)
		memory.SetFile("declared", []byte("A"))
		memory.SetFile("dynamic", []byte("B"))
		memory.SetFile("order", []byte("order"))
		parsed := script.Parse(a, "changing.kmk", "./child : ./declared | ./order ; [shell: kame]\n\t(str (count (files ./dynamic)) (text (read ./dynamic)))\n./output : ./child ; [shell: kame]\n\t(read ./child)\n")
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		compiled := program.Compile(a, parsed, registry, program.Options{Host: memory, Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
		if compiled.Program == nil {
			t.Fatal("production change program failed to compile")
			return
		}
		p := compiled.Program
		started := p.Start("./output")
		if started.Handle == nil {
			t.Fatal("production change root failed to start")
			return
		}
		changed, done := false, false
		for i := 0; i < 200 && !done; i++ {
			p.Tick(0)
			if !changed && memory.Stat("child").Exists {
				changed = true
				if mode == 1 {
					memory.SetFile("declared", []byte("changed"))
					memory.SetFile("dynamic", []byte("changed"))
				} else if mode == 2 {
					memory.Remove("dynamic")
				} else if mode == 3 {
					memory.SetFile("order", []byte("changed"))
				} else if mode == 4 {
					p.NoteProductionInputChange(core.ResourceKey{Kind: core.ResourceFile, Name: "declared"})
				}
			}
			polled := started.Handle.Poll()
			done = polled.Done
			if polled.Result.Diagnostic.Code != "" {
				t.Error("input warning changed success status: " + polled.Result.Diagnostic.Code)
			}
			polled.Result.Free(a)
		}
		if !done || !changed {
			t.Error("production change build did not settle")
		}
		started.Handle.Free()
		warnings := 0
		for {
			next := p.NextEvent()
			if !next.OK {
				break
			}
			if next.Event.Kind == program.InputWarning {
				warnings++
				if next.Event.Diagnostic.Code != "INPUT_UNSTABLE" || !strings.Contains(next.Event.Diagnostic.Message, "run the build again") {
					t.Error("missing recovery guidance")
				}
				if mode == 1 && (!strings.Contains(next.Event.Diagnostic.Message, "declared") || !strings.Contains(next.Event.Diagnostic.Message, "dynamic")) {
					t.Error("warning omitted a changed declared or discovered input")
				}
			}
			next.Event.Free(a)
		}
		unstable := mode == 1 || mode == 2 || mode == 4
		wantWarnings := 0
		if unstable {
			wantWarnings = 1
		}
		if warnings != wantWarnings {
			t.Errorf("mode %d: got %d warnings, want %d", mode, warnings, wantWarnings)
		}
		if unstable && len(p.ChangedProductionInputs()) == 0 {
			t.Errorf("mode %d: changed inputs were not reported", mode)
		}
		// Restore the original bytes: a suspect proof must not become usable just
		// because the final input or output bytes happen to match an earlier run.
		memory.SetFile("declared", []byte("A"))
		memory.SetFile("dynamic", []byte("B"))
		recovery := p.Start("./output")
		if len(p.ChangedProductionInputs()) != 0 {
			t.Errorf("mode %d: a new root retained stale changed inputs", mode)
		}
		done = false
		for i := 0; i < 200 && !done; i++ {
			p.Tick(0)
			polled := recovery.Handle.Poll()
			done = polled.Done
			if done && (polled.Result.Diagnostic.Code != "" || polled.Result.Fresh == unstable) {
				t.Errorf("mode %d: recovery freshness=%t, diagnostic=%s", mode, polled.Result.Fresh, polled.Result.Diagnostic.Code)
			}
			polled.Result.Free(a)
		}
		if !done {
			t.Errorf("mode %d: recovery did not settle", mode)
		}
		recovery.Handle.Free()
		p.Free()
		compiled.Free(a)
		registry.Free()
		parsed.Free()
	}
}

func TestProductionInputValidationUsesForwardedHostAndPreservesSuccess(t *testing.T) {
	a := t.Allocator()
	for mode := 0; mode < 4; mode++ {
		memory := wasm.NewMemoryHost(a)
		memory.SetFile("input", []byte("decoy: never consult the local forwarding host"))
		parsed := script.Parse(a, "forwarded.kmk", "run : ./input\n\tcompiler\n")
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		compiled := program.Compile(a, parsed, registry, program.Options{Host: memory, Directory: ".", ForwardRequests: true})
		if compiled.Program == nil {
			t.Fatal("forwarded production program failed to compile")
			return
		}
		p := compiled.Program
		started := p.Start("run")
		if started.Handle == nil {
			t.Fatal("forwarded production failed to start")
			return
		}
		executed, done := false, false
		for i := 0; i < 200 && !done; i++ {
			p.Tick(0)
			for {
				next := p.NextOutbound()
				if !next.OK {
					break
				}
				request := next.Request
				if request.Kind == host.RequestProcess {
					executed = true
					p.ProcessTerminal(request, nil, nil, 0, 0, 0, "", "")
				} else if request.Kind == host.RequestReadFile && host.PayloadText(request.Payload, host.FieldOp) == host.OpFileContent {
					value := core.NewBytes(a, []byte("before"))
					if executed && mode != 0 {
						value.Free(a)
						value = core.Value{Kind: core.Nil}
						if mode == 1 {
							value = core.NewBytes(a, []byte("after"))
						} else if mode == 3 {
							value = core.Value{Kind: core.Bool, Bool: true}
						}
					}
					p.Complete(request, value, core.Diagnostic{})
				} else {
					p.Complete(request, core.Value{Kind: core.Nil}, core.Diagnostic{})
				}
				request.Free(a)
			}
			polled := started.Handle.Poll()
			done = polled.Done
			if polled.Result.Diagnostic.Code != "" {
				t.Error("forwarded validation changed execution outcome")
			}
			polled.Result.Free(a)
		}
		if !done || !executed {
			t.Error("forwarded production did not settle")
		}
		warnings := 0
		for {
			next := p.NextEvent()
			if !next.OK {
				break
			}
			if next.Event.Kind == program.InputWarning {
				warnings++
				if mode == 3 && (!strings.Contains(next.Event.Diagnostic.Message, "unverifiable: ./input") || strings.Contains(next.Event.Diagnostic.Message, "changed: ./input")) {
					t.Error("unavailable proof was asserted to be changed content")
				}
			}
			next.Event.Free(a)
		}
		wantWarnings := 1
		if mode == 0 {
			wantWarnings = 0
		}
		if warnings != wantWarnings {
			t.Errorf("forwarded mode %d: got %d warnings", mode, warnings)
		}
		started.Handle.Free()
		p.Free()
		compiled.Free(a)
		registry.Free()
		parsed.Free()
	}
}

func TestProductionInputChangeDiscardsCachedTaskRecord(t *testing.T) {
	a := t.Allocator()
	memory := wasm.NewMemoryHost(a)
	memory.SetFile("input", []byte("before"))
	parsed := script.Parse(a, "task.kmk", "task run : ./input ; [shell: kash]\n\t(out! \"produced\")\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: memory, Directory: "."})
	if compiled.Program == nil {
		t.Fatal("cached production program failed to compile")
		return
	}
	p := compiled.Program
	started := p.Start("run")
	if started.Handle == nil {
		t.Fatal("cached production failed to start")
		return
	}
	changed, done := false, false
	for i := 0; i < 200 && !done; i++ {
		p.Tick(0)
		for {
			next := p.NextEvent()
			if !next.OK {
				break
			}
			if next.Event.Kind == program.Stdout && !changed {
				memory.SetFile("input", []byte("after"))
				changed = true
			}
			next.Event.Free(a)
		}
		polled := started.Handle.Poll()
		done = polled.Done
		if polled.Result.Diagnostic.Code != "" {
			t.Error("cached production changed success status")
		}
		polled.Result.Free(a)
	}
	if !done || !changed {
		t.Error("cached production did not settle")
	}
	started.Handle.Free()
	entries, _ := memory.ReadDir(a, ".kame/cache/tasks")
	records := 0
	for i := range entries {
		if !entries[i].IsDir {
			records++
		}
		mem.FreeString(a, entries[i].Name)
	}
	slices.Free(a, entries)
	if records != 0 {
		t.Error("suspect cached task record was not removed")
	}
	memory.SetFile("input", []byte("before"))
	result := p.Materialize("run")
	if result.Diagnostic.Code != "" {
		t.Error("cached production recovery failed")
	}
	result.Free(a)
	produced := false
	for {
		next := p.NextEvent()
		if !next.OK {
			break
		}
		if next.Event.Kind == program.Stdout {
			produced = true
			if next.Event.Cached {
				t.Error("suspect cached task record was replayed")
			}
		}
		next.Event.Free(a)
	}
	if !produced {
		t.Error("cached task recovery did not execute")
	}
	p.Free()
	compiled.Free(a)
	registry.Free()
	parsed.Free()
}
