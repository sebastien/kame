package programcheck

import (
	"bytes"
	"kame/host/wasm"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"strings"
)

func TestListTargetsCatalogueAndMatching(t *testing.T) {
	a := t.Allocator()
	parsed := script.ParseKameFile(a, "list.km", "ROOT = \"./build\"\nVALUE = 42\nrule ${ROOT}/{name}.o : ./src/{name}.c\nrule ./build/app ./build/app.map : ./missing-input\n\ttouch forbidden\ntask build : missing-target\ntask builder\ntask rebuild\nservice daemon :\ntask test {mode} :\nrule ./café\ngenerate (task generated)\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil {
		for i := range compiled.Diagnostics {
			t.Error(compiled.Diagnostics[i].Message)
		}
		compiled.Free(a)
		parsed.Free()
		registry.Free()
		return
	}
	queries := []string{"", "build", "biuld", "buld", "buuild", "builx", "buidlre", "apl", "build/apq", "cafe", "cafè", "completely-unrelated", "VALUE"}
	expected := []string{
		`["./build/app","./build/app.map","./build/{name}.o","./café","build","builder","daemon","generated","rebuild","test"]`,
		`["build","./build/app","./build/app.map","./build/{name}.o","builder","rebuild"]`,
		`["build"]`, `["build"]`, `["build"]`, `["build"]`, `["builder"]`,
		`["./build/app"]`, `["./build/app"]`, `["./café"]`, `["./café"]`, `[]`, `[]`,
	}
	for i := range queries {
		var buffer = bytes.Buffer{}
		compiled.Program.WriteTargetList(&buffer, queries[i])
		if buffer.String() != expected[i] {
			t.Error("unexpected list for '" + queries[i] + "': " + buffer.String())
		}
		buffer.Reset()
	}
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestListReportClassifiesWithoutPlanningPrerequisites(t *testing.T) {
	a := t.Allocator()
	parsed := script.ParseKameFile(a, "list.km", "task build : unknown ; {summary: \"Build project\", custom: \"kept\"}\nservice daemon : ; {summary: \"Run daemon\", kind: \"service\"}\ntask cached cache-check {mode} :\ntask cached out :\nrule ./out : ${out! \"forbidden\"}\nrule ./obj/{name}.o : ./src/{name}.c\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	defer registry.Free()
	defer parsed.Free()
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Error("compile failed")
		return
	}
	defer compiled.Program.Free()
	var first = bytes.Buffer{}
	var second = bytes.Buffer{}
	defer first.Reset()
	defer second.Reset()
	compiled.Program.WriteTargetReport(&first, "")
	compiled.Program.WriteTargetReport(&second, "")
	if first.String() != second.String() || compiled.Program.InspectionWaiting {
		t.Error("report was not repeat-stable")
	}
	expected := []string{
		`{"name":"build","group":"rules","summary":"Build project","status":""}`,
		`{"name":"daemon","group":"services","summary":"Run daemon","status":""}`,
		`{"name":"cache-check","group":"tasks","status":"unknown"}`,
		`{"name":"out","group":"tasks","status":"unknown"}`,
		`{"name":"./out","group":"artifacts","status":"unknown"}`,
		`{"name":"./obj/{name}.o","group":"artifacts","status":""}`,
	}
	for i := range expected {
		if !strings.Contains(first.String(), expected[i]) {
			t.Error("missing catalogue entry: " + expected[i])
		}
	}
	if strings.Contains(first.String(), "forbidden") {
		t.Error("prerequisite expression was evaluated")
	}
}

func TestListReportRefreshesAcceptedTaskAndArtifactProofs(t *testing.T) {
	a := t.Allocator()
	host := wasm.NewMemoryHost(a)
	host.SetFile("seed", []byte("one"))
	parsed := script.ParseKameFile(a, "list.km", "task cached checked : ./seed ; {shell: kash}\n\t(out! (text (read ./seed)))\nrule ./result : ./seed = read ./seed\nrule ./downstream : ./result = read ./result\nrule ./missing : ./seed\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: host, Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}}})
	defer registry.Free()
	defer parsed.Free()
	defer compiled.Free(a)
	if compiled.Program == nil {
		host.Free()
		t.Error("compile failed")
		return
	}
	defer compiled.Program.Free()
	targets := []string{"checked", "./downstream"}
	for i := range targets {
		result := compiled.Program.Materialize(targets[i])
		if result.Diagnostic.Code != "" {
			t.Error("materialization failed: " + result.Diagnostic.Code)
		}
		result.Free(a)
	}
	var buffer = bytes.Buffer{}
	defer buffer.Reset()
	compiled.Program.WriteTargetReport(&buffer, "")
	initial := []string{`"name":"checked","group":"tasks","status":"built"`, `"name":"./downstream","group":"artifacts","status":"built"`}
	for i := range initial {
		if !strings.Contains(buffer.String(), initial[i]) {
			t.Error("accepted proof did not establish status: " + buffer.String())
		}
	}
	host.SetFile("seed", []byte("two"))
	buffer.Reset()
	buffer = bytes.Buffer{}
	compiled.Program.WriteTargetReport(&buffer, "")
	expected := []string{`"name":"checked","group":"tasks","status":"outdated"`, `"name":"./result","group":"artifacts","status":"outdated"`, `"name":"./downstream","group":"artifacts","status":"unknown"`, `"name":"./missing","group":"artifacts","status":"missing"`}
	for i := range expected {
		if !strings.Contains(buffer.String(), expected[i]) {
			t.Error("status did not refresh: " + expected[i] + " in " + buffer.String())
		}
	}
}

func TestListReportDisabledTaskCacheDoesNotRequestHostProof(t *testing.T) {
	a := t.Allocator()
	host := wasm.NewMemoryHost(a)
	host.SetFile("seed", []byte("one"))
	parsed := script.ParseKameFile(a, "list.km", "task cached checked : ./seed ; {shell: kash}\n\t(out! \"ok\")\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: host, Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}, {Capability: eval.Write}}})
	defer registry.Free()
	defer parsed.Free()
	defer compiled.Free(a)
	if compiled.Program == nil {
		host.Free()
		t.Error("compile failed")
		return
	}
	defer compiled.Program.Free()
	initial := bytes.Buffer{}
	compiled.Program.WriteTargetReport(&initial, "")
	if !strings.Contains(initial.String(), `"name":"checked","group":"tasks","status":"missing"`) {
		t.Error("missing cache record was not reported as missing: " + initial.String())
	}
	initial.Reset()
	result := compiled.Program.Materialize("checked")
	if result.Diagnostic.Code != "" {
		t.Error("materialization failed: " + result.Diagnostic.Code)
	}
	result.Free(a)
	var buffer = bytes.Buffer{}
	defer buffer.Reset()
	compiled.Program.WriteTargetReport(&buffer, "")
	if !strings.Contains(buffer.String(), `"status":"built"`) {
		t.Error("accepted task proof missing")
	}
	buffer.Reset()
	buffer = bytes.Buffer{}
	compiled.Program.Options.CacheDisabled = true
	compiled.Program.Forwarding = true
	compiled.Program.WriteTargetReport(&buffer, "")
	if compiled.Program.InspectionWaiting || !strings.Contains(buffer.String(), `"name":"checked","group":"tasks","status":"outdated"`) {
		t.Error("disabled task cache must report outdated without host requests: " + buffer.String())
	}
}

func TestListTargetsDeduplicatesPatternOutputs(t *testing.T) {
	a := t.Allocator()
	parsed := script.ParseKameFile(a, "list.km", "rule ./build/{name}.o : ./src/{name}.c\nrule ./build/{name}.o : ./other/{name}.c\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{})
	if compiled.Program == nil {
		t.Error("compile failed")
	} else {
		var buffer = bytes.Buffer{}
		compiled.Program.WriteTargetList(&buffer, "")
		if buffer.String() != `["./build/{name}.o"]` {
			t.Error("duplicate output was listed")
		}
		buffer.Reset()
		compiled.Program.Free()
	}
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}
