package program_test

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

func TestTargetShadowDiagnosticOwnsMessageAndSource(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	parsed := script.Parse(a, "arguments.kmk", "region = \"global\"\ndeploy {region=west} :\n")
	compiled := program.Compile(a, parsed, registry, program.Options{})
	parsed.Free()
	defer compiled.Free(a)
	if compiled.Program != nil {
		compiled.Program.Free()
		t.Fatal("shadowing unexpectedly compiled")
		return
	}
	if len(compiled.Diagnostics) != 1 {
		t.Fatal("missing target argument diagnostic")
		return
	}
	d := compiled.Diagnostics[0]
	if !d.Owned || d.Code != "TGT_ARGUMENT" || d.Source != "arguments.kmk" || d.Message != "target argument shadows definition: region" {
		t.Error("returned diagnostic did not retain its message and source")
	}
}

func TestSessionCompileFreeResetsOnlyEnvelope(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	fragments := []program.Fragment{{Name: "values.km", Lang: "km", Text: "answer = 42\n", Entries: []string{"answer"}}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	session := compiled.Session
	if session == nil {
		compiled.Free(a)
		t.Fatal("session compilation failed")
		return
	}
	defer session.Free()
	compiled.Free(a)
	compiled.Free(a)
	if compiled.Session != nil || len(compiled.Diagnostics) != 0 {
		t.Error("compilation envelope was not reset")
	}
	started := session.Start(0)
	defer started.Diagnostic.Free(a)
	if started.Handle == nil {
		t.Fatal("freeing the envelope destroyed its session")
		return
	}
	defer started.Handle.Free()
	for i := 0; i < 20; i++ {
		session.Program.Tick(0)
		polled := started.Handle.Poll()
		if polled.Done {
			if polled.Result.Diagnostic.Code != "" || polled.Result.Value.Kind != core.Int || polled.Result.Value.Int != 42 {
				t.Error("retained session no longer evaluates")
			}
			polled.Result.Free(a)
			return
		}
		polled.Result.Free(a)
	}
	t.Error("retained session did not complete")
}

func TestSessionCompileFailureFreeIsRepeatable(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	fragments := []program.Fragment{{Name: "one.km", Lang: "km", Text: "name = 1\n"}, {Name: "two.km", Lang: "km", Text: "name = 2\n"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	if compiled.Session != nil {
		compiled.Session.Free()
	}
	if len(compiled.Diagnostics) == 0 {
		t.Error("missing duplicate-definition failure")
	}
	compiled.Free(a)
	compiled.Free(a)
	if len(compiled.Diagnostics) != 0 {
		t.Error("freed diagnostics remain reachable")
	}
}

func TestForwardedProcessCompletionRetiresDisplay(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	for mode := 0; mode < 2; mode++ {
		parsed := script.Parse(a, "pending.kmk", "value = (sh! \"printf ok\")\n")
		compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), ForwardRequests: true, Grants: []eval.Grant{{Capability: eval.Run}}})
		if compiled.Program == nil {
			compiled.Free(a)
			parsed.Free()
			t.Fatal("forwarded program compilation failed")
			return
		}
		p := compiled.Program
		started := p.Start("value")
		if started.Handle == nil {
			started.Diagnostic.Free(a)
			p.Free()
			compiled.Free(a)
			parsed.Free()
			t.Fatal("forwarded value did not start")
			return
		}
		completed := false
		for i := 0; i < 100 && !completed; i++ {
			p.Tick(0)
			for {
				next := p.NextOutbound()
				if !next.OK {
					break
				}
				if next.Request.Kind == host.RequestProcess {
					if len(p.Pending) != 1 {
						t.Error("missing active process display")
					}
					if mode == 0 {
						p.ProcessTerminal(next.Request, []byte("ok"), nil, 0, 0, 0, "", "")
					} else {
						fields := []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int}}, {Key: "stdout", Value: core.NewBytes(a, []byte("ok"))}, {Key: "stderr", Value: core.Value{Kind: core.Bytes}}, {Key: "signal", Value: core.Value{Kind: core.Int}}}
						value := core.NewRecord(a, fields)
						fields[1].Value.Free(a)
						p.Complete(next.Request, value, diagnostic.Diagnostic{})
					}
					if len(p.Pending) != 0 {
						t.Error("terminal process display was retained")
					}
				} else {
					p.Complete(next.Request, core.Value{Kind: core.Nil}, diagnostic.Diagnostic{})
				}
				next.Request.Free(a)
			}
			polled := started.Handle.Poll()
			completed = polled.Done
			if completed && polled.Result.Diagnostic.Code != "" {
				t.Error("forwarded completion failed")
			}
			polled.Result.Free(a)
		}
		if !completed {
			t.Error("forwarded value did not complete")
		}
		started.Handle.Free()
		started.Diagnostic.Free(a)
		p.Free()
		compiled.Free(a)
		parsed.Free()
	}
}
