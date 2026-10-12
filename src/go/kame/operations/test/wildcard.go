package lib_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestWildcardFlagsRejectInvalidCallsBeforeRequests(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	p := eval.Compile(a, engine, parsed, registry)
	texts := []string{`(wildcard :+files)`, `(wildcard :-dir :-symlinks)`, `(wildcard :+files :-files ./entries/*)`, `(wildcard :-dir :+dir ./entries/*)`, `(wildcard :+bogus ./entries/*)`, `(wildcard "-unknown" ./entries/*)`, `(wildcard "+files/child")`, `(wildcard :+bogus ([x] x))`, `(wildcard :+files :-files ([x] x))`}
	for i := range texts {
		e := expr.Parse(a, "flags.km", texts[i])
		context := eval.Context{Run: a, Grants: []eval.Grant{{Capability: eval.Read}}}
		r := p.EvaluateWith(e.Expr, &context)
		if r.Diagnostic.Code != "EXPR_INVALID" || r.Waiting || p.Requests.Next().OK { t.Error("invalid wildcard flag call was not rejected before requests") }
		r.Free(a)
		e.Free()
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestWildcardKindMasksRetainExcludedEntryObservations(t *testing.T) {
	checkWildcardKinds(t, `(wildcard :+files :-symlinks ./entries/*)`, "./entries/a", false)
	checkWildcardKinds(t, `(wildcard ./entries/* :+files :+files)`, "./entries/a", false)
	checkWildcardKinds(t, `(wildcard :-files :-dir :-symlinks ./entries/*)`, "./entries/other", false)
	checkWildcardKinds(t, `(let [flag "+symlinks"] (wildcard flag ./entries/*))`, "./entries/link", false)
	checkWildcardKinds(t, `(wildcard :+files ./entries/*)`, "", true)
}

func checkWildcardKinds(t *testing.T, text string, want string, failMetadata bool) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	p := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "kinds.km", text)
	state := mem.Alloc[observedFileState](a)
	state.Program, state.Expression = p, document.Expr
	node := engine.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "kinds"}, evaluateObservedFile, state)
	engine.Request(node)
	globs, metadata := 0, 0
	for i := 0; i < 128 && !node.Current && node.Diagnostic.Code == ""; i++ {
		engine.Step()
		next := p.Requests.Next()
		if !next.OK { continue }
		request := next.Request
		completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, HasValue: true}
		if host.PayloadText(request.Payload, host.FieldOp) == host.OpWildcard {
			globs++
			values := []core.Value{{Kind: core.String, Text: "./entries/a"}, {Kind: core.String, Text: "./entries/dir"}, {Kind: core.String, Text: "./entries/link"}, {Kind: core.String, Text: "./entries/other"}}
			completion.Value = core.NewList(a, values)
		} else {
			metadata++
			name := host.PayloadPath(request.Payload)
			if failMetadata && strings.HasSuffix(name, "/link") { completion.HasValue = false; completion.Diagnostic = core.Diagnostic{Code: "FS_ERR"} } else {
				fields := []core.RecordField{{Key: "regular", Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(name, "/a")}}, {Key: "dir", Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(name, "/dir")}}, {Key: "link", Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(name, "/link")}}}
				completion.Value = core.NewRecord(a, fields)
			}
		}
		engine.Complete(completion)
		request.Free(a)
	}
	if failMetadata {
		if node.Diagnostic.Code != "FS_ERR" { t.Error("excluded entry's metadata failure was hidden") }
	} else {
		if !node.Current || len(node.Latest.List) != 1 || node.Latest.List[0].Text != want || globs != 1 || metadata != 4 { t.Error("wildcard filtering or request replay was incorrect") }
		observed := 0
		for i := range node.Observations { if node.Observations[i].Aspect == core.ObservationLinkMetadata { observed++ } }
		if observed != 4 { t.Error("excluded wildcard entries lost kind observations") }
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
	document.Free()
	mem.Free(a, state)
}
