package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

func TestComputedPatternLiteralValuesAndValidation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "computed", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{`@(nop """../two {evil:*} *?[\] @() words""")/{name:*}.@(nop "[a-z]+")`, `@(nop "src")/{name:*}`, `@(nop "file://root")/{name:*}`, `@(nop "C:\\root")/{name:*}`, `./@(42)/{name:*}`, "./@(nop \"\x00\")/{name:*}", `@(nop "/root")/{name:*}`, `@(nop "./root")/{name:*}`, `@(nop "../root")/{name:*}`, `@(nop "./C:\\root")/{name:*}`, `./@("{name:*}")/{other:*}`, `./@([])/{name:*}`}
	for i := range texts {
		e := expr.Parse(a, "computed", texts[i])
		if len(e.Diagnostics) != 0 { t.Error("computed path did not parse"); e.Free(); continue }
		r := p.Evaluate(a, e.Expr, p.Scope)
		if i == 0 {
			if r.Diagnostic.Code != "" || r.Value.Kind != core.Pattern { t.Error("computed captures enumerated a glob or failed"); r.Free(a); e.Free(); continue }
			copy := r.Value.Clone(a)
			r.Free(a)
			pattern := expr.ParsePatternText(a, copy.Text, 0)
			if len(pattern.Diagnostics) != 0 || pattern.Pattern.Matchers != 1 || pattern.Pattern.Parts[0].Text != "../two {evil:*} *?[\\] @() words/" { t.Error("dynamic bytes injected capture syntax") }
			matched := pattern.Pattern.MatchText(a, "../two {evil:*} *?[\\] @() words/demo.[a-z]+")
			if !matched.Matched || len(matched.Captures) != 1 || matched.Captures[0] != "demo" { t.Error("dynamic literal bytes did not match exactly") }
			slices.Free(a, matched.Captures)
			pattern.Pattern.Free(a)
			slices.Free(a, pattern.Diagnostics)
			copy.Free(a)
		} else if i >= 6 && i <= 9 {
			if r.Diagnostic.Code != "" || r.Value.Kind != core.Pattern { t.Error("explicit root or literal backslash was rejected") }
			r.Free(a)
		} else {
			if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("computed pattern bypassed path value validation") }
			r.Free(a)
		}
		e.Free()
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestComputedPatternRetainsChildrenAcrossSuspension(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "computed", "result = ./@(str (once))/{name:*}.@(str (async))")
	p := eval.Compile(a, engine, parsed, registry)
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	for generation := 1; generation <= 2; generation++ {
		request := stepUntilRequest(engine, p)
		if !request.OK || state.Calls != generation || state.Effects != generation-1 { t.Error("computed operand did not complete before suspension"); break }
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
		request.Request.Free(a)
		stepUntilCurrent(engine, node)
		want := "./1/{name:*}.42"
		if generation == 2 { want = "./2/{name:*}.42" }
		if !node.Current || node.Latest.Kind != core.Pattern || node.Latest.Text != want || state.Calls != generation || state.Effects != generation { t.Error("computed children/effects replayed or canonical text lost") }
		if generation == 1 { engine.Invalidate(node) }
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
