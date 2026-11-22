package wasm

import (
	"encoding/json"
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"strings"
	"testing"
)

// These are ABI wire numbers, independent of the engine's internal enum.
func TestABIRequestKindWireNumbers(t *testing.T) {
	cases := []struct {
		kind host.RequestKind
		wire uint32
	}{
		{host.RequestReadFile, 1}, {host.RequestWriteFile, 2}, {host.RequestProcess, 3}, {host.RequestEnvironment, 4},
		{host.RequestStatPath, 5}, {host.RequestExpandGlob, 6}, {host.RequestWallTime, 8}, {host.RequestMonotonicTime, 9},
		{host.RequestCacheGet, 10}, {host.RequestCachePut, 11}, {host.RequestCacheDelete, 12}, {host.RequestPrepareOutputs, 21},
		{host.RequestTimer, 22}, {host.RequestProcessCancel, 23}, {host.RequestCacheLock, 24}, {host.RequestCacheUnlock, 25},
		{host.RequestPlugin, 26}, {host.RequestPublishFile, 39}, {host.RequestRecordRead, 40}, {host.RequestRecordClose, 41}, {host.RequestLoadSource, 42},
		{host.RequestProcessSignal, 44}, {host.RequestProcessStageIO, 46},
	}
	for _, c := range cases {
		if got := ABIRequestKind(host.Request{Kind: c.kind}); got != c.wire {
			t.Errorf("engine kind %d: wire number %d, want %d", c.kind, got, c.wire)
		}
	}
}

func TestForwardedRegistrationUsesImportedRuleConstructor(t *testing.T) {
	a := mem.System
	start := NewRuntime(a, "")
	if start.Runtime == nil {
		t.Fatalf("runtime could not initialize: %v", start.Result)
	}
	r := start.Runtime
	defer r.Free()
	r.SetForwarding(true)
	r.SetDirectory("/project")
	const main = `from ./lib/factory.km import create
generate (create "item")
`
	descriptor, _ := json.Marshal(map[string]any{"sources": []map[string]string{{"name": "/project/main.km", "sourceDirectory": "/project", "text": main}}})
	if result := r.SetBuildSources(descriptor); result.Code != "" {
		t.Fatalf("invalid build sources: %v", result)
	}
	for _, fixture := range []struct{ path, text string }{
		{"/project/lib/factory.km", `from ./values.km import label
export create
stem = "result-"
def create item =
  rule ./${stem}${item}.txt = str label ":" item
`},
		{"/project/lib/values.km", "export label\nlabel = \"private\"\n"},
	} {
		if result := r.Prepare(); !result.HostNeeded || r.Program != nil {
			t.Fatalf("registration did not suspend before %s: %v", fixture.path, result)
		}
		next := r.Step()
		if !next.OK || ABIRequestKind(next.Request) != 42 || ABIRequestPayload(next.Request) != fixture.path {
			t.Fatalf("unexpected source request: %v", next)
		}
		r.Complete(next.Request, core.NewBytes(a, []byte(fixture.text)), core.Diagnostic{})
		next.Request.Free(a)
	}
	if result := r.Prepare(); result.HostNeeded || result.Code != "" || r.Program == nil || !r.Program.HasTarget("./result-item.txt") || r.Step().OK {
		t.Fatalf("imported constructor did not register its computed output: %v", result)
	}
	snapshots := r.DeclarationStateJSON()
	defer snapshots.Free(a)
	if strings.Count(snapshots.Text, `"kind":"declaration-source"`) != 2 || !strings.Contains(snapshots.Text, "/project/lib/values.km") {
		t.Fatalf("registration lost transitive source observations: %s", snapshots.Text)
	}
}

func TestForwardedRegistrationLoadsOnlySelectedDeclarationIncludes(t *testing.T) {
	a := mem.System
	start := NewRuntime(a, "")
	if start.Runtime == nil {
		t.Fatalf("runtime could not initialize: %v", start.Result)
	}
	r := start.Runtime
	defer r.Free()
	r.SetForwarding(true)
	r.SetDirectory("/project")
	const main = `for variant in ["debug", "release"]
  if variant == "inactive"
    include ./main.km
  include ./rules/${variant}.km
  include? ./local-${variant}.km
  rule ./${variant}-after.txt = label
`
	descriptor, _ := json.Marshal(map[string]any{"sources": []map[string]string{{"name": "/project/main.km", "sourceDirectory": "/project", "text": main}}})
	if result := r.SetBuildSources(descriptor); result.Code != "" {
		t.Fatalf("invalid build sources: %v", result)
	}
	for _, fixture := range []struct{ path, text string }{
		{"/project/rules/debug.km", "label = variant\ninclude ./nested.km\n"},
		{"/project/rules/nested.km", "rule ./${variant}.txt = label\n"},
		{"/project/local-debug.km", ""},
		{"/project/rules/release.km", "label = variant\ninclude ./nested.km\n"},
		{"/project/local-release.km", ""},
	} {
		if result := r.Prepare(); !result.HostNeeded || r.Program != nil {
			t.Fatalf("registration did not suspend before %s: %v", fixture.path, result)
		}
		next := r.Step()
		if !next.OK || ABIRequestKind(next.Request) != 42 || ABIRequestPayload(next.Request) != fixture.path {
			t.Fatalf("unexpected source request: %v", next)
		}
		value := core.Value{Kind: core.Nil}
		if fixture.text != "" {
			value = core.NewBytes(a, []byte(fixture.text))
		}
		r.Complete(next.Request, value, core.Diagnostic{})
		next.Request.Free(a)
	}
	if result := r.Prepare(); result.HostNeeded || result.Code != "" || r.Program == nil || r.Step().OK {
		t.Fatalf("included declarations could not register: %v", result)
	}
	for _, name := range []string{"./debug.txt", "./debug-after.txt", "./release.txt", "./release-after.txt"} {
		if !r.Program.HasTarget(name) {
			t.Errorf("missing included or following target %s", name)
		}
	}
	snapshots := r.DeclarationStateJSON()
	defer snapshots.Free(a)
	if strings.Count(snapshots.Text, `"kind":"declaration-source"`) != 5 || !strings.Contains(snapshots.Text, `"signature":"missing"`) {
		t.Fatalf("registration lost selected source observations: %s", snapshots.Text)
	}
}

func TestABIProcessSignalPayload(t *testing.T) {
	payload := host.ProcessSignalPayload(nil, 9007199254740993, "SIGTERM")
	if got := ABIRequestPayload(host.Request{Kind: host.RequestProcessSignal, Payload: payload}); got != "9007199254740993:SIGTERM" {
		t.Fatalf("process signal payload = %q", got)
	}
	payload.Free(nil)
}
func TestABIReadRequestPromotions(t *testing.T) {
	cases := map[string]uint32{"resolve-tool": 18, "tool-exists": 19, "output-exists": 17, "file-times": 20, "file-content": 27, "tool-content": 28, "output-content": 29, "exists": 7, "stat": 5, "lstat": 30, "readlink": 31, "wildcard": 6}
	cases[host.OpDirectoryCheck] = 43
	cases[host.OpDeclarationFiles] = 45
	for op, wire := range cases {
		r := host.Request{Kind: host.RequestReadFile, Payload: core.Value{Kind: core.Record, Record: []core.RecordField{{Key: "op", Value: core.Value{Kind: core.String, Text: op}}}}}
		if got := ABIRequestKind(r); got != wire {
			t.Errorf("%s: wire number %d, want %d", op, got, wire)
		}
	}
}

func TestABICommandGlobKeepsPlainScanPayload(t *testing.T) {
	const scan = "/project/**/*.txt"
	a := mem.System
	key := host.CommandDiscoveryKey(a, scan, "/project")
	defer mem.FreeString(a, key)
	payload := host.FilePayload(a, host.OpWildcard, key)
	defer payload.Free(a)
	request := host.Request{Kind: host.RequestReadFile, Payload: payload}
	if kind, path := ABIRequestKind(request), ABIRequestPayload(request); kind != 48 || path != scan {
		t.Fatalf("command glob request = %d %q, want 48 %q", kind, path, scan)
	}
}

func TestForwardedDeclarationSnapshotsReuseScansAndRetainFailure(t *testing.T) {
	a := mem.System
	started := NewRuntime(a, `if true
  let first = files ./items/{x:*}-{x}.txt
  let same-scan = files ./items/{left:*}-{right}.txt
  let second = files ./other/*.txt
  if (count first) == 1
    if (count same-scan) == 2
      if (count second) == 0
        rule ./items/{name:*}-absent.txt = "overlap"
`)
	if started.Runtime == nil {
		t.Fatalf("source failed to parse: %v", started.Result)
	}
	r := started.Runtime
	defer r.Free()
	r.SetForwarding(true)
	r.SetDirectory("/project")
	for i, name := range []string{"/project/items/*-*.txt", "/project/other/*.txt"} {
		if result := r.Prepare(); !result.HostNeeded || r.Program != nil {
			t.Fatalf("registration did not suspend before %s: %v", name, result)
		}
		next := r.Step()
		if !next.OK || ABIRequestKind(next.Request) != 45 || ABIRequestPayload(next.Request) != name {
			t.Fatalf("unexpected declaration request: %v", next)
		}
		files := core.NewList(a, nil)
		if i == 0 {
			files = core.NewList(a, []core.Value{core.NewString(a, "/project/items/yes-yes.txt"), core.NewString(a, "/project/items/left-right.txt")})
		}
		r.Complete(next.Request, files, core.Diagnostic{})
		next.Request.Free(a)
	}
	result := r.Prepare()
	defer result.Free(a)
	if result.HostNeeded || r.Program != nil || result.Code != "DISCOVERY_OVERLAP" || r.Step().OK {
		t.Fatalf("expected overlap after two scans: %v", result)
	}
	state := r.DeclarationStateJSON()
	defer state.Free(a)
	if strings.Count(state.Text, `"kind":"discovery"`) != 3 || !strings.Contains(state.Text, "./other/*.txt") {
		t.Fatalf("failed registration lost its observations: %s", state.Text)
	}
}
