package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestInterpolatedPathsValidateTextAndRetainGlobProvenance(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "paths.km", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{`./@(nop "a *?[\\]")/file.txt`, `@(nop "../two words")/file`, `./@(:nil)/file`, `./@(42)/file`, `./@([])/file`, `./@([a: 1])/file`, `@(nop "src")/file`, `./@(:true)/file`, `./@(([x] x))/file`, "./@(nop \"\x00\")/file"}
	for i := range texts {
		e := expr.Parse(a, "path.km", texts[i])
		if len(e.Diagnostics) != 0 { t.Error("path did not parse") }
		r := p.Evaluate(a, e.Expr, p.Scope)
		if i == 0 {
			if r.Diagnostic.Code != "" || r.Value.Text != "./a *?[\\]/file.txt" || r.Value.GlobPattern != "./a \\*\\?\\[\\\\]/file.txt" { t.Error("dynamic glob bytes lost literal provenance") }
			copy := r.Value.Clone(a)
			if copy.GlobPattern != r.Value.GlobPattern || core.ValueSignature(copy).Equal(core.ValueSignature(core.Value{Kind: core.String, Text: copy.Text})) { t.Error("path clone or signature lost glob provenance") }
			copy.Free(a)
		} else if i == 1 {
			if r.Diagnostic.Code != "" || r.Value.Text != "../two words/file" { t.Error("explicit path root with spaces failed") }
		} else if r.Diagnostic.Code != "EXPR_INVALID" { t.Error("nontext or implicit-root path interpolation was accepted") }
		r.Free(a)
		e.Free()
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestPathInterpolationRetainsCompletedChildrenAcrossSuspension(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "paths.km", "result = ./@(str (once))/@(str (async))")
	p := eval.Compile(a, engine, parsed, registry)
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	for generation := 1; generation <= 2; generation++ {
		request := stepUntilRequest(engine, p)
		if !request.OK || state.Calls != generation || state.Effects != generation-1 { t.Fatal("path child did not complete before suspension"); return }
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
		request.Request.Free(a)
		stepUntilCurrent(engine, node)
		want := "./1/42"
		if generation == 2 { want = "./2/42" }
		if !node.Current || node.Latest.Text != want || state.Calls != generation || state.Effects != generation { t.Error("path children or effects were replayed") }
		if generation == 1 { engine.Invalidate(node) }
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
