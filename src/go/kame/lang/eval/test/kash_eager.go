package eval_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/testing"
)

func TestKashEagerBindingsAreReachedOnceAndFreshInEachCall(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := operationState{}
	registry.Add(eval.Operation{Name: "tick", Call: countOperation, Context: &state, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKash(a, "fresh.kash", "def probe\n\tlet unused = (tick)\n\tlet value = (tick)\n\tlet call = ([] value)\n\treturn (call) ?\nresult = (map ([item] (probe)) [1 2 3])\n")
	p := eval.Compile(a, engine, parsed, registry)
	node := p.Definition("result")
	engine.Request(node)
	stepUntilCurrent(engine, node)
	if !node.Current || node.Diagnostic.Code != "" || len(node.Latest.List) != 3 || state.Calls != 6 {
		t.Error("eager acquisition was skipped, repeated, or reused across calls: " + node.Diagnostic.Code)
	} else {
		for i := range node.Latest.List {
			if node.Latest.List[i].Record[0].Value.Int != int64((i+1)*2) {
				t.Error("fresh eager slot retained another call's value")
			}
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestKashEagerBindingRetainsInitializerAcrossSuspension(t *testing.T) {
	checkKashEagerSuspension(t, "def probe\n\tlet values = (list (once) (async))\n\treturn (count values) ?\nresult = (probe)\n", 1)
	checkKashEagerSuspension(t, "def probe\n\tunused = (once)\n\t(helper) = 42\n\tlet values = (list (once) (async))\n\treturn (count values) ?\nresult = (probe)\n", 1)
	checkKashEagerSuspension(t, "def probe callback\n\tlet values = (list (once) (async))\n\treturn (count values) ?\nresult = (probe ([] 42))\n", 1)
	checkKashEagerSuspension(t, "def probe\n\tlet values = (list ([] 42) (once) (async))\n\treturn 2 ?\nresult = (probe)\n", 1)
	checkKashEagerSuspension(t, "def probe\n\t(def unrelated 1)\n\tlet values = (list (once) (async))\n\treturn 2 ?\nresult = (probe)\n", 1)
	checkKashEagerSuspension(t, "def probe\n\tfor item in [1 2]\n\t\tif (eq? item item)\n\t\t\tunused = (once)\n\t\t\t(helper) = item\n\t\t\tlet values = (list (once) (async))\n\treturn 2 ?\nresult = (probe)\n", 2)
}

func checkKashEagerSuspension(t *testing.T, text string, expected int) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKash(a, "suspended.kash", text)
	if len(parsed.Diagnostics) != 0 {
		t.Fatal("invalid suspension fixture")
		return
	}
	p := eval.Compile(a, engine, parsed, registry)
	p.DefinitionEffectSink, p.DefinitionEffectState = memoEffectSink, &state
	node := p.Definition("result")
	engine.Request(node)
	for i := 0; i < 64 && !node.Current; i++ {
		request := stepUntilRequest(engine, p)
		if !request.OK {
			break
		}
		value := core.Value{}
		if request.Request.Kind == host.RequestMonotonicTime {
			value = core.Value{Kind: core.Int}
		}
		engine.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, Value: value})
		request.Request.Free(a)
	}
	stepUntilCurrent(engine, node)
	if !node.Current || node.Diagnostic.Code != "" || len(node.Latest.Record) == 0 || node.Latest.Record[0].Value.Int != 2 || state.Calls != expected || state.Effects != expected {
		t.Errorf("eager initializer replayed completed work or lost its output: current=%t calls=%d effects=%d diagnostic=%s", node.Current, state.Calls, state.Effects, node.Diagnostic.Code)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestKashEagerUninitializedReadsAreProtectedThroughRecoveryAndLazyProxies(t *testing.T) {
	bodies := []string{
		"\tlet use = value\n\tlet value = 1\n",
		"\tlet value = value\n",
		"\tlet use = value ?? (tick)\n\tlet value = 1\n",
		"\tlet use = (? value (tick))\n\tlet value = 1\n",
		"\tproxy = value\n\tlet use = proxy ?? (tick)\n\tlet value = 1\n",
		"\t(helper) = value\n\tif (helper)\n\t\t(tick)\n\tlet value = 1\n",
	}
	for i := range bodies {
		a := t.Allocator()
		engine := core.NewEngine(a)
		registry := eval.NewRegistry(a)
		operations.Register(registry)
		state := operationState{}
		registry.Add(eval.Operation{Name: "tick", Call: countOperation, Context: &state, MinArity: 0, MaxArity: 0})
		parsed := script.ParseKash(a, "uninitialized.kash", "value = 42\ndef probe\n"+bodies[i]+"result = (probe)\n")
		p := eval.Compile(a, engine, parsed, registry)
		node := p.Definition("result")
		engine.Request(node)
		stepUntilCurrent(engine, node)
		if node.Diagnostic.Code != "BINDING_UNINITIALIZED" || !node.Diagnostic.Protected || state.Calls != 0 {
			t.Error("uninitialized read fell back, leaked an outer binding, or became false: " + node.Diagnostic.Code)
		}
		engine.Free()
		p.Free()
		parsed.Free()
		registry.Free()
	}
}

func TestKashEagerCancelledInitializerReleasesCallableOperands(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	state := memoCountState{}
	registry.Add(eval.Operation{Name: "once", Call: memoCountOperation, Context: &state, MinArity: 0, MaxArity: 0})
	registry.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
	parsed := script.ParseKash(a, "cancelled.kash", "def probe\n\tlet values = (list ([] 42) (once) (async))\nresult = (probe)\n")
	p := eval.Compile(a, engine, parsed, registry)
	node := p.Definition("result")
	engine.Request(node)
	request := stepUntilRequest(engine, p)
	if !request.OK || state.Calls != 1 {
		t.Error("initializer did not suspend after callable and effect")
	}
	if request.OK {
		request.Request.Free(a)
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}
