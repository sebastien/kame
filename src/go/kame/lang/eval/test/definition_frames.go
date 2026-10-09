package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestScopedDefinitionsOwnDistinctRuleFrames(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "frames.km", "(newer UNUSED) = (if :true @<? @<?)\nSELECTED = (newer :nil)\nFORWARD = (if :true SELECTED :nil)\n")
	p := eval.Compile(a, engine, parsed, registry)
	outer, first, second := core.NewString(a, "outer"), core.NewString(a, "first"), core.NewString(a, "second")
	frames := []eval.RuleFrame{{FileRule: true, NewerInputs: []core.Value{outer}}, {FileRule: true, NewerInputs: []core.Value{first}}}
	context := eval.Context{Program: p, Scope: p.Scope, Run: a, HasDefinitionNamespace: true, Phase: eval.RenderingPhase, RuleFrames: frames}
	left := p.DefinitionWith("FORWARD", &context)
	frames[1].NewerInputs[0] = second
	right := p.DefinitionWith("FORWARD", &context)
	frames[1].FileRule = false
	nonfile := p.DefinitionWith("FORWARD", &context)
	outer.Free(a)
	first.Free(a)
	second.Free(a)
	if left == right || right == nonfile {
		t.Error("scoped definitions shared different rule snapshots")
	}
	engine.Request(left)
	engine.Request(right)
	for i := 0; i < 12; i++ { engine.Step() }
	if !left.Current || left.Latest.Kind != core.List || len(left.Latest.List) != 1 || left.Latest.List[0].Text != "first" {
		t.Error("nested definition lost its owned nearest rule snapshot: " + left.Diagnostic.Code + " " + left.Diagnostic.Message)
	}
	if !right.Current || right.Latest.Kind != core.List || len(right.Latest.List) != 1 || right.Latest.List[0].Text != "second" {
		t.Error("different rule reused another selector result: " + right.Diagnostic.Code + " " + right.Diagnostic.Message)
	}
	engine.Request(nonfile)
	for i := 0; i < 12; i++ { engine.Step() }
	if nonfile.Diagnostic.Code != "SEL_NO_CONTEXT" {
		t.Error("nonfile definition inherited an outer file frame")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestScopedDefinitionsRejectUnavailableRuleFrameIdentity(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "opaque-frame.km", "X = (if :true @< :nil)\n")
	p := eval.Compile(a, engine, parsed, registry)
	first, second := mem.Alloc[int](a), mem.Alloc[int](a)
	frames := []eval.RuleFrame{{FileRule: true, Inputs: []core.Value{{Kind: core.Process, Process: first}}}}
	context := eval.Context{Program: p, Scope: p.Scope, Run: a, HasDefinitionNamespace: true, RuleFrames: frames}
	left := p.DefinitionWith("X", &context)
	frames[0].Inputs[0].Process = second
	right := p.DefinitionWith("X", &context)
	mem.Free(a, first)
	mem.Free(a, second)
	engine.Request(left)
	engine.Request(right)
	for i := 0; i < 12; i++ { engine.Step() }
	if left.Diagnostic.Code != "EXPR_INVALID" || left.Diagnostic.Message != "rule frame values require stable content signatures" || right.Diagnostic.Code != "EXPR_INVALID" {
		t.Error("unavailable frame identity was silently cached")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
