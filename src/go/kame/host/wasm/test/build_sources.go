package wasm_test

import (
	"kame/host/wasm"
	"kame/lang/source"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func TestBuildSourceDescriptorCopiesBytesAndMapsAuthoredSpans(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	data := slices.Clone(a, []byte(`{"sources":[{"name":"main.kmk","text":"","offset":0},{"name":"child.kmk","text":"task child :\n\t@(out \"ok\")\n","offset":42}]}`))
	defer slices.Free(a, data)
	configured := r.SetBuildSources(data)
	if configured.Code != "" {
		t.Error("configure sources")
	}
	configured.Free(a)
	for i := range data {
		data[i] = 0
	}
	prepared := r.Prepare()
	if prepared.Code != "" {
		t.Error("prepare copied sources")
	}
	prepared.Free(a)
	if r.Program == nil || len(r.Program.Rules) != 1 {
		t.Error("missing included rule")
		return
	}
	location := r.Program.Eval.LocateSource(r.Program.Parsed.Source.Name, source.Span{Start: 1, End: 12})
	if location.Source != "child.kmk" || location.Span.Start != 42 {
		t.Error("authored rule span was lost")
	}
}

func TestBuildSourceCompileFailureKeepsCompleteDiagnosticJSON(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	configured := r.SetBuildSources([]byte(`{"sources":[{"name":"first.kmk","text":"name = 1\n","offset":0},{"name":"second.kmk","text":"name = 2\n","offset":17}]}`))
	if configured.Code != "" {
		t.Error("configure sources")
	}
	configured.Free(a)
	prepared := r.Prepare()
	if prepared.Code != "DEF_INVALID" || prepared.SpanStart < 17 {
		t.Error("duplicate diagnostic lost code or authored offset")
	}
	prepared.Free(a)
	if !strings.Contains(string(r.EventJSON), "second.kmk") || !strings.Contains(string(r.EventJSON), "DEF_INVALID") {
		t.Error("compile diagnostic JSON lost source")
	}
}

func TestBuildSourceDescriptorRejectsInvalidInputWithoutPoisoningRuntime(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	configured := r.SetBuildSources([]byte(`{"sources":[{"name":"valid.kmk","text":"x = 1","offset":0},{"name":"","text":"","offset":0}]}`))
	if configured.Code != "PARSE_ERR" || len(r.BuildSources) != 0 {
		t.Error("invalid partial descriptor retained sources")
	}
	configured.Free(a)
	configured = r.SetBuildSources([]byte(`{"sources":[{"name":"valid.kmk","text":"x = 1","offset":0}]}`))
	if configured.Code != "" {
		t.Error("valid retry was rejected")
	}
	configured.Free(a)
	configured = r.SetBuildSources([]byte(`{"sources":[{"name":"other.kmk","text":"x = 2","offset":0}]}`))
	if configured.Code != "PHASE_INVALID" {
		t.Error("second descriptor replaced configured sources")
	}
	configured.Free(a)
}

func TestBuildSourceDescriptorRejectsOversizeAndOverflowingOffsets(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil {
		t.Fatal("create runtime")
		return
	}
	r := start.Runtime
	defer r.Free()
	configured := r.SetBuildSources([]byte(`{"sources":[{"name":"source.kmk","text":"x = 1","offset":2147483648}]}`))
	if configured.Code != "PARSE_ERR" {
		t.Error("overflowing source offset accepted")
	}
	configured.Free(a)
	configured = r.SetBuildSources([]byte(`{"sources":[{"name":"source.kmk","text":"x = 1","offset":2147483647}]}`))
	if configured.Code != "PARSE_ERR" || len(r.BuildSources) != 0 {
		t.Error("overflowing source end accepted")
	}
	configured.Free(a)
	b := strings.NewBuilder(a)
	b.WriteString(`{"sources":[{"name":"source.kmk","text":"`)
	for i := 0; i < 65537; i++ {
		b.WriteString(" ")
	}
	b.WriteString(`","offset":0}]}`)
	configured = r.SetBuildSources([]byte(b.String()))
	if configured.Code != "NO_MEMORY" || len(r.BuildSources) != 0 {
		t.Error("oversize source text accepted")
	}
	configured.Free(a)
	b.Free()
}

func TestBuildSourceDescriptorRejectsInvalidFieldTypes(t *testing.T) {
	a := t.Allocator()
	start := wasm.NewRuntime(a, "")
	if start.Runtime == nil { t.Fatal("create runtime"); return }
	r := start.Runtime
	defer r.Free()
	bad := []string{
		`{"sources":[{"name":"source.kmk","text":"","offset":"1"}]}`,
		`{"sources":[{"name":"source.kmk","text":1,"offset":0}]}`,
		`{"sources":[{"name":"source.kmk","offset":0}]}`,
		`{"sources":[{"name":1,"text":"","offset":0}]}`,
	}
	for i := range bad {
		configured := r.SetBuildSources([]byte(bad[i]))
		if configured.Code != "PARSE_ERR" || len(r.BuildSources) != 0 { t.Error("invalid source field accepted") }
		configured.Free(a)
	}
	configured := r.SetBuildSources([]byte(`{"sources":[{"name":"source.kmk","text":""}]}`))
	if configured.Code != "" { t.Error("optional zero offset rejected") }
	configured.Free(a)
}
