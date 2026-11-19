package program

import (
	"bytes"
	"encoding/json"
	"fmt"
	"kame/core"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/operations"
	"strings"
	"testing"
)

func interactiveTestSession(t testing.TB) *Session {
	t.Helper()
	registry := eval.NewRegistry(mem.System)
	operations.Register(registry)
	compiled := CompileSession(mem.System, nil, registry, Options{Directory: ".", Host: posix.New(mem.System)})
	if compiled.Session == nil {
		t.Fatalf("compile session: %+v", compiled.Diagnostics)
	}
	s := compiled.Session
	compiled.Free(mem.System)
	s.Program.Eval.SetDefinitionEffectSink(func(any, eval.Effect) {}, nil)
	t.Cleanup(func() { s.Free(); registry.Free() })
	return s
}

func interactiveTestEntry(t testing.TB, s *Session, name, text string) {
	t.Helper()
	first, diagnostics := s.AppendKash(name, text)
	if len(diagnostics) != 0 {
		t.Fatalf("append %s: %+v", name, diagnostics)
	}
	end := len(s.Work)
	defer s.DiscardWork(first, end)
	for i := first; i < end; i++ {
		entry := s.Work[i].Expression.EntrySource
		if entry == nil || entry.Name != name || entry.Text != text {
			t.Fatalf("%s lost its independent source", name)
		}
		start := s.Start(i)
		if start.Handle == nil {
			t.Fatalf("start %s: %+v", name, start.Diagnostic)
		}
		h := start.Handle
		done := false
		for tick := 0; tick < 1000; tick++ {
			s.Program.Tick(0)
			result := h.Poll()
			if !result.Done {
				continue
			}
			if result.Result.Diagnostic.Code != "" {
				t.Fatalf("run %s: %+v", name, result.Result.Diagnostic)
			}
			result.Result.Free(mem.System)
			done = true
			break
		}
		h.Free()
		if !done {
			t.Fatalf("%s did not finish", name)
		}
	}
}

func TestInteractiveEntriesDoNotCopySessionSource(t *testing.T) {
	s := interactiveTestSession(t)
	original := s.Program.Parsed.Source.Text
	text := "(nop \"" + strings.Repeat("payload", 100) + "\")"
	for i := 0; i < 256; i++ {
		interactiveTestEntry(t, s, fmt.Sprintf("<entry:%d>", i), text)
		if s.Program.Parsed.Source.Text != original {
			t.Fatal("appending an entry changed the combined source")
		}
		if len(s.Work) != 0 || len(s.Program.Parsed.Items) != 0 || len(s.Program.Eval.SourceParts) != 0 {
			t.Fatal("completed entry retained queued work, parsed syntax or source-table entries")
		}
	}
	work, parts := len(s.Work), len(s.Program.Eval.SourceParts)
	_, diagnostics := s.AppendKash("<rejected>", "let broken = (")
	if len(diagnostics) == 0 {
		t.Fatal("invalid entry was accepted")
	}
	for i := range diagnostics {
		diagnostics[i].Free(mem.System)
	}
	if len(s.Work) != work || len(s.Program.Eval.SourceParts) != parts || s.Program.Parsed.Source.Text != original {
		t.Fatal("rejected entry changed the session")
	}
}

func TestSessionRetiresStatementsWithoutReplayingLazyEffects(t *testing.T) {
	s := interactiveTestSession(t)
	outputs := 0
	s.Program.Eval.SetDefinitionEffectSink(func(_ any, effect eval.Effect) {
		if effect.Kind == eval.EffectOut {
			outputs++
		}
	}, nil)
	interactiveTestEntry(t, s, "<definition>", "saved = (out! \"once\")")
	for i := 0; i < 256; i++ {
		interactiveTestEntry(t, s, "<use>", "(nop saved)\nfor i in [1, 2]\n\tif (nop true)\n\t\t(nop i)\n")
		if len(s.Program.Eval.OperationStates) != 0 || len(s.Program.Eval.OperationMemos) != 0 {
			t.Fatal("completed statement retained operation progress")
		}
		for _, node := range s.Program.Engine.TrackedNodes() {
			if node.Key.Kind == core.ResourceDefinition && strings.HasPrefix(node.Key.Name, "<statement:") {
				t.Fatal("completed statement remained in the engine")
			}
		}
		if len(s.retained) != 1 {
			t.Fatalf("retained %d roots, want one lazy definition", len(s.retained))
		}
	}
	if outputs != 1 {
		t.Fatalf("lazy effect ran %d times, want once", outputs)
	}
}

func TestSessionRetiresCompletedCallableScopes(t *testing.T) {
	s := interactiveTestSession(t)
	interactiveTestEntry(t, s, "<definition>", "def helper value\n\treturn 0\n")
	interactiveTestEntry(t, s, "<nested>", "def nested value\n\tdefer (helper value)\n\t(helper value)\n")
	for _, call := range []string{"(nested 1)", "(finally! (() => (nested 1)) (() => (nop ())))"} {
		for i := 0; i < 128; i++ {
			interactiveTestEntry(t, s, "<call>", call)
			if len(s.Program.Eval.DeferScopes) != 1 {
				t.Fatalf("retained %d defer scopes, want invocation scope only", len(s.Program.Eval.DeferScopes))
			}
			if len(s.Program.Eval.OperationStates) != 0 || len(s.Program.Engine.TrackedNodes()) != 0 {
				t.Fatal("completed callable retained execution progress")
			}
		}
	}
}

func TestInteractiveWorkReclaimsSyntaxWithoutMovingOtherEntries(t *testing.T) {
	s := interactiveTestSession(t)
	first, diagnostics := s.AppendKash("<abandoned>", "(nop \"abandoned\")")
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	end := len(s.Work)
	abandoned := s.Work[first].Expression
	second, diagnostics := s.AppendKash("<pending>", "(nop \"pending\")")
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	last := len(s.Work)
	s.DiscardWork(first, end)
	if abandoned.EntrySource != nil || len(s.Work) != last || s.Work[second].Expression.EntrySource.Name != "<pending>" {
		t.Fatal("discarding an entry retained its syntax or moved pending work")
	}
	start := s.Start(second)
	if start.Handle == nil {
		t.Fatal(start.Diagnostic)
	}
	h := start.Handle
	repeated := s.Start(second)
	if repeated.Handle != nil || repeated.Diagnostic.Code != "HOST_FAIL" {
		t.Fatal("interactive syntax was lent to a second statement node")
	}
	repeated.Diagnostic.Free(mem.System)
	for i := 0; i < 100 && !h.Node.Current; i++ {
		s.Program.Tick(0)
	}
	result := h.Poll()
	if !result.Done || result.Result.Diagnostic.Code != "" || result.Result.Value.Text != "pending" {
		t.Fatal("pending entry did not survive reclaiming an earlier entry")
	}
	result.Result.Free(mem.System)
	h.Free()
	s.DiscardWork(second, last)
	if len(s.Work) != 0 || len(s.Program.Parsed.Items) != 0 {
		t.Fatal("queue holes or transient syntax were retained")
	}
}

func TestInteractiveEscapedCallableKeepsItsSyntax(t *testing.T) {
	s := interactiveTestSession(t)
	interactiveTestEntry(t, s, "<saved>", "def escaped x = str x \"!\"")
	for i := 0; i < 256; i++ {
		interactiveTestEntry(t, s, "<later>", "(nop ())")
	}
	interactiveTestEntry(t, s, "<use>", "(if (ne? (apply escaped [\"ok\"]) \"ok!\") then (fail! \"escaped syntax was freed\") else nil)")
	if len(s.Program.Parsed.Items) != 0 || len(s.Work) != 0 {
		t.Fatal("escaped callable required retaining transient work or parsed history")
	}
}

func TestInteractiveDiagnosticOwnsSourceWithoutSerializingIt(t *testing.T) {
	s := interactiveTestSession(t)
	text := "(fail! \"expected-error\") # private-source-comment"
	first, diagnostics := s.AppendKash("<failure>", text)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	syntax, err := json.Marshal(s.Work[first].Expression)
	if err != nil || strings.Contains(string(syntax), "private-source-comment") {
		t.Fatal("AST JSON leaked internal source provenance")
	}
	start := s.Start(first)
	if start.Handle == nil {
		t.Fatal(start.Diagnostic)
	}
	h := start.Handle
	for i := 0; i < 100 && h.Node.State != core.NodeFailed; i++ {
		s.Program.Tick(0)
	}
	result := h.Poll()
	if !result.Done || result.Result.Diagnostic.Code != "USER_ERROR" {
		t.Fatal("entry did not produce the expected diagnostic")
	}
	d := result.Result.Diagnostic.Clone(mem.System)
	defer d.Free(mem.System)
	result.Result.Free(mem.System)
	h.Free()
	s.DiscardWork(first, len(s.Work))
	if d.Source != "<failure>" || d.SourceText != text || len(d.Frames) == 0 || d.Frames[0].SourceText != text {
		t.Fatal("reclaiming syntax lost the retained diagnostic's source")
	}
	var out bytes.Buffer
	WriteJSONDiagnostic(&out, d)
	encoded, err := json.Marshal(d)
	if err != nil || !strings.Contains(out.String(), `"code":"USER_ERROR"`) ||
		strings.Contains(out.String(), "private-source-comment") || strings.Contains(string(encoded), "private-source-comment") {
		t.Fatal("diagnostic JSON leaked internal source text")
	}
}

func TestInteractiveRebindingDoesNotRetainDeclarationHistory(t *testing.T) {
	s := interactiveTestSession(t)
	for i := 0; i < 512; i++ {
		interactiveTestEntry(t, s, "<value>", fmt.Sprintf("let value = \"value-%d\"", i))
		interactiveTestEntry(t, s, "<function>", "def helper arg\n\treturn 0\n")
		if len(s.Program.Eval.Scope.Bindings) != 2 || len(s.Program.Parsed.Items) != 0 ||
			len(s.Work) != 0 || len(s.Program.Eval.SourceParts) != 0 {
			t.Fatal("rebinding retained obsolete slots, syntax or source history")
		}
	}
	interactiveTestEntry(t, s, "<check>", "(if (ne? value \"value-511\") then (fail! \"lost latest value\") else nil)")
}

func TestInteractiveSavedAliasesKeepReplacedFunctionPayload(t *testing.T) {
	s := interactiveTestSession(t)
	var out strings.Builder
	s.Program.Eval.SetDefinitionEffectSink(func(_ any, effect eval.Effect) {
		out.Write(effect.Data)
	}, nil)
	interactiveTestEntry(t, s, "<old>", "def helper arg\n\t(out! (str \"old-\" arg))\n")
	old := s.Program.Eval.Scope.Bindings[0].Function.Definition
	interactiveTestEntry(t, s, "<aliases>", "let alias = helper\nlet keep = [helper, {call: helper}]")
	interactiveTestEntry(t, s, "<shadow>", "let helper = 42")
	interactiveTestEntry(t, s, "<new>", "def helper arg\n\t(out! (str \"new-\" arg))\n")
	if old.Expression == nil {
		t.Fatal("replacing a function freed its saved aliases' code")
	}
	interactiveTestEntry(t, s, "<calls>", "(alias \"a\")\n((first keep) \"b\")\n((get \"call\" (nth 1 keep)) \"c\")\n(helper \"d\")")
	if out.String() != "old-aold-bold-cnew-d" {
		t.Fatalf("saved aliases changed implementation: %q", out.String())
	}
	interactiveTestEntry(t, s, "<release>", "let alias = nil\nlet keep = nil")
	if old.Expression != nil {
		t.Fatal("last alias release retained the obsolete function body")
	}
}

func TestInteractiveRebindingPreservesSuspendedCallsAndCleanup(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancel), func(t *testing.T) {
			s := interactiveTestSession(t)
			var out strings.Builder
			s.Program.Eval.SetDefinitionEffectSink(func(_ any, effect eval.Effect) {
				out.Write(effect.Data)
			}, nil)
			interactiveTestEntry(t, s, "<old>", "def worker value\n\tlocal = (str \"cleanup-\" value)\n\tdefer (out! local)\n\t(out! (str \"started-\" value))\n\tfor i in [1, 2]\n\t\t(nop i)\n\t(out! (str \"old-\" value))\n")
			old := s.Program.Eval.Scope.Bindings[0].Function.Definition
			first, diagnostics := s.AppendKash("<pending>", "((if (out! \"head-\") then worker else worker) \"a\")")
			if len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			end := len(s.Work)
			start := s.Start(first)
			if start.Handle == nil {
				t.Fatal(start.Diagnostic)
			}
			h := start.Handle
			registered := false
			for i := 0; i < 100 && !h.Node.Current; i++ {
				s.Program.Tick(0)
				for _, scope := range s.Program.Eval.DeferScopes {
					registered = registered || len(scope.Actions) != 0
				}
				if registered && h.Node.Submitted {
					break
				}
			}
			if h.Node.Current || !registered {
				t.Fatal("call did not suspend after registering its cleanup")
			}
			interactiveTestEntry(t, s, "<new>", "def worker value\n\t(out! \"new\")\n")
			if old.Expression == nil {
				t.Fatal("rebinding freed a suspended invocation's code")
			}
			if cancel {
				h.Cancel()
				h.Free()
				s.DiscardWork(first, end)
				if old.Expression == nil {
					t.Fatal("cancellation freed code still borrowed by deferred cleanup")
				}
				s.Program.Eval.BeginFinalization()
				for i := 0; i < 1000 && s.Program.Eval.Finalizing(); i++ {
					s.Program.Eval.AdvanceFinalization()
					s.Program.Tick(0)
				}
				if s.Program.Eval.Finalizing() || out.String() != "cleanup-a" {
					t.Fatalf("cancelled call lost its cleanup or replayed its head: %q", out.String())
				}
				return
			}
			for i := 0; i < 100 && !h.Node.Current; i++ {
				s.Program.Tick(0)
			}
			result := h.Poll()
			if !result.Done || result.Result.Diagnostic.Code != "" {
				t.Fatalf("suspended call failed after rebinding: %+v", result.Result.Diagnostic)
			}
			result.Result.Free(mem.System)
			h.Free()
			s.DiscardWork(first, end)
			if old.Expression != nil {
				t.Fatal("finished invocation and cleanup retained replaced code")
			}
			interactiveTestEntry(t, s, "<latest>", "(worker \"b\")")
			if out.String() != "cleanup-ahead-started-aold-anew" {
				t.Fatalf("head was replayed, code changed mid-call or cleanup lost its scope: %q", out.String())
			}
		})
	}
}

func TestInteractiveLazyDefinitionCannotBeRedeclaredAfterShadowing(t *testing.T) {
	s := interactiveTestSession(t)
	interactiveTestEntry(t, s, "<lazy>", "saved = 1")
	interactiveTestEntry(t, s, "<shadow>", "let saved = 2")
	_, diagnostics := s.AppendKash("<duplicate>", "saved = 3")
	if len(diagnostics) != 1 || diagnostics[0].Code != "DEF_INVALID" {
		t.Fatal("shadowing allowed duplicate registration of a lazy engine resource")
	}
	diagnostics[0].Free(mem.System)
}

func TestRejectedReturnedCallableDoesNotPinReplacedCode(t *testing.T) {
	s := interactiveTestSession(t)
	interactiveTestEntry(t, s, "<factory>", "def factory () = (x => x)")
	body := s.Program.Eval.Scope.Bindings[0].Function.Expression
	if body == nil {
		t.Fatal("factory definition has no expression")
	}
	first, diagnostics := s.AppendKash("<rejected>", "(factory ())")
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	start := s.Start(first)
	if start.Handle == nil {
		t.Fatal(start.Diagnostic)
	}
	h := start.Handle
	for i := 0; i < 100 && h.Node.State != core.NodeFailed; i++ {
		s.Program.Tick(0)
	}
	result := h.Poll()
	if !result.Done || result.Result.Diagnostic.Code != "EXPR_INVALID" {
		t.Fatal("publishing a transient callable was not rejected")
	}
	result.Result.Free(mem.System)
	h.Free()
	s.DiscardWork(first, len(s.Work))
	interactiveTestEntry(t, s, "<replace>", "let factory = nil")
	if body.Kind != expr.Invalid {
		t.Fatal("rejected callable kept its factory's code pinned")
	}
}

func BenchmarkInteractiveEntries(b *testing.B) {
	s := interactiveTestSession(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interactiveTestEntry(b, s, "<entry>", "(nop ())")
	}
}

func drainInteractiveProcesses(t testing.TB, s *Session) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		s.Program.Tick(1)
		if len(s.Program.Eval.Processes) == 0 && len(s.Program.Pending) == 0 {
			return
		}
	}
	t.Fatalf("retained %d tasks and %d host requests", len(s.Program.Eval.Processes), len(s.Program.Pending))
}

func TestInteractiveAsyncRetirementAndSavedHandles(t *testing.T) {
	s := interactiveTestSession(t)
	p := s.Program
	p.Options.Grants = []eval.Grant{{Capability: eval.Run}}
	p.Options.ResolveTool = posix.ResolveTool
	var handle *eval.ProcessHandle
	p.Eval.ProcessStarted = func(_ int64, h *eval.ProcessHandle) { handle = h }
	for i := 0; i < 128; i++ {
		interactiveTestEntry(t, s, "<start>", `let job = (run! :async true "/bin/true")`)
		drainInteractiveProcesses(t, s)
		if handle == nil || handle.Root() != nil || handle.State() != core.NodeComplete {
			t.Fatal("completed handle did not retain its terminal snapshot")
		}
		for _, node := range p.Engine.TrackedNodes() {
			if strings.HasPrefix(node.Key.Name, "<process:") || strings.HasPrefix(node.Key.Name, "<statement:") {
				t.Fatalf("retained execution history: %s", node.Key.Name)
			}
		}
		if len(p.Eval.OperationStates) != 0 {
			t.Fatalf("retained %d operations", len(p.Eval.OperationStates))
		}
		interactiveTestEntry(t, s, "<await>", `(if (ne? (get "status" (await job)) 0) then (fail! "lost result") else nil)`)
		interactiveTestEntry(t, s, "<await-again>", `(await job)`)
	}
}

func TestInteractiveAsyncFailureRetainedUntilObserved(t *testing.T) {
	s := interactiveTestSession(t)
	p := s.Program
	p.Options.Grants = []eval.Grant{{Capability: eval.Run}}
	p.Options.ResolveTool = posix.ResolveTool
	var handle *eval.ProcessHandle
	p.Eval.ProcessStarted = func(_ int64, h *eval.ProcessHandle) { handle = h }
	interactiveTestEntry(t, s, "<failure>", `let job = (run! :async true "/bin/false")`)
	for i := 0; i < 2000 && handle.State() != core.NodeFailed; i++ {
		p.Tick(1)
	}
	p.CollectProcesses()
	if handle.State() != core.NodeFailed || handle.Observed() || len(p.Eval.Processes) != 1 {
		t.Fatal("unreported failure was lost before its owning join")
	}
	interactiveTestEntry(t, s, "<handled>", `let handled = (await job) ?? nil`)
	drainInteractiveProcesses(t, s)
	if !handle.Observed() || handle.Diagnostic().Code == "" {
		t.Fatal("observed failure lost its saved diagnostic")
	}
	interactiveTestEntry(t, s, "<repeat>", `let handled = (await job) ?? nil`)
	h := s.Start(len(s.Work)).Handle
	if h == nil {
		t.Fatal("cannot start invocation join")
	}
	defer h.Free()
	for i := 0; i < 100; i++ {
		p.Tick(0)
		result := h.Poll()
		if result.Done {
			defer result.Result.Free(mem.System)
			if result.Result.Diagnostic.Code != "" {
				t.Fatalf("join repeated handled failure: %+v", result.Result.Diagnostic)
			}
			return
		}
	}
	t.Fatal("join did not finish")
}

func TestInteractiveCancellationWaitsForHostReaping(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%t", async), func(t *testing.T) {
			s := interactiveTestSession(t)
			p := s.Program
			p.Options.Grants = []eval.Grant{{Capability: eval.Run}}
			p.Options.ResolveTool = posix.ResolveTool
			var process *eval.ProcessHandle
			p.Eval.ProcessStarted = func(_ int64, h *eval.ProcessHandle) { process = h }
			text := `(run! "/bin/sleep" "30")`
			if async {
				text = `let job = (run! :async true "/bin/sleep" "30")`
			}
			first, diagnostics := s.AppendKash("<cancel>", text)
			if len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			h := s.Start(first).Handle
			if h == nil {
				t.Fatal("cannot start sleep")
			}
			for i := 0; i < 1000 && len(p.Pending) == 0; i++ {
				p.Tick(0)
			}
			if len(p.Pending) == 0 {
				t.Fatal("process request was not submitted")
			}
			nodeID := h.Node.ID
			if async {
				nodeID = process.Root().Node().ID
				p.Eval.CancelProcess(process)
			} else {
				h.Cancel()
			}
			h.Free()
			s.DiscardWork(first, len(s.Work))
			p.CollectProcesses()
			if len(p.Pending) == 0 || p.Engine.LookupID(nodeID) == nil {
				t.Fatal("cancelled execution retired before the host acknowledged reaping")
			}
			drainInteractiveProcesses(t, s)
			if p.Engine.LookupID(nodeID) != nil || len(p.Eval.OperationStates) != 0 {
				t.Fatal("reaped cancellation retained execution state")
			}
		})
	}
}

func TestInteractiveInvocationJoinReportsAsyncFailureOnce(t *testing.T) {
	s := interactiveTestSession(t)
	p := s.Program
	p.Options.Grants = []eval.Grant{{Capability: eval.Run}}
	p.Options.ResolveTool = posix.ResolveTool
	interactiveTestEntry(t, s, "<failure>", `let job = (run! :async true "/bin/false")`)
	for _, wantFailure := range []bool{true, false} {
		h := s.Start(len(s.Work)).Handle
		if h == nil {
			t.Fatal("cannot start invocation join")
		}
		done := false
		for i := 0; i < 2000; i++ {
			p.Tick(1)
			result := h.Poll()
			if result.Done {
				if (result.Result.Diagnostic.Code != "") != wantFailure {
					t.Fatalf("join failure=%t: %+v", wantFailure, result.Result.Diagnostic)
				}
				result.Result.Free(mem.System)
				done = true
				break
			}
		}
		h.Free()
		if !done {
			t.Fatal("invocation join did not finish")
		}
	}
	drainInteractiveProcesses(t, s)
}

func BenchmarkInteractiveAsync(b *testing.B) {
	s := interactiveTestSession(b)
	s.Program.Options.Grants = []eval.Grant{{Capability: eval.Run}}
	s.Program.Options.ResolveTool = posix.ResolveTool
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interactiveTestEntry(b, s, "<async>", `let job = (run! :async true "/bin/true")`)
		drainInteractiveProcesses(b, s)
	}
}

func TestInteractiveAsyncKeepsReboundCallableScope(t *testing.T) {
	s := interactiveTestSession(t)
	p := s.Program
	p.Options.Grants = []eval.Grant{{Capability: eval.Run}}
	p.Options.ResolveTool = posix.ResolveTool
	var out strings.Builder
	p.Eval.SetDefinitionEffectSink(func(_ any, effect eval.Effect) { out.Write(effect.Data) }, nil)
	interactiveTestEntry(t, s, "<old>", "def worker duration\n\tlocal = \"old-scope\"\n\tdefer (out! local)\n\t/bin/sleep $duration &\n")
	old := p.Eval.Scope.Bindings[0].Function.Definition
	first, diagnostics := s.AppendKash("<call>", `(worker "0.02")`)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	h := s.Start(first).Handle
	if h == nil {
		t.Fatal("cannot start worker")
	}
	for i := 0; i < 1000 && len(p.Pending) == 0; i++ {
		p.Tick(0)
	}
	if len(p.Eval.Processes) != 1 || len(p.Pending) == 0 {
		t.Fatal("worker did not start its async child")
	}
	interactiveTestEntry(t, s, "<new>", "def worker duration\n\treturn 0\n")
	if old.Expression == nil {
		t.Fatal("rebinding released code still borrowed by async work")
	}
	done := false
	for i := 0; i < 2000; i++ {
		p.Tick(1)
		result := h.Poll()
		if result.Done {
			if result.Result.Diagnostic.Code != "" {
				t.Fatal(result.Result.Diagnostic)
			}
			result.Result.Free(mem.System)
			done = true
			break
		}
	}
	h.Free()
	s.DiscardWork(first, len(s.Work))
	if !done || out.String() != "old-scope" {
		t.Fatalf("async join or captured cleanup failed: done=%t out=%q", done, out.String())
	}
	drainInteractiveProcesses(t, s)
	if old.Expression != nil || len(p.Eval.OperationStates) != 0 {
		t.Fatal("completed async call retained the old callable scope")
	}
}

func TestInteractiveNestedAsyncStarts(t *testing.T) {
	s := interactiveTestSession(t)
	p := s.Program
	p.Options.Grants = []eval.Grant{{Capability: eval.Run}}
	p.Options.ResolveTool = posix.ResolveTool
	p.Eval.Terminal = true
	var handles []*eval.ProcessHandle
	p.Eval.ProcessStarted = func(_ int64, h *eval.ProcessHandle) { handles = append(handles, h) }
	interactiveTestEntry(t, s, "<nested>", `let job = (run! :async true "/bin/sleep" (if (run! :async true "/bin/false") then "0.01" else "0.01"))`)
	if len(handles) != 2 {
		t.Fatalf("started %d processes", len(handles))
	}
	for i := 0; i < 2000 && (handles[0].State() != core.NodeFailed || handles[1].State() != core.NodeComplete); i++ {
		p.Tick(1)
	}
	if handles[0].State() != core.NodeFailed || handles[1].State() != core.NodeComplete {
		t.Fatal("nested async processes did not complete independently")
	}
	for _, h := range handles {
		p.Eval.ObserveProcess(h)
	}
	drainInteractiveProcesses(t, s)
}

func BenchmarkInteractiveRebinding(b *testing.B) {
	for _, entry := range []struct{ name, text string }{
		{"let", "let value = \"value\""},
		{"def", "def helper value\n\treturn 0\n"},
	} {
		b.Run(entry.name, func(b *testing.B) {
			s := interactiveTestSession(b)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				interactiveTestEntry(b, s, "<entry>", entry.text)
			}
		})
	}
}
