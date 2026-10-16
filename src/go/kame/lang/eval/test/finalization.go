package eval_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type finalizationHost struct {
	Program *eval.Program
	CancelOutput bool
	Alloc mem.Allocator
	Now int64
	Quiet bool
	Output []byte
}

func finalizationClock(value any) int64 { h := value.(*finalizationHost); return h.Now }
func finalizationQuiescent(value any, node int64, generation int64) bool {
	_, _ = node, generation
	h := value.(*finalizationHost)
	return h.Quiet
}
func finalizationOutput(value any, effect eval.Effect) {
	h := value.(*finalizationHost)
	if h.CancelOutput && effect.FinalizationDeadlineNS != 0 { h.Program.FinalizerOutputCancelled(effect.FinalizationDeadlineNS) }
	for i := range effect.Data { h.Output = slices.Append(h.Alloc, h.Output, effect.Data[i]) }
}

func TestFinallyOutputCancellationCannotBeRecovered(t *testing.T) {
	a := t.Allocator(); e := core.NewEngine(a); r := eval.NewRegistry(a); operations.Register(r)
	parsed := script.Parse(a, "output-cancel.km", `result = (? (finally! ([] missing) ([] (out! "cleanup"))) "fallback")`)
	p := eval.Compile(a, e, parsed, r)
	h := finalizationHost{Alloc: a, Program: p, Quiet: true, CancelOutput: true}
	p.FinalizationNow, p.FinalizationQuiet, p.FinalizationHost = finalizationClock, finalizationQuiescent, &h
	p.SetDefinitionEffectSink(finalizationOutput, &h)
	n := p.Definition("result"); e.Request(n); stepUntilCurrent(e, n)
	if n.Current || n.Diagnostic.Code != "REF_MISSING" || !n.Diagnostic.Protected || !n.Diagnostic.Cancelled { t.Error("cancellation observed by the output driver was swallowed") }
	e.Free(); p.Free(); parsed.Free(); r.Free(); slices.Free(a, h.Output)
}

func finalizationAdvanceClock(c *eval.Context, value any, values []core.Value) eval.Result {
	_, _ = c, values
	h := value.(*finalizationHost)
	h.Now += 10000000
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func TestFinallyResultsAndFailures(t *testing.T) {
	sources := []string{
		`(finally! ([] 42) ([] (out! "cleanup")))`,
		`(finally! ([] :false) ([] :nil))`,
		`(finally! ([] :nil) ([] :false))`,
		`(finally! ([] (fail! "body")) ([] (out! "cleanup")))`,
		`(finally! ([] 42) ([] (fail! "cleanup")))`,
		`(finally! ([] (fail! "body")) ([] (fail! "cleanup")))`,
		`(let [finish finally!] (finish [timeout-ms: 1] ([] 42) ([] (out! "cleanup"))))`,
		`(apply finally! [([] 42) ([] (out! "cleanup"))])`,
		`(finally! ([] (finally! ([] 42) ([] (out! "inner")))) ([] (out! "outer")))`,
		`(finally! ([] result) ([] (out! "cleanup")))`,
	}
	codes := []string{"", "", "", "USER_ERROR", "USER_ERROR", "USER_ERROR", "", "", "", "DEP_CYCLE"}
	output := []string{"cleanup", "", "", "cleanup", "", "", "cleanup", "cleanup", "innerouter", "cleanup"}
	for i := range sources {
		a := t.Allocator()
		e := core.NewEngine(a)
		r := eval.NewRegistry(a)
		operations.Register(r)
		parsed := script.Parse(a, "finalization.km", "result = "+sources[i])
		p := eval.Compile(a, e, parsed, r)
		h := finalizationHost{Alloc: a, Quiet: true}
		p.FinalizationNow, p.FinalizationQuiet, p.FinalizationHost = finalizationClock, finalizationQuiescent, &h
		p.SetDefinitionEffectSink(finalizationOutput, &h)
		n := p.Definition("result")
		e.Request(n)
		stepUntilCurrent(e, n)
		if n.Diagnostic.Code != codes[i] || string(h.Output) != output[i] { t.Error("finally! outcome mismatch: "+sources[i]+" code="+n.Diagnostic.Code+" output="+string(h.Output)) }
		if i == 0 && n.Latest.Int != 42 { t.Error("body value lost") }
		if i == 1 && (n.Latest.Kind != core.Bool || n.Latest.Bool) { t.Error("false treated as failure") }
		if i == 2 && n.Latest.Kind != core.Nil { t.Error("nil treated as failure") }
		if i == 5 && (n.Diagnostic.Message != "body" || len(n.Diagnostic.Related)+len(n.Diagnostic.Notes) == 0) { t.Error("cleanup masked body failure") }
		e.Free(); p.Free(); parsed.Free(); r.Free(); slices.Free(a, h.Output)
	}
}

func TestFinallyValidationBeforeCallbacks(t *testing.T) {
	sources := []string{
		`(finally! [timeout-ms: 0] ([] (out! "body")) ([] (out! "cleanup")))`,
		`(finally! [timeout-ms: 60001] ([] (out! "body")) ([] (out! "cleanup")))`,
		`(finally! [timeout-ms: :true] ([] (out! "body")) ([] (out! "cleanup")))`,
		`(finally! [secret: "hidden"] ([] (out! "body")) ([] (out! "cleanup")))`,
		`(finally! [] ([] (out! "body")) ([] (out! "cleanup")))`,
		`(finally! ([] (out! "body")) ([required] required))`,
		`(finally! ([] (out! "body")) ((uppercase _)))`,
		`(finally! 42 ([] (out! "cleanup")))`,
	}
	for i := range sources {
		a := t.Allocator(); e := core.NewEngine(a); r := eval.NewRegistry(a); operations.Register(r)
		parsed := script.Parse(a, "finalization.km", "result = "+sources[i])
		p := eval.Compile(a, e, parsed, r)
		h := finalizationHost{Alloc: a, Quiet: true}
		p.SetDefinitionEffectSink(finalizationOutput, &h)
		n := p.Definition("result"); e.Request(n); stepUntilCurrent(e, n)
		if n.Diagnostic.Code != "EXPR_INVALID" || len(h.Output) != 0 { t.Error("invalid finally! ran callbacks or hid validation: "+sources[i]) }
		e.Free(); p.Free(); parsed.Free(); r.Free(); slices.Free(a, h.Output)
	}
}

func TestFinallySuspensionCancellationAndDeadline(t *testing.T) {
	for mode := 0; mode < 6; mode++ {
		a := t.Allocator(); e := core.NewEngine(a); r := eval.NewRegistry(a); operations.Register(r)
		r.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
		source := `result = (finally! ([] (async)) ([] (out! "cleanup")))`
		if mode >= 2 { source = `result = (finally! [timeout-ms: 1] ([] 42) ([] (async)))` }
		if mode == 5 { source = `result = (finally! ([] (async)) ([] (out! "cleanup")))` }
		parsed := script.Parse(a, "finalization.km", source); p := eval.Compile(a, e, parsed, r)
		h := finalizationHost{Alloc: a, Quiet: true}
		p.FinalizationNow, p.FinalizationQuiet, p.FinalizationHost = finalizationClock, finalizationQuiescent, &h
		p.SetDefinitionEffectSink(finalizationOutput, &h)
		n := p.Definition("result"); e.Request(n)
		request := stepUntilRequest(e, p)
		if !request.OK { t.Fatal("callback did not suspend"); return }
		if mode == 0 || mode == 2 {
			e.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, Value: core.Value{Kind: core.Int, Int: 42}})
			stepUntilCurrent(e, n)
			if !n.Current || n.Latest.Int != 42 || (mode == 0 && string(h.Output) != "cleanup") { t.Error("callback replayed or body value lost on resume") }
		} else if mode == 1 {
			h.Quiet = false
			e.Cancel(n)
			for j := 0; j < 5; j++ { e.Step() }
			if len(h.Output) != 0 || n.State == core.NodeCancelled { t.Error("cleanup or terminal outcome preceded reaping") }
			h.Quiet = true
			for j := 0; j < 20; j++ { p.PollFinalizers(); e.Step() }
			if string(h.Output) != "cleanup" || n.State != core.NodeCancelled { t.Errorf("cancelled scope did not finalize exactly once: output=%s state=%d code=%s node=%d request-node=%d", string(h.Output), n.State, n.Diagnostic.Code, n.ID, request.Request.NodeID) }
		} else if mode == 3 {
			h.Now = 1000001
			p.PollFinalizers()
			stepUntilCurrent(e, n)
			if n.Diagnostic.Code != "FINALIZE_TIMEOUT" { t.Error("suspended cleanup did not time out: "+n.Diagnostic.Code) }
		} else if mode == 4 {
			e.Cancel(n)
			if e.NextCancellation().RequestID != 0 { t.Error("cancellation interrupted designated cleanup") }
			e.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID})
			for j := 0; j < 20; j++ { p.PollFinalizers(); e.Step() }
			if n.State != core.NodeCancelled || p.Finalizing() { t.Error("cancellation during cleanup did not finish") }
		} else {
			generation := n.Generation
			e.Invalidate(n)
			if n.Generation != generation { t.Error("invalidation discarded an armed generation") }
			next := stepUntilRequest(e, p)
			if !next.OK || string(h.Output) != "cleanup" || n.Generation == generation { t.Error("replacement ran before old finalization") }
			if next.OK {
				e.Complete(core.Completion{NodeID: next.Request.NodeID, Generation: next.Request.Generation, Attempt: next.Request.Attempt, RequestID: next.Request.ID})
				next.Request.Free(a)
				stepUntilCurrent(e, n)
			}
		}
		request.Request.Free(a)
		for { cancellation := e.NextCancellation(); if cancellation.RequestID == 0 { break } }
		// No callback or host work is permitted from allocator destruction.
		e.Free(); p.Free(); parsed.Free(); r.Free(); slices.Free(a, h.Output)
	}
}

func TestFinallyNestedSharedDeadline(t *testing.T) {
	for mode := 0; mode < 2; mode++ {
		a := t.Allocator(); e := core.NewEngine(a); r := eval.NewRegistry(a); operations.Register(r)
		r.Add(eval.Operation{Name: "async", Call: asyncOperation, MinArity: 0, MaxArity: 0})
		source := `result = (finally! [timeout-ms: 1] ([] (finally! [timeout-ms: 60000] ([] (fail! "body")) ([] (async)))) ([] (out! "outer")))`
		if mode == 1 { source = `result = (finally! [timeout-ms: 1] ([] 42) ([] (finally! ([] (async)) ([] (out! "inner")))))` }
		parsed := script.Parse(a, "nested.km", source); p := eval.Compile(a, e, parsed, r)
		h := finalizationHost{Alloc: a, Quiet: true}
		p.FinalizationNow, p.FinalizationQuiet, p.FinalizationHost = finalizationClock, finalizationQuiescent, &h
		p.SetDefinitionEffectSink(finalizationOutput, &h)
		n := p.Definition("result"); e.Request(n)
		request := stepUntilRequest(e, p)
		if !request.OK { t.Fatal("nested scope did not suspend"); return }
		h.Now = 1000001
		p.PollFinalizers(); stepUntilCurrent(e, n)
		if len(h.Output) != 0 || p.Finalizing() { t.Error("exhausted nested finalizer ran or escaped") }
		code := "USER_ERROR"
		if mode == 1 { code = "FINALIZE_TIMEOUT" }
		if n.Diagnostic.Code != code { t.Error("shared deadline changed primary outcome: "+n.Diagnostic.Code) }
		request.Request.Free(a)
		e.Free(); p.Free(); parsed.Free(); r.Free(); slices.Free(a, h.Output)
	}
}

func TestFinallyUnavailableDriverDoesNotArm(t *testing.T) {
	a := t.Allocator(); e := core.NewEngine(a); r := eval.NewRegistry(a); operations.Register(r)
	parsed := script.Parse(a, "finalization.km", `result = (finally! ([] (out! "body")) ([] (out! "cleanup")))`)
	p := eval.Compile(a, e, parsed, r)
	n := p.Definition("result"); e.Request(n); stepUntilCurrent(e, n)
	if n.Diagnostic.Code != "FEATURE_UNSUP" || p.Finalizing() { t.Error("unsupported host armed cleanup") }
	e.Free(); p.Free(); parsed.Free(); r.Free()
}

func TestFinallyStatementsRetainBindingsAndAcceptedWrites(t *testing.T) {
	a := t.Allocator(); e := core.NewEngine(a); r := eval.NewRegistry(a); operations.Register(r)
	parsed := script.Parse(a, "sequence.km", `result = (finally! ([] (def saved 42) (write! "first" "one") (write! "second" "two") saved) ([] (out! "cleanup")))`)
	p := eval.Compile(a, e, parsed, r)
	h := finalizationHost{Alloc: a, Quiet: true}
	p.FinalizationNow, p.FinalizationQuiet, p.FinalizationHost = finalizationClock, finalizationQuiescent, &h
	p.SetDefinitionEffectSink(finalizationOutput, &h)
	p.SetGrants([]eval.Grant{{Capability: eval.Write}})
	n := p.Definition("result"); e.Request(n)
	for i := 0; i < 2; i++ {
		request := stepUntilRequest(e, p)
		if !request.OK { t.Fatal("write did not suspend"); return }
		path := "first"
		if i == 1 { path = "second" }
		if request.Request.Kind != host.RequestWriteFile || host.PayloadPath(request.Request.Payload) != path { t.Error("completed statement replayed at a later completion") }
		e.Complete(core.Completion{NodeID: request.Request.NodeID, Generation: request.Request.Generation, Attempt: request.Request.Attempt, RequestID: request.Request.ID, HasValue: true, Value: core.Value{Kind: core.Nil}})
		request.Request.Free(a)
	}
	stepUntilCurrent(e, n)
	if !n.Current || n.Latest.Int != 42 || string(h.Output) != "cleanup" { t.Error("callback lost lexical bindings or sequenced effects") }
	e.Free(); p.Free(); parsed.Free(); r.Free(); slices.Free(a, h.Output)
}

func TestFinallyHandledFailureDoesNotStartOuterUnwind(t *testing.T) {
	a := t.Allocator(); e := core.NewEngine(a); r := eval.NewRegistry(a); operations.Register(r)
	h := finalizationHost{Alloc: a, Quiet: true}
	r.Add(eval.Operation{Name: "advance-clock", Call: finalizationAdvanceClock, Context: &h, MinArity: 0, MaxArity: 0})
	parsed := script.Parse(a, "handled.km", `result = (finally! [timeout-ms: 1] ([] (? (finally! ([] missing) ([] :nil)) 0) (advance-clock) (fail! "later")) ([] (out! "outer")))`)
	p := eval.Compile(a, e, parsed, r)
	p.FinalizationNow, p.FinalizationQuiet, p.FinalizationHost = finalizationClock, finalizationQuiescent, &h
	p.SetDefinitionEffectSink(finalizationOutput, &h)
	n := p.Definition("result"); e.Request(n); stepUntilCurrent(e, n)
	if n.Diagnostic.Code != "USER_ERROR" || string(h.Output) != "outer" { t.Error("a locally handled failure consumed an outer cleanup budget") }
	e.Free(); p.Free(); parsed.Free(); r.Free(); slices.Free(a, h.Output)
}
