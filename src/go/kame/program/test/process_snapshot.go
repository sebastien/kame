package programcheck

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
)

func TestProcessSnapshotsCoalesceAndRejectStaleRequests(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	parsed := script.Parse(a, "live.kmk", "SHELL = kash\nprobe :\n\t(printf x, printf y) |(merge) cat\n")
	defer parsed.Free()
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), ForwardRequests: true, Directory: ".", Grants: []eval.Grant{{Capability: eval.Run}}})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Fatal("snapshot program failed to compile")
		return
	}
	p := compiled.Program
	defer p.Free()
	started := p.Start("probe")
	if started.Handle == nil {
		started.Diagnostic.Free(a)
		t.Fatal("snapshot program failed to start")
		return
	}
	defer started.Handle.Free()
	defer started.Diagnostic.Free(a)
	seen := false
	for tick := 0; tick < 100 && !seen; tick++ {
		p.Tick(0)
		for {
			next := p.NextOutbound()
			if !next.OK {
				break
			}
			request := next.Request
			if request.Kind == host.RequestProcess {
				seen = true
				states := make([]int, len(host.PayloadList(request.Payload, "kinds")))
				for i := range states {
					states[i] = 2
				}
				value := host.ProcessSnapshotPayload(a, states, nil)
				for i := 0; i < 1000; i++ {
					if !p.ProcessGraphSnapshot(request, value) {
						t.Error("valid snapshot rejected")
						break
					}
				}
				value.Free(a)
				states[0] = 8
				value = host.ProcessSnapshotPayload(a, states, nil)
				if p.ProcessGraphSnapshot(request, value) {
					t.Error("invalid state accepted")
				}
				value.Free(a)
				snapshots := 0
				for {
					event := p.NextEvent()
					if !event.OK {
						break
					}
					if event.Event.Kind == program.ProcessSnapshot {
						snapshots++
						if event.Event.RequestID != request.ID {
							t.Error("snapshot lost request identity")
						}
					}
					event.Event.Free(a)
				}
				if snapshots != 1 {
					t.Error("unread snapshots did not coalesce")
				}
				p.ProcessTerminal(request, nil, nil, 0, 0, 0, "", "")
				states[0] = 4
				value = host.ProcessSnapshotPayload(a, states, nil)
				if p.ProcessGraphSnapshot(request, value) {
					t.Error("completed request accepted stale snapshot")
				}
				value.Free(a)
				mem.FreeSlice(nil, states)
			} else if request.Kind == host.RequestReadFile && host.PayloadText(request.Payload, host.FieldOp) == "resolve-tool" {
				p.Complete(request, core.NewString(a, "/bin/sh"), diagnostic.Diagnostic{})
			} else if request.Kind == host.RequestReadFile && host.PayloadText(request.Payload, host.FieldOp) == host.OpToolContent {
				p.Complete(request, core.NewBytes(a, []byte("executable identity")), diagnostic.Diagnostic{})
			} else {
				p.Complete(request, core.Value{Kind: core.Nil}, diagnostic.Diagnostic{})
			}
			request.Free(a)
		}
	}
	if !seen {
		t.Error("graph never reached host")
	}
}
