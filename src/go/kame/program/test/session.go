package program_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

func TestSessionSharesLazyDefinitionsAndKeepsParserBoundaries(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	fragments := []program.Fragment{
		{Name: "first.km", Lang: "km", Text: "(greet name)\n"},
		{Name: "functions.km", Lang: "km", Text: "(greet WHO) = (cat \"Hello \" WHO)\n"},
		{Name: "values.km", Lang: "km", Text: "name = \"Ada\"\nunused = $(printf unused)\n"},
		{Name: "typed.kash", Lang: "kash", Text: "number = 42\n"},
		{Name: "last", Lang: "expr", Text: "number"},
	}
	compiled := program.CompileSession(a, fragments, registry, program.Options{Directory: "."})
	defer compiled.Free(a)
	if compiled.Session == nil || len(compiled.Diagnostics) != 0 { t.Fatal("session compilation failed"); return }
	s := compiled.Session
	defer s.Free()
	if len(s.Work) != 2 { t.Fatal("lazy declarations became eager work"); return }
	for i := range s.Work {
		start := s.Start(i)
		if start.Handle == nil { t.Fatal("statement scheduling failed"); return }
		h := start.Handle
		for j := 0; j < 100 && !h.Node.Current; j++ { s.Program.Tick(0) }
		if !h.Node.Current { h.Free(); t.Fatal("statement failed to publish"); return }
		if i == 0 && h.Node.Latest.Text != "Hello Ada" { t.Error("forward definitions or shared function scope lost") }
		if i == 1 && (h.Node.Latest.Kind != core.Int || h.Node.Latest.Int != 42) { t.Error("Kash definition was classified as build text") }
		h.Free()
	}
	if s.Program.Eval.Requests.Next().OK { t.Error("unused process definition submitted host work") }
}

func TestSessionDiagnosticsOwnAuthoredSourcesOnFailure(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	fragments := []program.Fragment{{Name: "one.km", Lang: "km", Text: "name = 1\n"}, {Name: "two.km", Lang: "km", Text: "name = 2\n"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session != nil || len(compiled.Diagnostics) != 1 { t.Error("duplicate declarations were not preflighted"); return }
	d := compiled.Diagnostics[0]
	if d.Code != "DEF_INVALID" || d.Source != "two.km" || d.Span.Start != 0 || d.Span.End != 4 || !d.Owned { t.Error("failed compilation borrowed freed source storage or lost authored spans") }
}

func TestSessionCannotCompleteSyntaxAcrossFragments(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	fragments := []program.Fragment{{Name: "one", Lang: "expr", Text: "(cat \"one\""}, {Name: "two", Lang: "expr", Text: "\"two\")"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session != nil || len(compiled.Diagnostics) == 0 { t.Error("fragment syntax was combined and reparsed") }
}

func TestSessionDryRunEvaluatesWithoutEffectRequests(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	fragments := []program.Fragment{{Name: "dry.km", Lang: "km", Text: "capture = $(printf ignored)\nlegacy = (shell \"printf ignored\")\n(out \"hidden\")\n(write \"protected\" \"hidden\")\n(count [capture legacy])\n"}}
	grants := []eval.Grant{{Capability: eval.Run}, {Capability: eval.Write}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{DryRun: true, Grants: grants})
	defer compiled.Free(a)
	if compiled.Session == nil { t.Fatal("dry-run compilation failed"); return }
	s := compiled.Session
	defer s.Free()
	for i := range s.Work {
		start := s.Start(i)
		if start.Handle == nil { t.Fatal("dry-run scheduling failed"); return }
		h := start.Handle
		for j := 0; j < 100 && !h.Node.Current; j++ { s.Program.Tick(0) }
		if !h.Node.Current { h.Free(); t.Fatal("dry-run submitted an effect instead of publishing"); return }
		if i+1 == len(s.Work) && (h.Node.Latest.Kind != core.Int || h.Node.Latest.Int != 2) { t.Error("dry-run skipped pure evaluation") }
		h.Free()
	}
	if s.Program.Eval.Requests.Next().OK || len(s.Program.Pending) != 0 { t.Error("dry-run issued process/write requests") }
	if s.Program.NextEvent().OK { t.Error("dry-run emitted a live output effect") }
}

func TestTemplateSessionPreservesPayloadAndAuthoredDiagnostics(t *testing.T) {
 a := t.Allocator()
 registry := eval.NewRegistry(a)
 defer registry.Free()
 operations.Register(registry)
 fragments := []program.Fragment{{Name: "page.md", Lang: "template", Text: "Hello @(name)!", Defines: []string{"name=old", "name=\"new\""}}}
 compiled := program.CompileSession(a, fragments, registry, program.Options{})
 defer compiled.Free(a)
 if compiled.Session == nil { t.Fatal("template compilation failed"); return }
 s := compiled.Session
 defer s.Free()
 start := s.Start(0)
 if start.Handle == nil { t.Fatal("template did not schedule"); return }
 h := start.Handle
 for j := 0; j < 100 && !h.Node.Current; j++ { s.Program.Tick(0) }
 if !h.Node.Current || string(h.Node.Latest.Bytes) != "Hello \"new\"!" { t.Error("template payload changed type or escaping") }
 h.Free()
 bad := []program.Fragment{{Name: "bad.md", Lang: "template", Text: "<!-- @if(:true) -->\nmissing end", Check: true}}
 rejected := program.CompileSession(a, bad, registry, program.Options{})
 defer rejected.Free(a)
 if rejected.Session != nil { rejected.Session.Free(); t.Error("parse-only mode accepted an unclosed block"); return }
 if len(rejected.Diagnostics) == 0 { t.Fatal("missing template diagnostic"); return }
 d := rejected.Diagnostics[0]
 if d.Code != "TPL_BLOCK" || d.Source != "bad.md" || d.Span.Start != 0 { t.Error("template diagnostic lost authored source") }
}

func TestTemplateCheckNeverEvaluates(t *testing.T) {
 a := t.Allocator()
 registry := eval.NewRegistry(a)
 defer registry.Free()
 operations.Register(registry)
 fragments := []program.Fragment{{Name: "<stdin>", Lang: "template", Text: "@(read ./secret)", Inline: true, Check: true}}
 compiled := program.CompileSession(a, fragments, registry, program.Options{})
 defer compiled.Free(a)
 if compiled.Session == nil { t.Fatal("check evaluated a denied read"); return }
 s := compiled.Session
 defer s.Free()
 if len(s.Work) != 0 || s.Program.Eval.Requests.Next().OK { t.Error("check scheduled template effects") }
}
