package evalcheck

import (
	"kame/core"
	"kame/internal/mem"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
)

func TestComputedPatternLiteralValuesAndValidation(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "computed", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{`@(nop """../two {evil:*} *?[\] @() words""")/{name:*}.@(nop "[a-z]+")`, `@(nop "src")/{name:*}`, `@(nop "file://root")/{name:*}`, `@(nop "C:\\root")/{name:*}`, `./@(nop 42)/{name:*}`, "./@(nop \"\x00\")/{name:*}", `@(nop "/root")/{name:*}`, `@(nop "./root")/{name:*}`, `@(nop "../root")/{name:*}`, `@(nop "./C:\\root")/{name:*}`, `./@(nop "{name:*}")/{other:*}`, `./@(nop [])/{name:*}`}
	for i := range texts {
		e := expr.Parse(a, "computed", texts[i])
		if len(e.Diagnostics) != 0 {
			t.Error("computed path did not parse")
			e.Free()
			continue
		}
		r := p.Evaluate(a, e.Expr, p.Scope)
		if i == 0 {
			if r.Diagnostic.Code != "" || r.Value.Kind != core.Pattern {
				t.Error("computed captures enumerated a glob or failed")
				r.Free(a)
				e.Free()
				continue
			}
			copy := r.Value.Clone(a)
			r.Free(a)
			pattern := expr.ParsePatternText(a, copy.Text, 0)
			if len(pattern.Diagnostics) != 0 || pattern.Pattern.Matchers != 1 || pattern.Pattern.Parts[0].Text != "../two {evil:*} *?[\\] @() words/" {
				t.Error("dynamic bytes injected capture syntax")
			}
			matched := pattern.Pattern.MatchText(a, "../two {evil:*} *?[\\] @() words/demo.[a-z]+")
			if !matched.Matched || len(matched.Captures) != 1 || matched.Captures[0] != "demo" {
				t.Error("dynamic literal bytes did not match exactly")
			}
			mem.FreeSlice(nil, matched.Captures)
			pattern.Pattern.Free(a)
			mem.FreeSlice(nil, pattern.Diagnostics)
			copy.Free(a)
		} else if i >= 6 && i <= 9 {
			if r.Diagnostic.Code != "" || r.Value.Kind != core.Pattern {
				t.Error("explicit root or literal backslash was rejected")
			}
			r.Free(a)
		} else {
			if r.Diagnostic.Code != "EXPR_INVALID" {
				t.Error("computed pattern bypassed path value validation")
			}
			r.Free(a)
		}
		e.Free()
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestPathReferencesResolveLexicalSymbols(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "paths", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{
		`(let [ROOT "./src" path "widgets/button"] @{ROOT}/js/{path}.js)`,
		`(let [path "widgets/button"] ./src/js/{path}.js)`,
		`(let [ROOT "./src" path "widgets/button"] (nop @{ROOT}/js/{path}.js))`,
		`(let [ROOT "./src" path """*{literal}"""] @{ROOT}/js/{path}.js)`,
		`(let [ROOT "./src"] @{ROOT}/js/\{path}.js)`,
		`(let [ROOT "./src"] "@{ROOT}/js/{path}.js")`,
		`(let [ROOT "./src"] @{ROOT}/js/{missing}.js)`,
		`(let [ROOT "./src" path 42] @{ROOT}/js/{path}.js)`,
		"(let [ROOT \"./src\" path \"\x00\"] @{ROOT}/js/{path}.js)",
	}
	for i := range texts {
		e := expr.Parse(a, "paths", texts[i])
		if len(e.Diagnostics) != 0 {
			t.Error("path reference did not parse")
			e.Free()
			continue
		}
		r := p.Evaluate(a, e.Expr, p.Scope)
		if i < 6 {
			want := "./src/js/widgets/button.js"
			if i == 3 {
				want = "./src/js/*{literal}.js"
			}
			if i == 4 || i == 5 {
				want = "./src/js/{path}.js"
			}
			if r.Diagnostic.Code != "" || r.Value.Kind != core.String || r.Value.Text != want {
				t.Errorf("path reference case %d failed: %s", i, r.Diagnostic.Code)
			}
		} else {
			want := "EXPR_INVALID"
			if i == 6 {
				want = "REF_MISSING"
			}
			if r.Diagnostic.Code != want {
				t.Errorf("path reference case %d did not report %s", i, want)
			}
		}
		r.Free(a)
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
		if !request.OK || state.Calls != generation || state.Effects != generation-1 {
			t.Error("computed operand did not complete before suspension")
			break
		}
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
		request.Request.Free(a)
		stepUntilCurrent(engine, node)
		want := "./1/{name:*}.42"
		if generation == 2 {
			want = "./2/{name:*}.42"
		}
		if !node.Current || node.Latest.Kind != core.Pattern || node.Latest.Text != want || state.Calls != generation || state.Effects != generation {
			t.Error("computed children/effects replayed or canonical text lost")
		}
		if generation == 1 {
			engine.Invalidate(node)
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestPathReferencesRetainChildrenAcrossSuspension(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "paths", "part = (str (async))\nresult = ./@(str (once))/{part}")
	p := eval.Compile(a, engine, parsed, registry)
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	request := stepUntilRequest(engine, p)
	if !request.OK || state.Calls != 1 {
		t.Error("path reference did not suspend after the first operand")
	} else {
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
	}
	if request.OK {
		request.Request.Free(a)
	}
	stepUntilCurrent(engine, node)
	if !node.Current || node.Latest.Kind != core.String || node.Latest.Text != "./1/42" || state.Calls != 1 || state.Effects != 1 {
		t.Error("path reference replayed operands or lost its resumed value")
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
