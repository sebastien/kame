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
	"solod.dev/so/testing"
)

func globObservation(a mem.Allocator, names []string) core.Value {
	value := core.Value{Kind: core.List}
	for i := range names {
		value.List = slices.Append(a, value.List, core.NewString(a, names[i]))
	}
	return value
}

func settleGlobRoots(t *testing.T, p *program.Program, roots []*program.Handle, names []string, code string) {
	a := t.Allocator()
	for tick := 0; tick < 100; tick++ {
		p.Tick(0)
		for {
			next := p.NextOutbound()
			if !next.OK {
				break
			}
			request := next.Request
			if request.Kind == host.RequestProcessCancel {
				p.Complete(request, core.Value{Kind: core.Nil}, core.Diagnostic{})
				request.Free(a)
				continue
			}
			path := host.PayloadPath(request.Payload)
			if request.Kind != host.RequestReadFile || host.PayloadText(request.Payload, host.FieldOp) != host.OpWildcard || (path != "./items/*.txt" && path != "./other/*.txt") {
				t.Errorf("unexpected forwarded glob request identity: %d %s %s", int(request.Kind), host.PayloadText(request.Payload, host.FieldOp), path)
			}
			value := globObservation(a, names)
			diagnostic := core.Diagnostic{Code: code}
			if path == "./other/*.txt" {
				value.Free(a)
				value = globObservation(a, nil)
				diagnostic = core.Diagnostic{}
			}
			p.Complete(request, value, diagnostic)
			request.Free(a)
		}
		done := true
		for i := range roots {
			polled := roots[i].PollRetained()
			if !polled.Done {
				done = false
			}
			if polled.Done {
				wantCode := code
				want := globObservation(a, names)
				if i == 2 {
					wantCode = ""
					want.Free(a)
					want = globObservation(a, nil)
				}
				if polled.Result.Diagnostic.Code != wantCode {
					t.Error("glob root diagnostic mismatch")
				}
				if wantCode == "" && !core.ValueSignature(polled.Result.Value).Equal(core.ValueSignature(want)) {
					t.Error("glob root membership mismatch")
				}
				want.Free(a)
			}
			polled.Result.Free(a)
		}
		if done {
			return
		}
	}
	t.Error("glob roots did not settle")
}

func TestGlobRevalidationSharedMembershipLocalAndForwarded(t *testing.T) {
	a := t.Allocator()
	for mode := 0; mode < 2; mode++ {
		memory := wasm.NewMemoryHost(a)
		memory.Mkdir("items", 0o755)
		memory.Mkdir("other", 0o755)
		parsed := script.Parse(a, "watch.kmk", "ROOT = \"./items\"\nFILES = @(ROOT)/*.txt\none = FILES\ntwo = FILES\nisolated = ./other/*.txt\n")
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		directory := "."
		if mode == 1 {
			directory = "/embedding/project"
			memory.SetFile("items/decoy.txt", []byte("not the host snapshot"))
		}
		compiled := program.Compile(a, parsed, registry, program.Options{Host: memory, Directory: directory, ForwardRequests: mode == 1, Grants: []eval.Grant{{Capability: eval.Read}}})
		if compiled.Program == nil {
			t.Fatal("glob program compile failed")
			return
		}
		p := compiled.Program
		roots := []*program.Handle{p.Start("one").Handle, p.Start("two").Handle, p.Start("isolated").Handle}
		settleGlobRoots(t, p, roots, nil, "")
		isolated := roots[2].Node.Revision
		key := core.ResourceKey{Kind: core.ResourceGlob, Name: "./items/*.txt"}
		node := p.Engine.Lookup(key)
		if node == nil {
			t.Fatal("glob observation missing")
			return
		}
		previous := node.Signature
		for phase := 0; phase < 5; phase++ {
			names := []string{"./items/a.txt", "./items/b.txt"}
			if phase == 0 || phase == 1 {
				names = names[:1]
			}
			if phase == 3 {
				names = names[1:]
			}
			if phase == 4 {
				names = nil
			}
			if mode == 0 {
				memory.Remove("items/a.txt")
				memory.Remove("items/b.txt")
				for i := len(names) - 1; i >= 0; i-- {
					memory.SetFile(names[i][2:], []byte("present"))
				}
			}
			p.InvalidateResource(key)
			settleGlobRoots(t, p, roots, names, "")
			want := globObservation(a, names)
			if !node.Signature.Equal(core.ValueSignature(want)) {
				t.Error("glob signature is not host membership")
			}
			want.Free(a)
			if node.Signature.Equal(previous) != (phase == 1) {
				t.Error("changed/unchanged glob signature mismatch")
			}
			previous = node.Signature
			if roots[2].Node.Revision != isolated {
				t.Error("unrelated glob root invalidated")
			}
			for idle := 0; idle < 3; idle++ {
				p.Tick(0)
				if next := p.NextOutbound(); next.OK {
					t.Error("settled retained glob queued another read")
					p.Complete(next.Request, globObservation(a, names), core.Diagnostic{})
					next.Request.Free(a)
				}
				for i := range roots {
					polled := roots[i].PollRetained()
					if !polled.Done || roots[i].Node.Interest == 0 { t.Error("settled glob root lost retained interest") }
					polled.Result.Free(a)
				}
			}
		}
		for i := range roots {
			roots[i].Free()
		}
		p.Free()
		compiled.Free(a)
		registry.Free()
		parsed.Free()
	}
}

func TestForwardedGlobRevalidationFailureStaleAndCancellation(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "watch.kmk", "FILES = ./items/*.txt\none = FILES\ntwo = FILES\nisolated = ./other/*.txt\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: wasm.NewMemoryHost(a), Directory: ".", ForwardRequests: true, Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil {
		t.Fatal("glob program compile failed")
		return
	}
	p := compiled.Program
	defer parsed.Free()
	defer registry.Free()
	defer compiled.Free(a)
	defer p.Free()
	roots := []*program.Handle{p.Start("one").Handle, p.Start("two").Handle, p.Start("isolated").Handle}
	settleGlobRoots(t, p, roots, nil, "")
	key := core.ResourceKey{Kind: core.ResourceGlob, Name: "./items/*.txt"}
	p.InvalidateResource(key)
	settleGlobRoots(t, p, roots, nil, "FS_ERR")
	p.InvalidateResource(key)
	settleGlobRoots(t, p, roots, []string{"./items/a.txt"}, "")
	for invalid := 0; invalid < 2; invalid++ {
		p.InvalidateResource(key)
		answered := false
		for tick := 0; tick < 100; tick++ {
			p.Tick(0)
			next := p.NextOutbound()
			if !next.OK {
				continue
			}
			value := core.Value{Kind: core.Nil}
			if invalid == 1 {
				value = core.NewList(a, []core.Value{{Kind: core.Int, Int: 42}})
			}
			p.Complete(next.Request, value, core.Diagnostic{})
			next.Request.Free(a)
			answered = true
			break
		}
		if !answered {
			t.Error("missing malformed glob completion request")
		}
		settleGlobRoots(t, p, roots, nil, "HOST_FAIL")
		p.InvalidateResource(key)
		settleGlobRoots(t, p, roots, []string{"./items/a.txt"}, "")
	}
	p.InvalidateResource(key)
	var old host.Request
	for tick := 0; tick < 100; tick++ {
		p.Tick(0)
		next := p.NextOutbound()
		if next.OK {
			old = next.Request
			break
		}
	}
	if old.ID == 0 {
		t.Error("glob revalidation did not forward")
		for i := range roots {
			roots[i].Free()
		}
		return
	}
	p.InvalidateResource(key)
	p.Complete(old, globObservation(a, []string{"./items/stale.txt"}), core.Diagnostic{})
	old.Free(a)
	settleGlobRoots(t, p, roots, []string{"./items/b.txt"}, "")
	p.InvalidateResource(key)
	var pending host.Request
	for tick := 0; tick < 100; tick++ {
		p.Tick(0)
		next := p.NextOutbound()
		if next.OK {
			pending = next.Request
			break
		}
	}
	if pending.ID == 0 {
		t.Error("missing pending glob cancellation request")
	}
	for i := range roots {
		roots[i].Cancel()
		roots[i].Free()
	}
	if pending.ID != 0 {
		p.Complete(pending, globObservation(a, []string{"./items/cancelled.txt"}), core.Diagnostic{})
		pending.Free(a)
	}
	for tick := 0; tick < 10; tick++ {
		p.Tick(0)
	}
	if p.Engine.Lookup(key).Current {
		t.Error("cancelled glob completion published")
	}
}
