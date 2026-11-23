package program

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/operations"
	"testing"
)

func TestAuthoredImportsLoadEachNormalizedSourceOnce(t *testing.T) {
	a := mem.System
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	compiled := CompileMany(a, []CompileSource{{
		Name: "/project/main.km", SourceDirectory: "/project",
		Text: "import ./leaf as leaf\nfrom ./sub/../leaf.kash import answer as copy\nimport ./facade.km\nresult = leaf/answer + copy + facade/answer\n",
	}}, registry, Options{Directory: "/project", ForwardRequests: true})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Fatal("module program failed to compile")
		return
	}
	p := compiled.Program
	defer p.Free()
	root := p.Start("result").Handle
	if root == nil {
		t.Fatal("module result could not start")
		return
	}
	defer root.Free()
	loads := make(map[string]int)
	for tick := 0; tick < 100; tick++ {
		p.Tick(0)
		for {
			next := p.NextOutbound()
			if !next.OK {
				break
			}
			name := next.Request.Payload.Text
			loads[name]++
			if next.Request.Kind != host.RequestLoadSource {
				t.Error("unexpected request while loading authored modules")
			}
			value := core.Value{Kind: core.Nil}
			switch name {
			case "/project/leaf.kash":
				value = core.NewBytes(a, []byte("export answer\ndef answer = 14\n"))
			case "/project/facade.km":
				value = core.NewBytes(a, []byte("include ./helper.km\nexport answer\n"))
			case "/project/helper.km":
				value = core.NewBytes(a, []byte("import ./leaf.kash\nanswer = leaf/answer\n"))
			}
			p.Complete(next.Request, value, core.Diagnostic{})
			next.Request.Free(a)
		}
		poll := root.PollRetained()
		if poll.Done {
			if poll.Result.Diagnostic.Code != "" || poll.Result.Value.Kind != core.Int || poll.Result.Value.Int != 42 {
				t.Errorf("module result failed: %s %s", poll.Result.Diagnostic.Code, poll.Result.Diagnostic.Message)
			}
			poll.Result.Free(a)
			for _, name := range []string{"leaf.km", "leaf.kame", "leaf.kash", "leaf.ksh", "facade.km", "helper.km"} {
				if loads["/project/"+name] != 1 {
					t.Errorf("expected one source request for %s, got %d", name, loads["/project/"+name])
				}
			}
			if len(loads) != 6 || len(p.Eval.Modules) != 2 {
				t.Error("unexpected additional source requests or parsed modules")
			}
			return
		}
		poll.Result.Free(a)
	}
	t.Fatal("module result did not settle")
}

func TestModuleSourceEditRejectsInFlightCompletion(t *testing.T) {
	a := mem.System
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	compiled := CompileMany(a, []CompileSource{{
		Name: "/project/main.km", SourceDirectory: "/project",
		Text: "import ./lib.kash as lib\nresult = lib/value\n",
	}}, registry, Options{Directory: "/project", ForwardRequests: true, Watch: true})
	defer compiled.Free(a)
	p := compiled.Program
	if p == nil {
		t.Fatal("module program failed to compile")
	}
	defer p.Free()
	root := p.Start("result").Handle
	if root == nil {
		t.Fatal("module result could not start")
	}
	defer root.Free()
	loads := 0
	for range 100 {
		p.Tick(0)
		for next := p.NextOutbound(); next.OK; next = p.NextOutbound() {
			request := next.Request
			value := core.Value{}
			switch request.Kind {
			case host.RequestLoadSource:
				if request.Payload.Text != "/project/lib.kash" {
					t.Fatalf("unexpected source request %q", request.Payload.Text)
				}
				loads++
				text := "def value = 2\nexport value\n"
				if loads == 1 {
					p.InvalidateResource(core.ResourceKey{Kind: core.ResourceFile, Name: "/project/./lib.kash"})
					text = "def value = 1\nexport value\n"
				}
				value = core.NewBytes(a, []byte(text))
			case host.RequestProcessCancel:
			default:
				t.Fatalf("unexpected host request %d", request.Kind)
			}
			p.Complete(request, value, core.Diagnostic{})
			request.Free(a)
		}
		poll := root.PollRetained()
		if poll.Done {
			got, code := poll.Result.Value.Int, poll.Result.Diagnostic.Code
			poll.Result.Free(a)
			if loads != 2 || got != 2 || code != "" {
				t.Fatalf("module loaded %d times; result %d, diagnostic %q", loads, got, code)
			}
			return
		}
		poll.Result.Free(a)
	}
	t.Fatal("module result did not settle")
}
