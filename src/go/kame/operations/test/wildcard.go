package lib_test

import (
	"kame/core"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/operations"
	"solod.dev/so/mem"
	"solod.dev/so/strconv"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

type wildcardProgressState struct {
	Program    *eval.Program
	Expression *expr.Expr
	Engine     *core.Engine
	Visits     int
}

func wildcardProgressDependency(value any, key core.ResourceKey) {
	state := value.(*wildcardProgressState)
	state.Visits++
	state.Engine.Add(key, publishWildcardDependency, nil)
}

func publishWildcardDependency(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	c.Publish(core.Value{Kind: core.Nil})
	return core.ProducerCompleted
}

func evaluateWildcardProgress(c *core.EngineContext, nodeID int64) core.ProducerResult {
	_ = nodeID
	state := c.Context().(*wildcardProgressState)
	context := eval.Context{Run: c.Allocator(), Engine: c, Grants: []eval.Grant{{Capability: eval.Read}}, DependencyObserver: wildcardProgressDependency, ResolverState: state}
	result := state.Program.EvaluateWith(state.Expression, &context)
	if result.Diagnostic.Code != "" {
		c.Fail(result.Diagnostic)
		result.Value.Free(c.Allocator())
		return core.ProducerFailed
	}
	if result.Waiting {
		return core.ProducerWaiting
	}
	c.Publish(result.Value)
	return core.ProducerCompleted
}

func TestWildcardUnionResumesWithoutRevisitingEarlierPatterns(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	p := eval.Compile(a, engine, parsed, registry)
	text := strings.NewBuilder(a)
	text.WriteString(`(wildcard ["./entries/z" "./entries/a"]`)
	const count = 16
	for i := 0; i < count; i++ {
		var digits [32]byte
		text.WriteString(` "./union/` + strconv.Itoa(digits[:], i) + `/*"`)
	}
	text.WriteByte(')')
	document := expr.Parse(a, "union.km", text.String())
	text.Free()
	state := mem.Alloc[wildcardProgressState](a)
	state.Program, state.Expression, state.Engine = p, document.Expr, engine
	node := engine.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "union"}, evaluateWildcardProgress, state)
	engine.Request(node)
	for phase := 0; phase < 3; phase++ {
		if phase != 0 {
			engine.Invalidate(node)
		}
		state.Visits = 0
		globs := 0
		for tick := 0; tick < 512 && !node.Current && node.Diagnostic.Code == ""; tick++ {
			engine.Step()
			next := p.Requests.Next()
			if !next.OK {
				continue
			}
			request := next.Request
			globs++
			completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, HasValue: true}
			if phase == 1 && globs == 3 {
				completion.HasValue = false
				completion.Diagnostic = core.Diagnostic{Code: "FS_ERR"}
			} else {
				name := "./entries/b"
				if phase == 2 {
					name = "./entries/c"
				}
				completion.Value = core.NewList(a, []core.Value{{Kind: core.String, Text: name}, {Kind: core.String, Text: "./entries/a"}})
			}
			engine.Complete(completion)
			request.Free(a)
		}
		if phase == 1 {
			if node.Diagnostic.Code != "FS_ERR" || globs != 3 {
				t.Error("union hid a later pattern failure")
			}
		} else {
			middle := "./entries/b"
			if phase == 2 {
				middle = "./entries/c"
			}
			if !node.Current || len(node.Latest.List) != 3 || globs != count {
				t.Error("union did not settle or repeated host requests")
			} else if node.Latest.List[0].Text != "./entries/a" || node.Latest.List[1].Text != middle || node.Latest.List[2].Text != "./entries/z" {
				t.Error("union lost sorted membership after invalidation")
			}
		}
		if state.Visits > 6*count {
			t.Errorf("union replay revisited earlier patterns: %d visits", state.Visits)
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
	document.Free()
	mem.Free(a, state)
}

func TestWildcardUnionPreservesFirstPathProvenance(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	p := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "union.km", `(let [name "*"] (wildcard ["./z" "./a" "./z"] [./path/@{name} "./a"] ["./path/*"]))`)
	context := eval.Context{Run: a, Grants: []eval.Grant{{Capability: eval.Read}}}
	r := p.EvaluateWith(document.Expr, &context)
	if r.Waiting || r.Diagnostic.Code != "" || len(r.Value.List) != 3 {
		t.Error("literal union failed")
	} else if r.Value.List[0].Text != "./a" || r.Value.List[1].Text != "./path/*" || r.Value.List[1].GlobPattern != "./path/\\*" || r.Value.List[2].Text != "./z" {
		t.Error("union lost sorted order or first duplicate's provenance")
	}
	r.Free(a)
	document.Free()
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
}

func TestWildcardFilteringResumesWithoutRevisitingEarlierEntries(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	parsed := script.Parse(a, "test", "")
	p := eval.Compile(a, engine, parsed, registry)
	document := expr.Parse(a, "progress.km", `(files ./entries/*)`)
	state := mem.Alloc[wildcardProgressState](a)
	state.Program, state.Expression, state.Engine = p, document.Expr, engine
	node := engine.Add(core.ResourceKey{Kind: core.ResourceTask, Name: "progress"}, evaluateWildcardProgress, state)
	engine.Request(node)
	const count = 16
	for phase := 0; phase < 2; phase++ {
		if phase != 0 {
			engine.Invalidate(node)
		}
		state.Visits = 0
		metadata, globs := 0, 0
		restarted := false
		for tick := 0; tick < 512 && !node.Current && node.Diagnostic.Code == ""; tick++ {
			engine.Step()
			next := p.Requests.Next()
			if !next.OK {
				continue
			}
			request := next.Request
			value := core.Value{}
			if host.PayloadText(request.Payload, host.FieldOp) == host.OpWildcard {
				globs++
				var paths [count]core.Value
				for i := range paths {
					var digits [32]byte
					paths[i] = core.NewString(a, "./entries/"+strconv.Itoa(digits[:], i))
				}
				value = core.NewList(a, paths[:])
				for i := range paths {
					paths[i].Free(a)
				}
			} else {
				metadata++
				value = core.NewRecord(a, []core.RecordField{{Key: "regular", Value: core.Value{Kind: core.Bool, Bool: phase == 0 || !restarted}}, {Key: "dir", Value: core.Value{Kind: core.Bool, Bool: phase != 0 && restarted}}, {Key: "link", Value: core.Value{Kind: core.Bool}}})
			}
			engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, HasValue: true, Value: value})
			request.Free(a)
			if phase == 1 && metadata == 4 && !restarted {
				// Invalidate a partially filtered union, not just a finished result.
				engine.Invalidate(node)
				restarted = true
			}
		}
		want := count
		if phase == 1 {
			want = 0
		}
		if !node.Current || node.Diagnostic.Code != "" || len(node.Latest.List) != want {
			t.Error("wildcard progress survived the wrong generation or lost entries")
		}
		wantMetadata, wantGlobs := count, 1
		if phase == 1 {
			wantMetadata += 4
			wantGlobs++
		}
		// Dependency setup and host completion may each suspend, but work must
		// stay linear in the reads, including the interrupted generation.
		if state.Visits > 6*(wantMetadata+wantGlobs) {
			t.Errorf("filter replay revisited earlier dependencies: %d visits", state.Visits)
		}
		if metadata != wantMetadata || globs != wantGlobs {
			t.Errorf("unexpected host reads: %d metadata, %d globs", metadata, globs)
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
	document.Free()
	mem.Free(a, state)
}

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
		if r.Diagnostic.Code != "EXPR_INVALID" || r.Waiting || p.Requests.Next().OK {
			t.Error("invalid wildcard flag call was not rejected before requests")
		}
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
		if !next.OK {
			continue
		}
		request := next.Request
		completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, HasValue: true}
		if host.PayloadText(request.Payload, host.FieldOp) == host.OpWildcard {
			globs++
			values := []core.Value{{Kind: core.String, Text: "./entries/a"}, {Kind: core.String, Text: "./entries/dir"}, {Kind: core.String, Text: "./entries/link"}, {Kind: core.String, Text: "./entries/other"}}
			completion.Value = core.NewList(a, values)
		} else {
			metadata++
			name := host.PayloadPath(request.Payload)
			if failMetadata && strings.HasSuffix(name, "/link") {
				completion.HasValue = false
				completion.Diagnostic = core.Diagnostic{Code: "FS_ERR"}
			} else {
				fields := []core.RecordField{{Key: "regular", Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(name, "/a")}}, {Key: "dir", Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(name, "/dir")}}, {Key: "link", Value: core.Value{Kind: core.Bool, Bool: strings.HasSuffix(name, "/link")}}}
				completion.Value = core.NewRecord(a, fields)
			}
		}
		engine.Complete(completion)
		request.Free(a)
	}
	if failMetadata {
		if node.Diagnostic.Code != "FS_ERR" {
			t.Error("excluded entry's metadata failure was hidden")
		}
	} else {
		if !node.Current || len(node.Latest.List) != 1 || node.Latest.List[0].Text != want || globs != 1 || metadata != 4 {
			t.Error("wildcard filtering or request replay was incorrect")
		}
		observed := 0
		for i := range node.Observations {
			if node.Observations[i].Aspect == core.ObservationLinkMetadata {
				observed++
			}
		}
		if observed != 4 {
			t.Error("excluded wildcard entries lost kind observations")
		}
	}
	engine.Free()
	p.Free()
	parsed.Free()
	registry.Free()
	document.Free()
	mem.Free(a, state)
}
