package posix_test

import (
	"kame/host"
	"kame/host/posix"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
	"solod.dev/so/time"
)

// waitBudget bounds every test wait. It is deliberately generous so a loaded
// machine slows the suite down instead of failing it.
const waitBudget = 5 * time.Second

// saturationBudget is the short window the backpressure test spends filling a
// child's output queue before it starts consuming events.
const saturationBudget = 100 * time.Millisecond

func request(a mem.Allocator, id int64, script string) posix.Request {
	return posix.Request{ID: id, Shell: slices.Clone(a, []string{"/bin/sh", "-c"}), Script: slices.Clone(a, []byte(script)), Directory: ".", Environment: slices.Clone(a, []string{"PATH=/usr/bin:/bin"}), RetainBytes: 1024}
}

func freeRequest(a mem.Allocator, r *posix.Request) {
	slices.Free(a, r.Shell); slices.Free(a, r.Script); slices.Free(a, r.Environment)
	*r = posix.Request{}
}

func drain(t *testing.T, h *posix.Host, terminal *posix.Event) []posix.Event {
	var events []posix.Event
	deadline := time.Now().Add(waitBudget)
	for terminal.Kind != posix.Terminal && time.Now().Before(deadline) {
		if !h.Pump(10) { t.Error("host pump failed"); break }
		for {
			result := h.Next()
			if !result.OK { break }
			events = slices.Append(t.Allocator(), events, result.Event)
			if result.Event.Kind == posix.Terminal { *terminal = result.Event }
		}
	}
	if terminal.Kind != posix.Terminal { t.Error("timed out waiting for terminal event") }
	return events
}

func freeEvents(a mem.Allocator, events []posix.Event) {
	for i := range events { events[i].Free(a) }
	slices.Free(a, events)
}

func TestPipelineRetainsEveryStageStatus(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	stages := []host.ProcessStage{{Argv: []string{"/bin/sh", "-c", "printf first >&2; exit 7"}}, {Argv: []string{"/bin/sh", "-c", "cat; printf second >&2; exit 9"}}, {Argv: []string{"/bin/cat"}}}
	r := host.ProcessRequest{ID: 71, Stages: stages, Directory: ".", Environment: []string{"PATH=/bin:/usr/bin"}, RetainBytes: 1024}
	if !h.Start(r) { t.Fatal("pipeline start failed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Outcome != posix.Exited || terminal.Status != 9 || len(terminal.Stages) != 3 { t.Error("pipeline lost aggregate status or stage results")
	} else if terminal.Stages[0].Status != 7 || terminal.Stages[1].Status != 9 || terminal.Stages[2].Status != 0 { t.Error("pipeline did not retain every stage's status") }
	if len(terminal.Stdout) != 0 || len(terminal.Stderr) != len("firstsecond") { t.Error("pipeline streams were not routed correctly") }
	freeEvents(a, events)
	h.Free()
}

func TestPipelineStreamsBeyondCaptureLimit(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	stages := []host.ProcessStage{{Argv: []string{"/usr/bin/head", "-c", "4194304", "/dev/zero"}}, {Argv: []string{"/bin/sh", "-c", "wc -c | tr -d ' '"}}}
	r := host.ProcessRequest{ID: 72, Stages: stages, Directory: ".", Environment: []string{"PATH=/bin:/usr/bin"}, RetainBytes: 16}
	if !h.Start(r) { t.Fatal("pipeline start failed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Outcome != posix.Exited || terminal.Status != 0 || terminal.StdoutTruncated || string(terminal.Stdout) != "4194304\n" { t.Error("pipeline buffered or captured intermediate stdout") }
	freeEvents(a, events)
	h.Free()
}

func TestPipelineTimeoutAndLaunchFailureReapAllStages(t *testing.T) {
	a := t.Allocator()
	for i := 0; i < 2; i++ {
		h := posix.New(a)
		second := "/bin/cat"
		if i == 1 { second = "/no-such-kash-pipeline-executable" }
		stages := []host.ProcessStage{{Argv: []string{"/bin/sh", "-c", "sleep 30"}}, {Argv: []string{second}}}
		r := host.ProcessRequest{ID: 73, Stages: stages, Directory: ".", Environment: []string{"PATH=/bin:/usr/bin"}, TimeoutMS: 50, RetainBytes: 16}
		if !h.Start(r) { t.Fatal("pipeline fork failed"); h.Free(); return }
		var terminal posix.Event
		events := drain(t, h, &terminal)
		if i == 0 && terminal.Outcome != posix.TimedOut { t.Error("graph timeout was not propagated") }
		if i == 1 && terminal.Outcome != posix.Failed { t.Error("stage launch failure was not propagated") }
		if h.Active() != 0 || len(terminal.Stages) != 2 { t.Error("graph was not completely retired") }
		freeEvents(a, events)
		h.Free()
	}
}

func TestEnvironmentSnapshotIsCompleteAndOwned(t *testing.T) {
	a := t.Allocator()
	environment := posix.Environment(a)
	defer posix.FreeEnvironment(a, environment)
	foundPath := false
	for i := range environment { if len(environment[i]) >= 5 && environment[i][:5] == "PATH=" { foundPath = true; break } }
	if !foundPath { t.Error("environment snapshot does not include PATH") }
}

func TestStreamsStatusAndRetention(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 1, "printf out; printf err >&2; exit 7")
	if !h.Start(r) { t.Fatal("start failed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	var out, err []byte
	for i := range events {
		if events[i].Kind == posix.Stdout { for j := range events[i].Data { out = slices.Append(a, out, events[i].Data[j]) } }
		if events[i].Kind == posix.Stderr { for j := range events[i].Data { err = slices.Append(a, err, events[i].Data[j]) } }
	}
	if string(out) != "out" || string(err) != "err" { t.Error("streams were corrupted") }
	if terminal.Outcome != posix.Exited || terminal.Status != 7 || string(terminal.Stdout) != "out" || string(terminal.Stderr) != "err" || terminal.RetainBytes != 1024 { t.Error("terminal result lost status, retention bound, or retained logs") }
	if len(events) == 0 || events[len(events)-1].Kind != posix.Terminal { t.Error("terminal arrived before output was drained") }
	freeEvents(a, events)
	slices.Free(a, out); slices.Free(a, err)
	freeRequest(a, &r)
	h.Free()
}

func TestOneShellEnvironmentAndDirectory(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 2, "value=ok\nprintf '%s:%s\\n' \"$value\" \"$KM_TEST\"\ncd /tmp\npwd")
	slices.Free(a, r.Environment)
	r.Environment = slices.Clone(a, []string{"PATH=/usr/bin:/bin", "KM_TEST=env"})
	if !h.Start(r) { t.Fatal("start failed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Status != 0 || string(terminal.Stdout) != "ok:env\n/tmp\n" { t.Error("script was not one shell with its explicit environment") }
	freeEvents(a, events)
	freeRequest(a, &r)
	h.Free()
}

func TestMissingShellIsOnlyFailedTerminal(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 3, "true")
	slices.Free(a, r.Shell)
	r.Shell = slices.Clone(a, []string{"/missing/kame-shell", "-c"})
	if !h.Start(r) { t.Error("missing shell was not submitted") }
	if h.Active() != 0 { t.Error("failed exec was exposed as an active process") }
	h.Pump(10)
	result := h.Next()
	if !result.OK || result.Event.Kind != posix.Terminal || result.Event.Outcome != posix.Failed || result.Event.Diagnostic.Code != posix.DiagnosticHostFailure || result.Event.Diagnostic.Message == "" { t.Error("missing shell did not produce failed terminal") }
	if result.OK { result.Event.Free(a) }
	if h.Next().OK { t.Error("spawn failure emitted extra event") }
	freeRequest(a, &r)
	h.Free()
}

func TestRejectedRequestIsOnlyFailedTerminal(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 0, "true")
	if h.Start(r) { t.Error("request with ID 0 was accepted") }
	result := h.Next()
	if !result.OK || result.Event.Kind != posix.Terminal || result.Event.Outcome != posix.Failed || result.Event.Diagnostic.Code != posix.DiagnosticHostFailure || result.Event.Diagnostic.Message != "invalid request" { t.Error("rejected request did not produce one failed terminal") }
	if result.OK { result.Event.Free(a) }
	if h.Next().OK { t.Error("rejected request emitted extra event") }
	freeRequest(a, &r)
	h.Free()
}

func TestDuplicateRequestIDIsRejected(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 500, "sleep 5 & wait")
	if !h.Start(r) { t.Fatal("start failed"); return }
	if h.Start(r) { t.Error("duplicate request ID was accepted") }
	result := h.Next()
	if !result.OK || result.Event.Kind != posix.Terminal || result.Event.Outcome != posix.Failed || result.Event.Diagnostic.Message != "request ID already active" { t.Error("duplicate request did not report an already active ID") }
	if result.OK { result.Event.Free(a) }
	h.CancelAll()
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Outcome != posix.Cancelled { t.Error("original process was not cancelled") }
	freeEvents(a, events)
	freeRequest(a, &r)
	h.Free()
}

func TestDynamicRequestStringsAndNULScript(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 30, "printf %s \"$KM_TEST\"")
	// Concatenation keeps these strings dynamically built, so the environment
	// is cloned at run time instead of folded into a static literal.
	slices.Free(a, r.Environment)
	key, value := "KM_"+"TEST", "en"+"v"
	r.Environment = slices.Clone(a, []string{"PATH=/usr/bin:/bin", key + "=" + value})
	if !h.Start(r) { t.Fatal("dynamic request strings failed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Status != 0 || string(terminal.Stdout) != "env" { t.Error("dynamic request strings were not passed exactly") }
	freeEvents(a, events)
	freeRequest(a, &r)
	r = request(a, 31, "true")
	slices.Free(a, r.Script)
	r.Script = slices.Clone(a, []byte{'t', 'r', 'u', 'e', 0, 'f', 'a', 'l', 's', 'e'})
	if h.Start(r) { t.Error("NUL-containing script was accepted") }
	result := h.Next()
	if !result.OK || result.Event.Kind != posix.Terminal || result.Event.Outcome != posix.Failed { t.Error("NUL-containing script did not produce a failed terminal") }
	if result.OK { result.Event.Free(a) }
	freeRequest(a, &r)
	h.Free()
}

func TestEmptyScript(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 32, "")
	if !h.Start(r) { t.Fatal("empty script start failed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Outcome != posix.Exited || terminal.Status != 0 { t.Error("empty script did not exit successfully") }
	freeEvents(a, events)
	freeRequest(a, &r)
	h.Free()
}

func TestCancellationAndTimeout(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 4, "sleep 5 & wait")
	if !h.Start(r) { t.Fatal("start failed"); return }
	if !h.Cancel(4) || !h.Cancel(4) { t.Error("cancellation was not idempotent") }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Outcome != posix.Cancelled { t.Error("cancellation did not win terminal outcome") }
	freeEvents(a, events)
	freeRequest(a, &r)
	r = request(a, 5, "trap '' TERM; while :; do :; done")
	r.TimeoutMS = 10
	if !h.Start(r) { t.Fatal("timeout start failed"); return }
	terminal = posix.Event{}
	events = drain(t, h, &terminal)
	if terminal.Outcome != posix.TimedOut || terminal.Signal != 9 || h.Active() != 0 { t.Errorf("timeout did not kill and reap process group: kind=%d outcome=%d status=%d signal=%d active=%d", terminal.Kind, terminal.Outcome, terminal.Status, terminal.Signal, h.Active()) }
	freeEvents(a, events)
	freeRequest(a, &r)
	h.Free()
}

func TestStopHonorsConfiguredGracePeriod(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 41, "trap '' TERM; printf READY; while :; do sleep 1; done")
	if !h.Start(r) { t.Fatal("start failed"); return }
	ready := false
	for attempt := 0; attempt < 100 && !ready; attempt++ {
		if !h.Pump(10) { t.Fatal("host pump failed"); return }
		for {
			result := h.Next()
			if !result.OK { break }
			if result.Event.Kind == posix.Stdout && string(result.Event.Data) == "READY" { ready = true }
			result.Event.Free(a)
		}
	}
	if !ready { t.Fatal("process did not install its TERM handler"); return }
	if !h.Stop(41, 0) { t.Fatal("stop request was rejected"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Outcome != posix.Cancelled || terminal.Signal != 9 { t.Error("zero grace period did not escalate to SIGKILL") }
	freeEvents(a, events)
	freeRequest(a, &r)
	h.Free()
}

func TestRetainedPrefixAndBackpressure(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 6, "printf 12345")
	r.RetainBytes = 3
	if !h.Start(r) { t.Fatal("start failed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if string(terminal.Stdout) != "123" || !terminal.StdoutTruncated { t.Error("retained output was not a truncated byte prefix") }
	freeEvents(a, events)
	freeRequest(a, &r)
	r = request(a, 7, "dd if=/dev/zero bs=300000 count=1 2>/dev/null")
	if !h.Start(r) { t.Fatal("large-output start failed"); return }
	other := request(a, 8, "printf other")
	if !h.Start(other) { t.Fatal("second start failed"); return }
	// Pump without consuming first so the large child fills its output queue;
	// the unrelated child must then make progress through backpressure.
	saturation := time.Now().Add(saturationBudget)
	for time.Now().Before(saturation) { h.Pump(1) }
	var streamed int
	var sawOther bool
	deadline := time.Now().Add(waitBudget)
	for !sawOther && time.Now().Before(deadline) {
		for {
			result := h.Next()
			if !result.OK { break }
			if result.Event.ID == 8 && result.Event.Kind == posix.Stdout { sawOther = true }
			if result.Event.ID == 7 && result.Event.Kind == posix.Stdout {
				streamed += len(result.Event.Data)
				for j := range result.Event.Data { if result.Event.Data[j] != 0 { t.Error("large-output stream was corrupted") } }
			}
			result.Event.Free(a)
		}
		if !sawOther { h.Pump(1) }
	}
	if !sawOther { t.Error("saturated output blocked an unrelated child") }
	terminal = posix.Event{}
	events = drain(t, h, &terminal)
	for i := range events {
		if events[i].ID == 7 && events[i].Kind == posix.Stdout {
			streamed += len(events[i].Data)
			for j := range events[i].Data { if events[i].Data[j] != 0 { t.Error("resumed output stream was corrupted") } }
		}
	}
	if terminal.ID != 7 || terminal.Outcome != posix.Exited || terminal.StdoutTruncated != true { t.Error("saturated process did not resume to a terminal result") }
	if streamed != 300000 { t.Errorf("saturated process dropped output: got %d bytes", streamed) }
	freeEvents(a, events)
	freeRequest(a, &r); freeRequest(a, &other)
	h.Free()
}

func TestClosingOneStreamDoesNotCloseTheOther(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 9, "exec 1>&-; printf stderr >&2")
	if !h.Start(r) { t.Fatal("start failed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Status != 0 || string(terminal.Stderr) != "stderr" { t.Error("stderr was lost after stdout closed") }
	freeEvents(a, events)
	freeRequest(a, &r)
	h.Free()
}

func TestRepeatedSpawnAndCancellation(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	for i := 0; i < 16; i++ {
		r := request(a, int64(100+i), "sleep 5 & wait")
		if !h.Start(r) || !h.Cancel(r.ID) { t.Error("repeated request failed") }
		var terminal posix.Event
		events := drain(t, h, &terminal)
		if terminal.Outcome != posix.Cancelled || h.Active() != 0 { t.Error("repeated cancellation did not reap its process") }
		freeEvents(a, events)
		freeRequest(a, &r)
	}
	h.Free()
}

func TestCancellationTerminatesBackgroundGrandchild(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 200, "sleep 5 & printf %s \"$!\"; wait")
	if !h.Start(r) { t.Fatal("start failed"); return }
	var check posix.Request
	deadline := time.Now().Add(waitBudget)
	for check.ID == 0 && time.Now().Before(deadline) {
		if !h.Pump(10) { t.Fatal("host pump failed"); return }
		for {
			result := h.Next()
			if !result.OK { break }
			if result.Event.Kind == posix.Stdout {
				check = request(a, 201, "kill -0 "+string(result.Event.Data))
				if !h.Cancel(r.ID) { t.Error("failed to cancel process group") }
			}
			result.Event.Free(a)
		}
	}
	if check.ID == 0 { t.Fatal("background child pid was not streamed"); return }
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Outcome != posix.Cancelled { t.Error("parent process was not cancelled") }
	freeEvents(a, events)
	freeRequest(a, &r)
	if !h.Start(check) { t.Fatal("child check did not start"); return }
	terminal = posix.Event{}
	events = drain(t, h, &terminal)
	if terminal.Status == 0 { t.Error("background grandchild survived cancellation") }
	freeEvents(a, events)
	freeRequest(a, &check)
	h.Free()
}

func TestWaitpidFailureEmitsHostFailure(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	r := request(a, 300, "sleep 5")
	if !h.Start(r) { t.Fatal("start failed"); return }
	h.ForceWaitpidFailureForTest()
	var terminal posix.Event
	events := drain(t, h, &terminal)
	if terminal.Kind != posix.Terminal || terminal.Outcome != posix.Failed || terminal.Diagnostic.Code != posix.DiagnosticHostFailure || terminal.Diagnostic.Message != "waitpid failed" { t.Error("waitpid failure did not emit HOST_FAIL terminal") }
	freeEvents(a, events)
	freeRequest(a, &r)
	h.Free()
}

func TestCancelAllTerminatesActiveGroups(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	first := request(a, 400, "sleep 5 & wait")
	second := request(a, 401, "sleep 5 & wait")
	if !h.Start(first) || !h.Start(second) { t.Fatal("start failed"); return }
	h.CancelAll()
	var terminals int
	deadline := time.Now().Add(waitBudget)
	for terminals != 2 && time.Now().Before(deadline) {
		if !h.Pump(10) { t.Fatal("host pump failed"); return }
		for {
			result := h.Next()
			if !result.OK { break }
			if result.Event.Kind == posix.Terminal {
				if result.Event.Outcome != posix.Cancelled { t.Error("cancel-all did not report cancellation") }
				terminals++
			}
			result.Event.Free(a)
		}
	}
	if terminals != 2 || h.Active() != 0 { t.Error("cancel-all left a process active") }
	freeRequest(a, &first); freeRequest(a, &second)
	h.Free()
}
