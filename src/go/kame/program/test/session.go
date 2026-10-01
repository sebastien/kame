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
