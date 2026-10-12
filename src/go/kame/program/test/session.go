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
		{Name: "functions.km", Lang: "km", Text: "(greet WHO) = (str \"Hello \" WHO)\n"},
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
	fragments := []program.Fragment{{Name: "one", Lang: "expr", Text: "(str \"one\""}, {Name: "two", Lang: "expr", Text: "\"two\")"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session != nil || len(compiled.Diagnostics) == 0 { t.Error("fragment syntax was combined and reparsed") }
}

func TestSessionScriptDisplayIsExplicit(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	for display := 0; display < 2; display++ {
		fragments := []program.Fragment{
			{Name: "script.km", Lang: "km", Text: "(out 42)\n42"},
			{Name: "expression", Lang: "expr", Text: "42"},
			{Name: "entry.km", Lang: "km", Text: "answer = 42", Entries: []string{"answer"}},
		}
		compiled := program.CompileSession(a, fragments, registry, program.Options{PrintResult: display != 0})
		if compiled.Session == nil { compiled.Free(a); t.Fatal("display session compilation failed"); return }
		s := compiled.Session
		if len(s.Work) != 4 { t.Error("display policy changed execution work") } else {
			if s.Work[0].Display != (display != 0) || s.Work[1].Display != (display != 0) { t.Error("script display depended on out rather than explicit selection") }
			if !s.Work[2].Display || !s.Work[3].Display || !s.Work[3].Selected { t.Error("expression or explicit entry lost value display") }
		}
		s.Free()
		compiled.Free(a)
	}
}

func TestSessionEntryPoliciesStayWithTheirFragments(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	for order := 0; order < 2; order++ {
		fragments := []program.Fragment{
			{Name: "explicit.kmk", Lang: "kmk", Text: "chosen = 2\n", Entries: []string{"chosen"}},
			{Name: "implicit.kmk", Lang: "kmk", Text: "default = 1\n"},
		}
		if order == 1 { first := fragments[0]; fragments[0] = fragments[1]; fragments[1] = first }
		compiled := program.CompileSession(a, fragments, registry, program.Options{})
		if compiled.Session == nil { compiled.Free(a); t.Fatal("entry policy session compilation failed"); return }
		s := compiled.Session
		if len(s.Work) != 2 { t.Error("fragment entries were merged") } else {
			for i := range s.Work {
				implicit := len(fragments[i].Entries) == 0
				target := "chosen"
				if implicit { target = "default" }
				w := s.Work[i]
				if w.Target != target || w.ImplicitDefault != implicit || w.Selected == implicit || !w.Value || w.Raw {
					t.Error("normalization lost explicit or implicit entry policy")
				}
			}
		}
		s.Free()
		compiled.Free(a)
	}
}

func TestSessionImplicitDefaultDiagnosticSurvivesAdjacentExplicitEntry(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	for order := 0; order < 2; order++ {
		fragments := []program.Fragment{
			{Name: "explicit.kmk", Lang: "kmk", Text: "chosen :\n", Entries: []string{"chosen"}},
			{Name: "implicit.kmk", Lang: "kmk", Text: ""},
		}
		if order == 1 { first := fragments[0]; fragments[0] = fragments[1]; fragments[1] = first }
		compiled := program.CompileSession(a, fragments, registry, program.Options{})
		if compiled.Session != nil { compiled.Session.Free(); t.Error("missing implicit default was accepted") }
		if len(compiled.Diagnostics) != 1 { t.Error("missing implicit default diagnostic was lost") } else {
			d := compiled.Diagnostics[0]
			if d.Code != "TGT_NO_DEFAULT" || d.Message != "no target was requested and no default target is defined" || len(d.Notes) == 0 {
				t.Error("implicit default became an explicit target diagnostic")
			}
		}
		compiled.Free(a)
	}
}

func TestSessionTargetAssignmentsJoinOnlyWithinTheirFragment(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	for joined := 0; joined < 2; joined++ {
		entries := []string{"deploy", "region=west"}
		expected := "deploy"
		if joined == 1 { expected = "deploy region=west" } else { entries = entries[:1] }
		fragments := []program.Fragment{
			{Name: "target.kmk", Lang: "kmk", Text: "deploy {region=original} :\ndefault {region=original} :\n", Entries: entries},
			{Name: "assignment.kmk", Lang: "kmk", Text: "", Entries: []string{"region=east"}},
		}
		compiled := program.CompileSession(a, fragments, registry, program.Options{})
		if compiled.Session == nil { compiled.Free(a); t.Fatal("fragment-local target arguments failed compilation"); return }
		s := compiled.Session
		if len(s.Work) != 2 { t.Error("target and assignment fragments were merged") } else {
			if s.Work[0].Target != expected || s.Work[1].Target != "default region=east" { t.Error("assignment joined a target from another fragment") }
			for i := range s.Work {
				w := s.Work[i]
				if w.Selected || w.ImplicitDefault || w.Value || w.Raw { t.Error("rule entry policy changed during normalization") }
			}
		}
		s.Free()
		compiled.Free(a)
	}
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
