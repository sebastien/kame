package programcheck

import (
	"kame/core"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/operations"
	"kame/program"
)

func TestCompileManyHonorsSourceLanguagesWithoutKash(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	languages := []string{"km", ""}
	for i := range languages {
		for extra := 0; extra < 2; extra++ {
			sources := make([]program.CompileSource, 1+extra)
			sources[0] = program.CompileSource{Name: "authored", Lang: languages[i], Text: "task target\n", Offset: 20}
			if extra != 0 {
				sources[1] = program.CompileSource{Name: "unrelated.kash", Lang: "kash", Text: "def unused = 42\n"}
			}
			compiled := program.CompileMany(a, sources, registry, program.Options{})
			if compiled.Program == nil || len(compiled.Program.Rules) != 1 || len(compiled.Diagnostics) != 0 {
				t.Error("Kame rule grammar changed for the combined sources")
			}
			if compiled.Program != nil {
				compiled.Program.Free()
			}
			compiled.Free(a)
			sources[0].Text = "target :\n"
			rejected := program.CompileMany(a, sources, registry, program.Options{})
			if rejected.Program != nil || len(rejected.Diagnostics) == 0 || rejected.Diagnostics[0].Source != "authored" ||
				rejected.Diagnostics[0].Span.Start != 27 {
				t.Error("obsolete rule syntax was accepted or lost its authored offset")
			}
			if rejected.Program != nil {
				rejected.Program.Free()
			}
			rejected.Free(a)
			mem.FreeSlice(nil, sources)
		}
	}
}

func TestCompileManyBoundsExplicitLanguageFragments(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	for mode := 0; mode < 2; mode++ {
		lang := ""
		if mode != 0 {
			lang = "km"
		}
		sources := []program.CompileSource{
			{Name: "first", Lang: lang, Text: "value = (str \"one\""},
			{Name: "second", Lang: lang, Text: "\"two\")"},
		}
		compiled := program.CompileMany(a, sources, registry, program.Options{})
		if compiled.Program != nil || len(compiled.Diagnostics) == 0 {
			t.Error("fragment syntax crossed source boundaries")
		}
		if compiled.Program != nil {
			compiled.Program.Free()
		}
		compiled.Free(a)
	}
}

func TestSessionSharesLazyDefinitionsAndKeepsParserBoundaries(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	fragments := []program.Fragment{
		{Name: "first.km", Lang: "km", Inline: true, Text: "(greet name)\n"},
		{Name: "functions.km", Lang: "km", Text: "def greet WHO = (str \"Hello \" WHO)\n"},
		{Name: "values.km", Lang: "km", Text: "name = \"Ada\"\nunused = $(printf unused)\n"},
		{Name: "typed.kash", Lang: "kash", Text: "def number = 42\n"},
		{Name: "last", Lang: "expr", Text: "number"},
	}
	compiled := program.CompileSession(a, fragments, registry, program.Options{Directory: "."})
	defer compiled.Free(a)
	if compiled.Session == nil || len(compiled.Diagnostics) != 0 {
		t.Fatal("session compilation failed")
		return
	}
	s := compiled.Session
	defer s.Free()
	if len(s.Work) != 2 {
		t.Fatal("lazy declarations became eager work")
		return
	}
	for i := range s.Work {
		start := s.Start(i)
		if start.Handle == nil {
			t.Fatal("statement scheduling failed")
			return
		}
		h := start.Handle
		for j := 0; j < 100 && !h.Node.Current; j++ {
			s.Program.Tick(0)
		}
		if !h.Node.Current {
			h.Free()
			t.Fatal("statement failed to publish")
			return
		}
		if i == 0 && h.Node.Latest.Text != "Hello Ada" {
			t.Error("forward definitions or shared function scope lost")
		}
		if i == 1 && (h.Node.Latest.Kind != core.Int || h.Node.Latest.Int != 42) {
			t.Error("Kash definition was classified as build text")
		}
		h.Free()
	}
	if s.Program.Eval.Requests.Next().OK {
		t.Error("unused process definition submitted host work")
	}
}

func TestSessionDiagnosticsOwnAuthoredSourcesOnFailure(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	fragments := []program.Fragment{{Name: "one.km", Lang: "km", Text: "name = 1\n"}, {Name: "two.km", Lang: "km", Text: "name = 2\n"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session != nil || len(compiled.Diagnostics) != 1 {
		t.Error("duplicate declarations were not preflighted")
		return
	}
	d := compiled.Diagnostics[0]
	if d.Code != "DEF_INVALID" || d.Source != "two.km" || d.Span.Start != 0 || d.Span.End != 4 || !d.Owned {
		t.Error("failed compilation borrowed freed source storage or lost authored spans")
	}
}

func TestSessionCannotCompleteSyntaxAcrossFragments(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	fragments := []program.Fragment{{Name: "one", Lang: "expr", Text: "(str \"one\""}, {Name: "two", Lang: "expr", Text: "\"two\")"}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session != nil || len(compiled.Diagnostics) == 0 {
		t.Error("fragment syntax was combined and reparsed")
	}
}

func TestSessionSourceDirectoryUsesAuthoredTreesNotLabelsOrGeneratedSpans(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	fragments := []program.Fragment{
		{Name: "display-label", SourceDirectory: "/sdk/library", Lang: "km", Text: "def assets () = (source-dir ())\n"},
		{Name: "caller", SourceDirectory: "/project", Lang: "expr", Text: "(assets ())"},
		{Name: "caller", SourceDirectory: "/project", Lang: "expr", Text: "(eval \"(assets ())\")"},
		{Name: "caller", SourceDirectory: "/project", Lang: "expr", Text: "(eval \"(source-dir ())\")"},
		{Name: "caller", SourceDirectory: "/project", Lang: "expr", Text: "((eval \"(() => (source-dir ()))\") ())"},
		{Name: "/pretend/file.kash", Lang: "expr", Text: "(source-dir ())"},
	}
	compiled := program.CompileSession(a, fragments, registry, program.Options{Directory: "/caller"})
	defer compiled.Free(a)
	if compiled.Session == nil {
		t.Fatal("source-directory session compilation failed")
		return
	}
	s := compiled.Session
	defer s.Free()
	for i := range s.Work {
		start := s.Start(i)
		if start.Handle == nil {
			t.Fatal("source-directory work was not scheduled")
			return
		}
		h := start.Handle
		for j := 0; j < 100 && !h.Node.Current && h.Node.Diagnostic.Code == ""; j++ {
			s.Program.Tick(0)
		}
		if i < 2 {
			if !h.Node.Current || h.Node.Latest.Text != "/sdk/library" {
				t.Error("function borrowed caller cwd/source identity: " + h.Node.Diagnostic.Code)
			}
		} else if h.Node.Diagnostic.Code != "SEL_NO_CONTEXT" {
			t.Error("generated source or display label acquired a file loading base")
		}
		h.Free()
	}
}

func TestSessionScriptDisplayIsExplicit(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	for display := 0; display < 2; display++ {
		fragments := []program.Fragment{
			{Name: "script.km", Lang: "km", Inline: true, Text: "(out! 42)\n42"},
			{Name: "expression", Lang: "expr", Text: "42"},
			{Name: "entry.km", Lang: "km", Text: "answer = 42", Entries: []string{"answer"}},
		}
		compiled := program.CompileSession(a, fragments, registry, program.Options{PrintResult: display != 0})
		if compiled.Session == nil {
			compiled.Free(a)
			t.Fatal("display session compilation failed")
			return
		}
		s := compiled.Session
		if len(s.Work) != 4 {
			t.Error("display policy changed execution work")
		} else {
			if s.Work[0].Display != (display != 0) || s.Work[1].Display != (display != 0) {
				t.Error("script display depended on out rather than explicit selection")
			}
			if !s.Work[2].Display || !s.Work[3].Display || !s.Work[3].Selected {
				t.Error("expression or explicit entry lost value display")
			}
		}
		s.Free()
		compiled.Free(a)
	}
}

func TestSessionExplicitEntriesSuppressTheCombinedDefault(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	for order := 0; order < 2; order++ {
		fragments := []program.Fragment{
			{Name: "explicit.km", Lang: "km", Text: "chosen = 2\n", Entries: []string{"chosen"}},
			{Name: "implicit.km", Lang: "km", Text: "default = 1\n"},
			{Name: "library.kash", Lang: "kash", Text: "def library = 3\n"},
		}
		if order == 1 {
			fragments[0], fragments[1] = fragments[1], fragments[0]
		}
		compiled := program.CompileSession(a, fragments, registry, program.Options{})
		if compiled.Session == nil {
			compiled.Free(a)
			t.Fatal("entry policy session compilation failed")
			return
		}
		s := compiled.Session
		if len(s.Work) != 1 || s.Work[0].Target != "chosen" || s.Work[0].ImplicitDefault ||
			!s.Work[0].Selected || !s.Work[0].Value || s.Work[0].Raw {
			t.Error("explicit entry policy changed across fragments")
		}
		s.Free()
		compiled.Free(a)
		for i := range fragments {
			fragments[i].Entries = nil
		}
		compiled = program.CompileSession(a, fragments, registry, program.Options{})
		s = compiled.Session
		if s == nil {
			compiled.Free(a)
			t.Fatal("mixed declaration-only session failed to compile")
			return
		}
		if len(s.Work) != 1 || s.Work[0].Target != "default" || !s.Work[0].ImplicitDefault || !s.Work[0].Value {
			t.Error("mixed session did not schedule its combined default once")
		}
		s.Free()
		compiled.Free(a)
	}
}

func TestSessionReportsMissingCombinedDefault(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	fragments := []program.Fragment{
		{Name: "rules.km", Lang: "km", Text: "task chosen\n"},
		{Name: "empty.km", Lang: "km"},
	}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session != nil {
		compiled.Session.Free()
		t.Error("missing implicit default was accepted")
	}
	if len(compiled.Diagnostics) != 1 {
		t.Error("missing implicit default diagnostic was lost")
		return
	}
	d := compiled.Diagnostics[0]
	if d.Code != "TGT_NO_DEFAULT" || d.Message != "no target was requested and no default target is defined" || len(d.Notes) == 0 {
		t.Error("implicit default became an explicit target diagnostic")
	}
}

func TestSessionTargetAssignmentsJoinOnlyWithinTheirFragment(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	for joined := 0; joined < 2; joined++ {
		entries := []string{"deploy", "region=west"}
		expected := "deploy"
		if joined == 1 {
			expected = "deploy region=west"
		} else {
			entries = entries[:1]
		}
		fragments := []program.Fragment{
			{Name: "target.km", Lang: "km", Text: "task deploy {region=original}\ntask default {region=original}\n", Entries: entries},
			{Name: "assignment.km", Lang: "km", Text: "", Entries: []string{"region=east"}},
		}
		compiled := program.CompileSession(a, fragments, registry, program.Options{})
		if compiled.Session == nil {
			compiled.Free(a)
			t.Fatal("fragment-local target arguments failed compilation")
			return
		}
		s := compiled.Session
		if len(s.Work) != 2 {
			t.Error("target and assignment fragments were merged")
		} else {
			if s.Work[0].Target != expected || s.Work[1].Target != "default region=east" {
				t.Error("assignment joined a target from another fragment")
			}
			for i := range s.Work {
				w := s.Work[i]
				if w.Selected || w.ImplicitDefault || w.Value || w.Raw {
					t.Error("rule entry policy changed during normalization")
				}
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
	fragments := []program.Fragment{{Name: "dry.km", Lang: "km", Inline: true, Text: "capture = $(printf ignored)\nlegacy = (sh! \"printf ignored\")\n(out! \"hidden\")\n(write! \"protected\" \"hidden\")\n(count [capture, legacy])\n"}}
	grants := []eval.Grant{{Capability: eval.Run}, {Capability: eval.Write}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{DryRun: true, Grants: grants})
	defer compiled.Free(a)
	if compiled.Session == nil {
		t.Fatal("dry-run compilation failed")
		return
	}
	s := compiled.Session
	defer s.Free()
	for i := range s.Work {
		start := s.Start(i)
		if start.Handle == nil {
			t.Fatal("dry-run scheduling failed")
			return
		}
		h := start.Handle
		for j := 0; j < 100 && !h.Node.Current; j++ {
			s.Program.Tick(0)
		}
		if !h.Node.Current {
			h.Free()
			t.Fatal("dry-run submitted an effect instead of publishing")
			return
		}
		if i+1 == len(s.Work) && (h.Node.Latest.Kind != core.Int || h.Node.Latest.Int != 2) {
			t.Error("dry-run skipped pure evaluation")
		}
		h.Free()
	}
	if s.Program.Eval.Requests.Next().OK || len(s.Program.Pending) != 0 {
		t.Error("dry-run issued process/write requests")
	}
	if s.Program.NextEvent().OK {
		t.Error("dry-run emitted a live output effect")
	}
}

func TestTemplateSessionPreservesPayloadAndAuthoredDiagnostics(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	fragments := []program.Fragment{{Name: "page.md", Lang: "template", Text: "Hello @{name}!", Defines: []string{"name=old", "name=\"new\""}}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session == nil {
		t.Fatal("template compilation failed")
		return
	}
	s := compiled.Session
	defer s.Free()
	start := s.Start(0)
	if start.Handle == nil {
		t.Fatal("template did not schedule")
		return
	}
	h := start.Handle
	for j := 0; j < 100 && !h.Node.Current; j++ {
		s.Program.Tick(0)
	}
	if !h.Node.Current || string(h.Node.Latest.Bytes) != "Hello \"new\"!" {
		t.Error("template payload changed type or escaping")
	}
	h.Free()
	bad := []program.Fragment{{Name: "bad.md", Lang: "template", Text: "<!-- @if(true) -->\nmissing end", Check: true}}
	rejected := program.CompileSession(a, bad, registry, program.Options{})
	defer rejected.Free(a)
	if rejected.Session != nil {
		rejected.Session.Free()
		t.Error("parse-only mode accepted an unclosed block")
		return
	}
	if len(rejected.Diagnostics) == 0 {
		t.Fatal("missing template diagnostic")
		return
	}
	d := rejected.Diagnostics[0]
	if d.Code != "TPL_BLOCK" || d.Source != "bad.md" || d.Span.Start != 0 {
		t.Error("template diagnostic lost authored source")
	}
}

func TestTemplateCheckNeverEvaluates(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	fragments := []program.Fragment{{Name: "<stdin>", Lang: "template", Text: "@(read ./secret)", Inline: true, Check: true}}
	compiled := program.CompileSession(a, fragments, registry, program.Options{})
	defer compiled.Free(a)
	if compiled.Session == nil {
		t.Fatal("check evaluated a denied read")
		return
	}
	s := compiled.Session
	defer s.Free()
	if len(s.Work) != 0 || s.Program.Eval.Requests.Next().OK {
		t.Error("check scheduled template effects")
	}
}
