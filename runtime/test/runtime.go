package program_test

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lib"
	"littlemake/lang/eval"
	"littlemake/lang/script"
	"littlemake/runtime"
	"solod.dev/so/math"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
	"solod.dev/so/time"
)

func dependOnNamedTarget(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if len(values) != 1 || values[0].Kind != core.String { return eval.Result{Diagnostic: coreDiagnostic("invalid target dependency")} }
	key := core.ResourceKey{Kind: core.ResourceTarget, Name: values[0].Text}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func observeDependency(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	_ = values
	key := core.ResourceKey{Kind: core.ResourceTarget, Name: "input"}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func observeFileDependency(context *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if len(values) != 1 || values[0].Kind != core.String { return eval.Result{Diagnostic: coreDiagnostic("invalid file dependency")} }
	key := core.ResourceKey{Kind: core.ResourceFile, Name: values[0].Text}
	if !context.Dependency(key) { return eval.Result{Waiting: true} }
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func coreDiagnostic(message string) diagnostic.Diagnostic { return diagnostic.Diagnostic{Code: "EXPR_INVALID", Severity: diagnostic.Error, Message: message} }

func cacheVersionOperation(context *eval.Context, state any, values []core.Value) eval.Result {
	_, _, _ = context, state, values
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}

func TestLiteralSelectionAndFreshness(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "./out/result : ./input\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	plan := compiled.Program.Plan("out/result")
	if plan.Diagnostic.Code != "" || plan.Plan.Rule == nil || len(plan.Plan.Outputs) != 1 { t.Error("relative file target did not select literal rule") }
	plan.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

type cacheHashVector struct { text string; want string }
type cacheValueVector struct { name string; value core.Value; want string }

func TestFingerprintSHA256StandardVectors(t *testing.T) {
	a:=t.Allocator()
	cases:=[]cacheHashVector{
		{"","e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc","ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq","248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1"},
	}
	for _,tc:=range cases {
		var digest [32]byte
		if !program.FingerprintSHA256([]byte(tc.text),digest[:]) { t.Fatal("digest rejected full-size output"); return }
		actual:=program.FingerprintHex(digest[:]); if actual!=tc.want { t.Errorf("SHA-256(%q) = %s",tc.text,actual) }; mem.FreeString(mem.System,actual)
	}
	if program.FingerprintSHA256(nil,make([]byte,31)) { t.Error("SHA-256 accepted undersized destination") }
	_ = math.Float64bits(0) // Keep float bit-pattern edge tests adjacent to digest vectors.
	_ = a
}

func TestFingerprintValueTagVectors(t *testing.T) {
	a:=t.Allocator()
	cases:=[]cacheValueVector{
		{"nil",core.Value{Kind:core.Nil},"00"},
		{"false",core.Value{Kind:core.Bool},"01"},
		{"true",core.Value{Kind:core.Bool,Bool:true},"02"},
		{"int",core.Value{Kind:core.Int,Int:-2},"03feffffffffffffff"},
		{"float",core.Value{Kind:core.Float,Float:1},"04000000000000f03f"},
		{"string",core.NewString(a,"A"),"05010000000000000041"},
		{"bytes",core.NewBytes(a,[]byte{0,255}),"06020000000000000000ff"},
		{"list",core.Value{Kind:core.List},"070000000000000000"},
		{"record",core.Value{Kind:core.Record},"080000000000000000"},
		{"resource",core.Value{Kind:core.Resource,Resource:core.ResourceKey{Kind:core.ResourceFile,Name:"x"}},"0905040000000000000066696c6505010000000000000078"},
	}
	for i:=range cases {
		encoded:=program.EncodeFingerprintValue(a,cases[i].value)
		actual:=program.FingerprintHex(encoded); if actual!=cases[i].want { t.Errorf("%s encoding = %s, want %s",cases[i].name,actual,cases[i].want) }; mem.FreeString(mem.System,actual)
		if len(encoded)!=0 { mem.FreeSlice(a,encoded) }
		if cases[i].value.Kind!=core.Resource { cases[i].value.Free(a) }
	}
	listValues:=[]core.Value{{Kind:core.Bool},{Kind:core.String,Text:"A"}}
	listValue:=core.NewList(a,listValues)
	listBytes:=program.EncodeFingerprintValue(a,listValue)
	if actual:=program.FingerprintHex(listBytes); actual!="0702000000000000000105010000000000000041" { t.Errorf("nested list encoding = %s",actual);mem.FreeString(mem.System,actual) } else { mem.FreeString(mem.System,actual) }
	mem.FreeSlice(a,listBytes);listValue.Free(a)
	fields:=[]core.RecordField{{Key:"b",Value:core.Value{Kind:core.Bool}},{Key:"a",Value:core.Value{Kind:core.Bool,Bool:true}}}
	recordValue:=core.NewRecord(a,fields)
	recordBytes:=program.EncodeFingerprintValue(a,recordValue)
	actualRecord:=program.FingerprintHex(recordBytes)
	if actualRecord!="08020000000000000005010000000000000061020501000000000000006201" { t.Errorf("sorted record encoding = %s",actualRecord) }
	mem.FreeString(mem.System,actualRecord);mem.FreeSlice(a,recordBytes);recordValue.Free(a)
	negativeZero:=core.Value{Kind:core.Float,Float:-0.0}
	positiveZero:=core.Value{Kind:core.Float,Float:0.0}
	left,right:=program.EncodeFingerprintValue(a,negativeZero),program.EncodeFingerprintValue(a,positiveZero)
	if string(left)!=string(right) { t.Error("negative zero was not canonicalized") }
	mem.FreeSlice(a,left);mem.FreeSlice(a,right)
	canonicalNaN:=math.Float64frombits(0x7ff8000000000000)
	otherNaN:=math.Float64frombits(0xfff0000000000001)
	left=program.EncodeFingerprintValue(a,core.Value{Kind:core.Float,Float:canonicalNaN})
	right=program.EncodeFingerprintValue(a,core.Value{Kind:core.Float,Float:otherNaN})
	if string(left)!=string(right) { t.Error("NaN payload/sign was not canonicalized") }
	mem.FreeSlice(a,left);mem.FreeSlice(a,right)
}

func TestCompleteTaskFingerprintVector(t *testing.T) {
	a:=t.Allocator()
	const dir="/tmp/littlemake-cache-fingerprint-vector"
	if os.Mkdir(dir,0o755)!=nil { t.Fatal("cannot create fingerprint vector directory");return }
	parsed:=script.Parse(a,"vector.lmk","task cache-vector :\n\tprintf vector\n")
	registry:=eval.NewRegistry(a)
	compiled:=program.Compile(a,parsed,registry,program.Options{Directory:dir,Shell:[]string{"/bin/sh","-c"},Environment:[]string{"PATH=/usr/bin:/bin"}})
	if compiled.Program==nil { t.Fatal("vector task compile failed");return }
	result:=compiled.Program.Materialize("cache-vector")
	if result.Diagnostic.Code!="" { t.Fatalf("vector task failed: %s",result.Diagnostic.Code) }
	result.Free(a)
	entries,err:=os.ReadDir(a,dir+"/.littlemake/cache/tasks")
	if err!=nil||len(entries)!=1 { t.Fatal("expected exactly one task record");return }
	data,err:=os.ReadFile(a,dir+"/.littlemake/cache/tasks/"+entries[0].Name)
	os.FreeDirEntry(a,entries)
	if err!=nil||len(data)<46 { t.Fatal("task record could not be read");return }
	identityLength:=uint64(0);for i:=0;i<8;i++{identityLength|=uint64(data[5+i])<<uint(i*8)}
	fingerprintAt:=13+int(identityLength)
	if fingerprintAt+32>len(data) { mem.FreeSlice(a,data);t.Fatal("record fingerprint is truncated");return }
	fingerprint:=program.FingerprintHex(data[fingerprintAt:fingerprintAt+32])
	if fingerprint!="442a19570c148beb32b3ad2f34e604631caeb818896b2da3b584e78c7038b6a8" { t.Errorf("complete task fingerprint = %s",fingerprint) }
	mem.FreeString(mem.System,fingerprint);mem.FreeSlice(a,data)
	compiled.Program.Free();compiled.Free(a);parsed.Free();registry.Free()
	entries,err=os.ReadDir(a,dir+"/.littlemake/cache/tasks")
	if err==nil { for i:=range entries { _=os.Remove(dir+"/.littlemake/cache/tasks/"+entries[i].Name) };os.FreeDirEntry(a,entries) }
	_ = os.Remove(dir+"/.littlemake/cache/tasks")
	_ = os.Remove(dir+"/.littlemake/cache")
	_ = os.Remove(dir+"/.littlemake")
	_ = os.Remove(dir)
}

func TestMaterializeWritesOutput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "./out/result :\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("out/result")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/out/result")
	if readErr != nil || string(data) != "result" { t.Error("recipe did not write declared output") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMissingDeclaredOutputFails(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "./out/result :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("out/result")
	if result.Diagnostic.Code != "OUTPUT_MISSING" { t.Error("missing output did not fail") }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestSiblingOutputsShareOneExecution(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./out/a ./out/b : ./input\n\tprintf a > @>0\n\tprintf b > @>1\n\tprintf x >> file-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	left := compiled.Program.Materialize("out/a")
	right := compiled.Program.Materialize("out/b")
	if left.Diagnostic.Code != "" || right.Diagnostic.Code != "" || right.Path != "out/b" { t.Error("sibling output materialization failed") }
	left.Free(a); right.Free(a)
	file, fileErr := os.ReadFile(a, dir+"/file-log")
	if fileErr != nil || string(file) != "x" { t.Error("sibling outputs did not share execution") }
	mem.FreeSlice(a, file)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestTemplateCapturesRenderInputsAndOutputs(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil { t.Fatal("source directory failed"); return }
	if os.WriteFile(dir+"/src/demo", []byte("captured"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./out/{name} : ./src/{name}\n\tcp @< @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("out/demo")
	if result.Diagnostic.Code != "" { t.Errorf("template materialization failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/out/demo")
	if readErr != nil || string(data) != "captured" { t.Error("captures did not render corresponding input and output") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMatchingTemplatesAreAmbiguous(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "./out/{name} :\n\ttrue\n./out/{other} :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	planned := compiled.Program.Plan("out/demo")
	if planned.Diagnostic.Code != "TGT_AMBIG" { t.Error("matching templates were not ambiguous") }
	planned.Diagnostic.Free(a)
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestExistingFileNeedsNoRule(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./input")
	if result.Diagnostic.Code != "" || !result.Fresh || result.Path != "./input" { t.Error("existing explicit file was not materialized") }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBareTaskRuns(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	first := compiled.Program.Materialize("run")
	second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("bare task failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Error("bare task did not rerun") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskHits(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	first := compiled.Program.Materialize("run")
	second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("cached task failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "x" { t.Error("cached task did not hit") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskInvalidatesForDeclaredFile(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("one"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "task run : ./input\n\tcat ./input >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run")
	second := compiled.Program.Materialize("run")
	if os.WriteFile(dir+"/input", []byte("two"), 0o644) != nil { t.Fatal("input update failed"); return }
	third := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" || third.Diagnostic.Code != "" { t.Error("cached task materialization failed") }
	first.Free(a); second.Free(a); third.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "onetwo" { t.Error("declared file did not invalidate cached task") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

// File fingerprints include the filesystem kind as well as metadata and bytes.
// A directory and a regular file at the same dependency path must never share
// a cache result, even when their names are unchanged.
func TestCachedTaskInvalidatesWhenDependencyKindChanges(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-kind-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/input", 0o755) != nil { t.Fatal("input directory creation failed"); return }
	parsed := script.Parse(a, "test.lmk", "task run : ./input\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, Verbose: true})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" { t.Fatalf("first materialization failed: %s", first.Diagnostic.Code) }
	first.Free(a)
	if os.Remove(dir+"/input") != nil { t.Fatal("input directory removal failed"); return }
	if os.WriteFile(dir+"/input", []byte("regular file"), 0o644) != nil { t.Fatal("regular input creation failed"); return }
	second := compiled.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Errorf("second materialization failed: %s", second.Diagnostic.Code) }
	second.Free(a)
	warnings := 0
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; if next.Event.Diagnostic.Code == "CACHE_UNUSABLE" { warnings++ }; next.Event.Free(a) }
	if warnings != 1 { t.Errorf("unfingerprintable dependency should warn once; got %d warnings", warnings) }
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Errorf("dependency kind change incorrectly reused cache: %q", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskRechecksFileAfterMtimeOnlyChange(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-mtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("same"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "task run : ./input\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); first.Free(a)
	stamp := time.Unix(100, 0)
	if os.Chtimes(dir+"/input", stamp, stamp) != nil { t.Fatal("mtime update failed"); return }
	second := compiled.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Errorf("mtime-only rerun failed: %s", second.Diagnostic.Code) }
	second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Errorf("mtime-only change did not safely recompute the input fingerprint: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskReplaysSeparatedStreams(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf out; printf err >&2\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); first.Free(a)
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; next.Event.Free(a) }
	second := compiled.Program.Materialize("run"); second.Free(a)
	cachedOut, cachedErr := false, false
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; if next.Event.Cached && next.Event.Kind == program.Stdout && string(next.Event.Data) == "out" { cachedOut = true }; if next.Event.Cached && next.Event.Kind == program.Stderr && string(next.Event.Data) == "err" { cachedErr = true }; next.Event.Free(a) }
	if !cachedOut || !cachedErr { t.Error("cache did not replay stdout and stderr separately") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskCachesDeferredOutput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\t@(out \"out\")\n\t@(err \"err\")\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); first.Free(a)
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; next.Event.Free(a) }
	second := compiled.Program.Materialize("run"); second.Free(a)
	cachedOut, cachedErr := false, false
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; if next.Event.Cached && next.Event.Kind == program.Stdout && string(next.Event.Data) == "out" { cachedOut = true }; if next.Event.Cached && next.Event.Kind == program.Stderr && string(next.Event.Data) == "err" { cachedErr = true }; next.Event.Free(a) }
	if !cachedOut || !cachedErr { t.Error("cached task did not replay deferred output") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMalformedCacheRecordIsMiss(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); first.Free(a)
	entries, readErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if readErr != nil || len(entries) != 1 { t.Fatal("cache record was not written"); return }
	if os.WriteFile(dir+"/.littlemake/cache/tasks/"+entries[0].Name, []byte("broken"), 0o644) != nil { t.Fatal("cache corruption failed"); return }
	os.FreeDirEntry(a, entries)
	second := compiled.Program.Materialize("run"); second.Free(a)
	data, dataErr := os.ReadFile(a, dir+"/task-log")
	if dataErr != nil || string(data) != "xx" { t.Error("malformed cache record was not treated as a miss") }
	mem.FreeSlice(a, data)
	// A malformed record is silent unless verbose diagnostics were requested.
	compiled.Program.Free(); compiled.Free(a)
	entries, readErr = os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if readErr != nil || len(entries)!=1 { t.Fatal("cache record was not replaced"); return }
	verboseCorruptPath:=dir+"/.littlemake/cache/tasks/"+entries[0].Name
	os.FreeDirEntry(a,entries)
	if os.WriteFile(verboseCorruptPath,[]byte("broken"),0o644)!=nil { t.Fatal("cache corruption failed"); return }
	verbose := program.Compile(a, parsed, registry, program.Options{Directory: dir, Verbose: true})
	if verbose.Program == nil { t.Fatal("verbose compile failed"); return }
	verboseResult := verbose.Program.Materialize("run"); verboseResult.Free(a)
	warningFound := false
	for { next:=verbose.Program.NextEvent(); if !next.OK { break }; if next.Event.Kind==program.CacheWarning && next.Event.Diagnostic.Code=="CACHE_RECORD" { warningFound=true }; next.Event.Free(a) }
	if !warningFound { t.Error("verbose mode omitted malformed-record warning") }
	verbose.Program.Free(); verbose.Free(a)
	// Write another malformed record and ensure default mode remains silent.
	entries, readErr = os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if readErr != nil || len(entries)!=1 { t.Fatal("cache record was not replaced"); return }
	cacheFile := dir+"/.littlemake/cache/tasks/"+entries[0].Name
	os.FreeDirEntry(a,entries)
	if os.WriteFile(cacheFile,[]byte("broken"),0o644)!=nil { t.Fatal("cache corruption failed"); return }
	quiet := program.Compile(a,parsed,registry,program.Options{Directory:dir})
	if quiet.Program==nil { t.Fatal("quiet compile failed"); return }
	quietResult:=quiet.Program.Materialize("run");quietResult.Free(a)
	for { next:=quiet.Program.NextEvent();if !next.OK{break};if next.Event.Kind==program.CacheWarning{t.Error("default mode emitted cache warning")};next.Event.Free(a) }
	quiet.Program.Free();quiet.Free(a)
	compiled = program.Compile(a,parsed,registry,program.Options{Directory:dir})
	if compiled.Program==nil { t.Fatal("final compile failed");return }
	entries, readErr = os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if readErr != nil || len(entries) != 1 { t.Fatal("replacement cache record was not written"); return }
	cachePath := dir+"/.littlemake/cache/tasks/"+entries[0].Name
	os.FreeDirEntry(a, entries)
	f, openErr := os.OpenFile(cachePath, os.O_WRONLY|os.O_APPEND, 0o644)
	if openErr != nil { t.Fatal("cannot append malformed trailing byte"); return }
	if _, writeErr := f.Write([]byte{0xff}); writeErr != nil { t.Fatal("cannot append malformed trailing byte"); f.Close(); return }
	f.Close()
	third := compiled.Program.Materialize("run"); third.Free(a)
	data, dataErr = os.ReadFile(a, dir+"/task-log")
	if dataErr != nil || string(data) != "xxxxx" { t.Error("cache record with trailing garbage was not rejected") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCacheLookupRejectsMutatedDependencyManifest(t *testing.T) {
	a:=t.Allocator()
	dirBuffer:=make([]byte,os.MaxPathLen)
	dir,err:=os.MkdirTemp(dirBuffer,"","littlemake-cache-manifest-")
	if err!=nil { t.Fatal("temporary directory failed");return }
	defer os.Remove(dir)
	parsed:=script.Parse(a,"manifest.lmk","task run :\n\tprintf x >> task-log\n")
	registry:=eval.NewRegistry(a)
	compiled:=program.Compile(a,parsed,registry,program.Options{Directory:dir})
	if compiled.Program==nil { t.Fatal("compile failed");return }
	first:=compiled.Program.Materialize("run");first.Free(a)
	entries,readErr:=os.ReadDir(a,dir+"/.littlemake/cache/tasks")
	if readErr!=nil||len(entries)!=1 { t.Fatal("cache record was not written");return }
	cachePath:=dir+"/.littlemake/cache/tasks/"+entries[0].Name
	os.FreeDirEntry(a,entries)
	record,readErr:=os.ReadFile(a,cachePath)
	if readErr!=nil||len(record)<90 { t.Fatal("cache record could not be read");return }
	identityLength:=uint64(0)
	for i:=0;i<8;i++ { identityLength|=uint64(record[5+i])<<uint(i*8) }
	manifestLengthAt:=5+8+int(identityLength)+32+1+32
	manifestStart:=manifestLengthAt+8
	if manifestStart>=len(record) { t.Fatal("manifest was empty");mem.FreeSlice(a,record);return }
	record[manifestStart]^=0x01
	if os.WriteFile(cachePath,record,0o644)!=nil { t.Fatal("manifest mutation failed");mem.FreeSlice(a,record);return }
	mem.FreeSlice(a,record)
	second:=compiled.Program.Materialize("run");second.Free(a)
	log,logErr:=os.ReadFile(a,dir+"/task-log")
	if logErr!=nil||string(log)!="xx" { t.Errorf("mutated manifest incorrectly produced a cache hit: %s",string(log)) }
	mem.FreeSlice(a,log)
	compiled.Program.Free();compiled.Free(a);parsed.Free();registry.Free()
}

func TestCachedTaskInvalidatesForOperationVersion(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\t@((versioned))\n\tprintf x >> task-log\n")
	firstRegistry := eval.NewRegistry(a)
	if !firstRegistry.Add(eval.Operation{Name: "versioned", Version: "v1", Call: cacheVersionOperation, MinArity: 0, MaxArity: 0}) { t.Fatal("operation registration failed"); return }
	firstProgram := program.Compile(a, parsed, firstRegistry, program.Options{Directory: dir})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a); firstRegistry.Free()
	secondRegistry := eval.NewRegistry(a)
	if !secondRegistry.Add(eval.Operation{Name: "versioned", Version: "v2", Call: cacheVersionOperation, MinArity: 0, MaxArity: 0}) { t.Fatal("operation registration failed"); return }
	secondProgram := program.Compile(a, parsed, secondRegistry, program.Options{Directory: dir})
	if secondProgram.Program == nil { t.Fatal("second compile failed"); return }
	second := secondProgram.Program.Materialize("run"); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Error("operation version did not invalidate cached task") }
	mem.FreeSlice(a, data)
	secondProgram.Program.Free(); secondProgram.Free(a); secondRegistry.Free(); parsed.Free()
}

// A recipe runs with the explicitly configured environment (the host does not
// inherit the caller's ambient environment). Changing that environment must
// therefore produce a distinct cache fingerprint.
func TestCachedTaskInvalidatesForExecutionEnvironment(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-env-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf '%s' \\\"$LM_CACHE_VALUE\\\" >> task-log\n")
	registry := eval.NewRegistry(a)
	firstProgram := program.Compile(a, parsed, registry, program.Options{Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "LM_CACHE_VALUE=one"}})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run")
	if first.Diagnostic.Code != "" { t.Error("first cached task failed") }
	first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	secondProgram := program.Compile(a, parsed, registry, program.Options{Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "LM_CACHE_VALUE=two"}})
	if secondProgram.Program == nil { t.Fatal("second compile failed"); return }
	second := secondProgram.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Error("second cached task failed") }
	second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != `"one""two"` { t.Errorf("execution environment did not invalidate cache: %s", string(data)) }
	mem.FreeSlice(a, data)
	secondProgram.Program.Free(); secondProgram.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskInvalidatesForDeclaredEnvironmentDependency(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-envdep-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\t@(out (str (env \"LM_CACHE_VALUE\")))\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	grant := []eval.Grant{{Capability: eval.Env, Names: []string{"LM_CACHE_VALUE"}}}
	firstProgram := program.Compile(a, parsed, registry, program.Options{Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "LM_CACHE_VALUE=one"}, Grants: grant})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	secondProgram := program.Compile(a, parsed, registry, program.Options{Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "LM_CACHE_VALUE=two"}, Grants: grant})
	if secondProgram.Program == nil { t.Fatal("second compile failed"); return }
	second := secondProgram.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Errorf("second run failed: %s", second.Diagnostic.Code) }
	second.Free(a)
	found := false
	for { next := secondProgram.Program.NextEvent(); if !next.OK { break }; if next.Event.Kind == program.Stdout && string(next.Event.Data) == "two" && !next.Event.Cached { found = true }; next.Event.Free(a) }
	if !found { t.Error("changed declared environment dependency did not rerun recipe") }
	secondProgram.Program.Free(); secondProgram.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskInvalidatesForShell(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-shell-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	firstProgram := program.Compile(a, parsed, registry, program.Options{Directory: dir, Shell: []string{"/bin/sh", "-c"}})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	secondProgram := program.Compile(a, parsed, registry, program.Options{Directory: dir, Shell: []string{"/bin/bash", "-c"}})
	if secondProgram.Program == nil { t.Fatal("second compile failed"); return }
	second := secondProgram.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Error("second cached task failed") }
	second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Errorf("shell change did not invalidate cache: %s", string(data)) }
	mem.FreeSlice(a, data)
	secondProgram.Program.Free(); secondProgram.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskInvalidatesForTimeoutOption(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-timeout-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	firstProgram := program.Compile(a, parsed, registry, program.Options{Directory: dir, TimeoutMS: 1000})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	secondProgram := program.Compile(a, parsed, registry, program.Options{Directory: dir, TimeoutMS: 2000})
	if secondProgram.Program == nil { t.Fatal("second compile failed"); return }
	second := secondProgram.Program.Materialize("run"); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Errorf("timeout change did not invalidate cached task: %s", string(data)) }
	mem.FreeSlice(a, data)
	secondProgram.Program.Free(); secondProgram.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskInvalidatesForRetryCount(t *testing.T) {
	a:=t.Allocator()
	dirBuffer:=make([]byte,os.MaxPathLen)
	dir,err:=os.MkdirTemp(dirBuffer,"","littlemake-cache-retry-option-")
	if err!=nil { t.Fatal("temporary directory failed");return }
	defer os.Remove(dir)
	parsed:=script.Parse(a,"retry.lmk","task run :\n\tprintf x >> task-log\n")
	registry:=eval.NewRegistry(a)
	firstProgram:=program.Compile(a,parsed,registry,program.Options{Directory:dir,RetryCount:0})
	if firstProgram.Program==nil { t.Fatal("first compile failed");return }
	first:=firstProgram.Program.Materialize("run");first.Free(a)
	firstProgram.Program.Free();firstProgram.Free(a)
	secondProgram:=program.Compile(a,parsed,registry,program.Options{Directory:dir,RetryCount:1})
	if secondProgram.Program==nil { t.Fatal("second compile failed");return }
	second:=secondProgram.Program.Materialize("run");second.Free(a)
	data,readErr:=os.ReadFile(a,dir+"/task-log")
	if readErr!=nil||string(data)!="xx" { t.Error("retry count change did not invalidate the cached task") }
	mem.FreeSlice(a,data)
	secondProgram.Program.Free();secondProgram.Free(a);parsed.Free();registry.Free()
}

func TestCachedTaskRetriesFailedRecipe(t *testing.T) {
	a:=t.Allocator()
	dirBuffer:=make([]byte,os.MaxPathLen)
	dir,err:=os.MkdirTemp(dirBuffer,"","littlemake-cache-retry-run-")
	if err!=nil { t.Fatal("temporary directory failed");return }
	defer os.Remove(dir)
	parsed:=script.Parse(a,"retry.lmk","task run :\n\tif [ ! -e attempted ]; then touch attempted; exit 1; fi\n\tprintf success >> task-log\n")
	registry:=eval.NewRegistry(a)
	compiled:=program.Compile(a,parsed,registry,program.Options{Directory:dir,RetryCount:1})
	if compiled.Program==nil { t.Fatal("compile failed");return }
	result:=compiled.Program.Materialize("run")
	if result.Diagnostic.Code!="" { t.Errorf("cached task failed after configured retry: %s",result.Diagnostic.Code) }
	result.Free(a)
	data,readErr:=os.ReadFile(a,dir+"/task-log")
	if readErr!=nil||string(data)!="success" { t.Error("failed cached task did not execute its successful retry") }
	mem.FreeSlice(a,data)
	compiled.Program.Free();compiled.Free(a);parsed.Free();registry.Free()
}

func TestCachedTaskInvalidatesForRuleBody(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-rule-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	registry := eval.NewRegistry(a)
	firstScript := script.Parse(a, "first.lmk", "task run :\n\tprintf a >> task-log\n")
	firstProgram := program.Compile(a, firstScript, registry, program.Options{Directory: dir})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a); firstScript.Free()
	secondScript := script.Parse(a, "second.lmk", "task run :\n\tprintf b >> task-log\n")
	secondProgram := program.Compile(a, secondScript, registry, program.Options{Directory: dir})
	if secondProgram.Program == nil { t.Fatal("second compile failed"); return }
	second := secondProgram.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Error("second cached task failed") }
	second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "ab" { t.Errorf("rule body change did not invalidate cache: %s", string(data)) }
	mem.FreeSlice(a, data)
	secondProgram.Program.Free(); secondProgram.Free(a); secondScript.Free(); registry.Free()
}

func TestCachedTaskCapturesHaveDistinctIdentities(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-capture-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run-{name} :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run-one")
	second := compiled.Program.Materialize("run-two")
	third := compiled.Program.Materialize("run-one")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" || third.Diagnostic.Code != "" { t.Error("template cached task failed") }
	first.Free(a); second.Free(a); third.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Errorf("capture values did not produce distinct identities and a subsequent hit: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskRetainedOutputLimitAndTruncation(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-output-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf abcdef\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, CacheRetainBytes: 3})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); first.Free(a)
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; next.Event.Free(a) }
	second := compiled.Program.Materialize("run"); second.Free(a)
	found := false
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; if next.Event.Cached && next.Event.Kind == program.Stdout { found = true; if string(next.Event.Data) != "abc" || !next.Event.Truncated { t.Errorf("cached output limit/truncation mismatch: %q truncated=%v", string(next.Event.Data), next.Event.Truncated) } }; next.Event.Free(a) }
	if !found { t.Error("cache hit did not replay retained stdout") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskInvalidatesWhenGlobMembershipChanges(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-glob-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil { t.Fatal("source directory failed"); return }
	if os.WriteFile(dir+"/src/one", []byte("1"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.lmk", "task run :\n\t@(out (str (count (wildcard \"./src/*\"))))\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	options := program.Options{Directory: dir, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}}
	firstProgram := program.Compile(a, parsed, registry, options)
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run")
	if first.Diagnostic.Code != "" { t.Errorf("first run failed: %s", first.Diagnostic.Code) }
	first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	if os.WriteFile(dir+"/src/two", []byte("2"), 0o644) != nil { t.Fatal("glob member write failed"); return }
	secondProgram := program.Compile(a, parsed, registry, options)
	if secondProgram.Program == nil { t.Fatal("second compile failed"); return }
	second := secondProgram.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Errorf("second run failed: %s", second.Diagnostic.Code) }
	second.Free(a)
	foundCurrent := false
	for { next := secondProgram.Program.NextEvent(); if !next.OK { break }; if next.Event.Kind == program.Stdout && string(next.Event.Data) == "2" && !next.Event.Cached { foundCurrent = true }; next.Event.Free(a) }
	if !foundCurrent { t.Error("glob membership change did not execute recipe") }
	secondProgram.Program.Free(); secondProgram.Free(a); parsed.Free(); registry.Free()
}

func TestFailedCachedAttemptDoesNotReplacePriorSuccessfulRecord(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-failure-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("good"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", `task run : ./input
	if [ "$(cat ./input)" = good ]; then printf x >> task-log; else false; fi
`)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	success := compiled.Program.Materialize("run")
	if success.Diagnostic.Code != "" { t.Errorf("initial run failed: %s", success.Diagnostic.Code) }
	success.Free(a)
	entries, listErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if listErr != nil || len(entries) != 1 { t.Fatal("successful record was not written"); return }
	cachePath := dir+"/.littlemake/cache/tasks/"+entries[0].Name
	before, beforeErr := os.ReadFile(a, cachePath)
	os.FreeDirEntry(a, entries)
	if beforeErr != nil { t.Fatal("successful record was unreadable"); return }
	if os.WriteFile(dir+"/input", []byte("bad"), 0o644) != nil { t.Fatal("input update failed"); return }
	failed := compiled.Program.Materialize("run")
	if failed.Diagnostic.Code == "" { t.Error("changed input should make the recipe fail") }
	failed.Free(a)
	afterFailure, afterFailureErr := os.ReadFile(a, cachePath)
	if afterFailureErr != nil || string(before) != string(afterFailure) { t.Error("failed attempt replaced the previous successful record") }
	if afterFailureErr == nil { mem.FreeSlice(a, afterFailure) }
	if os.WriteFile(dir+"/input", []byte("good"), 0o644) != nil { t.Fatal("input restore failed"); return }
	restored := compiled.Program.Materialize("run")
	if restored.Diagnostic.Code != "" { t.Errorf("restored successful fingerprint failed: %s", restored.Diagnostic.Code) }
	restored.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || len(data)==0 || data[0]!='x' { t.Errorf("failed attempt should not destroy prior successful output: %s", string(data)) }
	mem.FreeSlice(a, before)
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestOversizedCacheRecordIsRejectedWithoutPanic(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-oversize-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); first.Free(a)
	entries, readErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if readErr != nil || len(entries) != 1 { t.Fatal("cache record was not written"); return }
	path := dir+"/.littlemake/cache/tasks/"+entries[0].Name
	os.FreeDirEntry(a, entries)
	f, createErr := os.Create(path)
	if createErr != nil { t.Fatal("cannot open cache record for corruption"); return }
	chunk := make([]byte, 32*1024)
	copy(chunk, []byte("LMKR\x01"))
	for written := 0; written < 17*1024*1024; written += len(chunk) {
		if _, writeErr := f.Write(chunk); writeErr != nil { t.Fatal("cannot write oversized record"); f.Close(); return }
	}
	f.Close()
	second := compiled.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Errorf("oversized record caused runtime failure: %s", second.Diagnostic.Code) }
	second.Free(a)
	data, dataErr := os.ReadFile(a, dir+"/task-log")
	if dataErr != nil || string(data) != "xx" { t.Errorf("oversized record was not a cache miss: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestIndependentProgramsUseAtomicConcurrentCacheWriters(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-writers-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task run :\n\tsleep 0.1; printf x >> task-log\n")
	registry := eval.NewRegistry(a)
	left := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	right := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if left.Program == nil || right.Program == nil { t.Fatal("concurrent compile failed"); return }
	leftStart := left.Program.Start("run")
	rightStart := right.Program.Start("run")
	if leftStart.Handle == nil || rightStart.Handle == nil { t.Fatal("concurrent start failed"); return }
	leftDone, rightDone := false, false
	for i:=0; i<100 && (!leftDone || !rightDone); i++ {
		left.Program.Tick(2); right.Program.Tick(2)
		if !leftDone { poll:=leftStart.Handle.Poll(); if poll.Done { leftDone=true; if poll.Result.Diagnostic.Code!="" { t.Errorf("left writer failed: %s",poll.Result.Diagnostic.Code) }; poll.Result.Free(a) } }
		if !rightDone { poll:=rightStart.Handle.Poll(); if poll.Done { rightDone=true; if poll.Result.Diagnostic.Code!="" { t.Errorf("right writer failed: %s",poll.Result.Diagnostic.Code) }; poll.Result.Free(a) } }
	}
	if !leftDone || !rightDone { t.Error("concurrent writers did not complete") }
	leftStart.Handle.Free(); rightStart.Handle.Free()
	left.Program.Free(); left.Free(a); right.Program.Free(); right.Free(a)
	verify := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if verify.Program == nil { t.Fatal("verification compile failed"); return }
	result := verify.Program.Materialize("run"); if result.Diagnostic.Code!="" { t.Errorf("cache record after concurrent writes was unreadable: %s",result.Diagnostic.Code) }; result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data)!="xx" { t.Errorf("concurrent cache writer left no reusable complete record: %s",string(data)) }
	mem.FreeSlice(a,data)
	verify.Program.Free(); verify.Free(a); parsed.Free(); registry.Free()
}

func TestBareTaskDependencyPreventsCachedHit(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tprintf l >> task-log\ntask run : leaf\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("task materialization failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "lrlr" { t.Errorf("bare dependency did not rerun: %s", string(data)) }
	mem.FreeSlice(a, data)
	entries, cacheErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if cacheErr == nil {
		if len(entries) != 0 { t.Error("bare dependency committed a cache record") }
		os.FreeDirEntry(a, entries)
	}
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBareTaskRerunsForEachRoot(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tprintf l >> task-log\nleft : leaf\nright : leaf\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("left")
	second := compiled.Program.Materialize("right")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("root materialization failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "ll" { t.Errorf("bare task did not run once per root: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestTransitiveBareTaskReruns(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tprintf l >> task-log\ntask mid : leaf\n\tprintf m >> task-log\ntask run : mid\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run")
	second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("transitive materialization failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "lmrlmr" { t.Errorf("transitive bare task did not rerun: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestConcurrentRootsShareBareTask(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tprintf x >> task-log\nleft : leaf\nright : leaf\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	left := compiled.Program.Start("left")
	right := compiled.Program.Start("right")
	if left.Diagnostic.Code != "" || right.Diagnostic.Code != "" || left.Handle == nil || right.Handle == nil { t.Fatal("start failed"); return }
	leftDone, rightDone := false, false
	for i := 0; i < 40 && (!leftDone || !rightDone); i++ {
		compiled.Program.Tick(10)
		if !leftDone {
			polled := left.Handle.Poll()
			if polled.Done {
				leftDone = true
				if polled.Result.Diagnostic.Code != "" { t.Error("left failed") }
				polled.Result.Free(a)
			}
		}
		if !rightDone {
			polled := right.Handle.Poll()
			if polled.Done {
				rightDone = true
				if polled.Result.Diagnostic.Code != "" { t.Error("right failed") }
				polled.Result.Free(a)
			}
		}
	}
	if !leftDone || !rightDone { t.Error("concurrent roots did not finish") }
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "x" { t.Errorf("concurrent roots did not share bare task: %s", string(data)) }
	mem.FreeSlice(a, data)
	left.Handle.Free(); right.Handle.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCancelBareRerunPreservesCachedRecord(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "task keep :\n\tprintf k >> task-log\nleaf :\n\tsleep 1\ntask run : leaf\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	kept := compiled.Program.Materialize("keep")
	if kept.Diagnostic.Code != "" { t.Fatal("cached task failed"); return }
	kept.Free(a)
	entries, readErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if readErr != nil || len(entries) != 1 { t.Fatal("cache record was not written"); return }
	before, beforeErr := os.ReadFile(a, dir+"/.littlemake/cache/tasks/"+entries[0].Name)
	os.FreeDirEntry(a, entries)
	if beforeErr != nil { t.Fatal("cache record unreadable"); return }
	first := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" { mem.FreeSlice(a, before); t.Fatal("first run failed"); return }
	first.Free(a)
	rerun := compiled.Program.Start("run")
	if rerun.Diagnostic.Code != "" || rerun.Handle == nil { mem.FreeSlice(a, before); t.Fatal("rerun start failed"); return }
	submitted := false
	for i := 0; i < 40 && !submitted; i++ {
		compiled.Program.Tick(10)
		if compiled.Program.Host.Active() != 0 { submitted = true }
	}
	if !submitted { mem.FreeSlice(a, before); rerun.Handle.Free(); t.Fatal("bare rerun did not submit"); return }
	rerun.Handle.Cancel()
	for i := 0; i < 40 && compiled.Program.Host.Active() != 0; i++ { compiled.Program.Tick(10) }
	if compiled.Program.Host.Active() != 0 { t.Error("cancelled bare rerun did not stop") }
	rerun.Handle.Free()
	later, laterErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if laterErr != nil || len(later) != 1 { mem.FreeSlice(a, before); t.Error("cancellation changed cache records"); return }
	after, afterErr := os.ReadFile(a, dir+"/.littlemake/cache/tasks/"+later[0].Name)
	os.FreeDirEntry(a, later)
	if afterErr != nil || string(before) != string(after) { t.Error("cancellation replaced cached-task record") }
	mem.FreeSlice(a, before)
	if afterErr == nil { mem.FreeSlice(a, after) }
	again := compiled.Program.Materialize("keep")
	if again.Diagnostic.Code != "" { t.Error("cached task failed after cancellation") }
	again.Free(a)
	data, dataErr := os.ReadFile(a, dir+"/task-log")
	if dataErr != nil || string(data) != "kr" { t.Errorf("cancellation replaced cached work: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDiscoveredBareTaskRerunsForNewRoot(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tprintf l >> task-log\nrun :\n\t@(depends \"leaf\")\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "depends", Call: dependOnNamedTarget, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	primed := compiled.Program.Materialize("leaf")
	if primed.Diagnostic.Code != "" { t.Fatal("leaf failed"); return }
	primed.Free(a)
	reached := compiled.Program.Materialize("run")
	if reached.Diagnostic.Code != "" { t.Errorf("discovered dependency failed: %s", reached.Diagnostic.Code) }
	reached.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "llr" { t.Errorf("discovered bare task was reused: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestFailedBareTaskRerunsForNewRoot(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tprintf l >> task-log\n\tfalse\nrun : leaf\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run")
	if first.Diagnostic.Code == "" { t.Fatal("failing bare task succeeded"); return }
	first.Free(a)
	second := materializeWithin(compiled.Program, "run", 40)
	if second.Diagnostic.Code == "" || second.Diagnostic.Code == "TEST_TIMEOUT" { t.Errorf("failed bare task was not rerun: %s", second.Diagnostic.Code) }
	second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "ll" { t.Errorf("failed bare task was reused: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCancelledBareTaskRerunsForNewRoot(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tsleep 1\n\tprintf l >> task-log\nrun : leaf\n\tprintf r >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	started := compiled.Program.Start("run")
	if started.Diagnostic.Code != "" || started.Handle == nil { t.Fatal("start failed"); return }
	submitted := false
	for i := 0; i < 40 && !submitted; i++ {
		compiled.Program.Tick(10)
		if compiled.Program.Host.Active() != 0 { submitted = true }
	}
	if !submitted { started.Handle.Free(); t.Fatal("bare task did not submit"); return }
	started.Handle.Cancel()
	for i := 0; i < 40 && compiled.Program.Host.Active() != 0; i++ { compiled.Program.Tick(10) }
	started.Handle.Free()
	second := materializeWithin(compiled.Program, "run", 200)
	if second.Diagnostic.Code != "" { t.Errorf("cancelled bare task was not rerun: %s", second.Diagnostic.Code) }
	second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "lr" { t.Errorf("cancelled bare task was reused: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func materializeWithin(p *program.Program, target string, ticks int) program.Result {
	started := p.Start(target)
	if started.Diagnostic.Code != "" || started.Handle == nil { return program.Result{Diagnostic: started.Diagnostic} }
	for i := 0; i < ticks; i++ {
		p.Tick(10)
		polled := started.Handle.Poll()
		if polled.Done { started.Handle.Free(); return polled.Result }
	}
	started.Handle.Cancel()
	started.Handle.Free()
	return program.Result{Diagnostic: diagnostic.Diagnostic{Code: "TEST_TIMEOUT", Severity: diagnostic.Error, Message: "materialization did not finish"}}
}

func TestDiamondDependencySharesPrerequisite(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "leaf :\n\tprintf x >> task-log\nleft : leaf\nright : leaf\nroot : left right\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("root")
	if result.Diagnostic.Code != "" { t.Errorf("diamond dependency failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "x" { t.Error("shared prerequisite did not execute once") }
	mem.FreeSlice(a, data)
	second := compiled.Program.Materialize("root")
	if second.Diagnostic.Code != "" { t.Errorf("second diamond root failed: %s", second.Diagnostic.Code) }
	second.Free(a)
	again, againErr := os.ReadFile(a, dir+"/task-log")
	if againErr != nil || string(again) != "xx" { t.Error("shared prerequisite did not rerun once for the second root") }
	mem.FreeSlice(a, again)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestFreshFileSkipsRecipe(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output : ./input\n\tcp @< @>\n\tprintf x >> recipe-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 1 || planned.Plan.Inputs[0] != "./input" { t.Error("plan did not flatten expression input") }
	planned.Plan.Free(a)
	first := compiled.Program.Materialize("./output")
	second := compiled.Program.Materialize("./output")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" || !second.Fresh { t.Errorf("fresh file materialization failed: %s %s %t", first.Diagnostic.Code, second.Diagnostic.Code, second.Fresh) }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/recipe-log")
	if readErr != nil || string(data) != "x" { t.Error("fresh file reran recipe") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestNewerInputRebuildsFile(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("first"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output : ./input\n\tcp @< @>\n\tprintf x >> recipe-log\nwait :\n\tsleep 1\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	first := compiled.Program.Materialize("./output")
	wait := compiled.Program.Materialize("wait")
	if os.WriteFile(dir+"/input", []byte("second"), 0o644) != nil { t.Fatal("input update failed"); return }
	second := compiled.Program.Materialize("./output")
	if first.Diagnostic.Code != "" || wait.Diagnostic.Code != "" || second.Diagnostic.Code != "" || second.Fresh { t.Error("newer input did not rebuild output") }
	first.Free(a); wait.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/recipe-log")
	if readErr != nil || string(data) != "xx" { t.Error("newer input did not rerun recipe") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDefinitionExpressionSuppliesInput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "SRC = ./input\n./output : @(SRC)\n\tcp @< @>\n\tprintf x >> recipe-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	first := compiled.Program.Materialize("./output")
	second := compiled.Program.Materialize("./output")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" || !second.Fresh { t.Error("expression input freshness failed") }
	first.Free(a); second.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "source" { t.Error("expression input did not render") }
	mem.FreeSlice(a, data)
	log, logErr := os.ReadFile(a, dir+"/recipe-log")
	if logErr != nil || string(log) != "x" { t.Error("fresh expression input reran recipe") }
	mem.FreeSlice(a, log)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDefinitionHostReadFailureReleasesDiagnosticOnce(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "value = (read \"missing-file\")\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("value")
	if result.Diagnostic.Code != "FS_ERR" { t.Errorf("diagnostic = %s, want FS_ERR", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestYieldWritesFileOutput(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "./output :\n\t@(yield \"yielded\")\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("yield failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "yielded" { t.Error("yield did not write output") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestOutAndErrEmitEvents(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "run :\n\t@(out \"out\")\n\t@(err \"err\")\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("effects failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	seenOut, seenErr := false, false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.Kind == program.Stdout && next.Event.Target == "run" && string(next.Event.Data) == "out" { seenOut = true }
		if next.Event.Kind == program.Stderr && next.Event.Target == "run" && string(next.Event.Data) == "err" { seenErr = true }
		next.Event.Free(a)
	}
	if !seenOut || !seenErr { t.Error("out and err events were not emitted with target identity") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestOperationDependencyEmitsEvent(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "input :\n\ttrue\noutput : input\n\t@(depends \"x\")\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "depends", Call: observeDependency, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { if len(compiled.Diagnostics) != 0 { t.Errorf("compile failed: %s %s", compiled.Diagnostics[0].Code, compiled.Diagnostics[0].Message) } else { t.Error("compile failed") }; return }
	result := compiled.Program.Materialize("output")
	if result.Diagnostic.Code != "" { t.Errorf("dependency operation failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	seen := false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.Kind == program.DependencyDiscovered && next.Event.Target == "output" && next.Event.DependencyKey.Name == "input" { seen = true }
		next.Event.Free(a)
	}
	if !seen { t.Error("dependency operation did not emit discovery event") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func containsSubstring(text string, sub string) bool {
	if len(sub) == 0 || len(sub) > len(text) { return false }
	for i := 0; i+len(sub) <= len(text); i++ {
		match := true
		for j := 0; j < len(sub); j++ { if text[i+j] != sub[j] { match = false; break } }
		if match { return true }
	}
	return false
}

func TestYieldRejectsShellCommand(t *testing.T) {
	a := t.Allocator()
	source := "./output :\n\t@(yield \"content\")\n\ttrue\n"
	parsed := script.Parse(a, "test.lmk", source)
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "OUTPUT_CONFLICT" { t.Errorf("yield conflict = %s", result.Diagnostic.Code) }
	if result.Diagnostic.Span.Start <= 0 || result.Diagnostic.Span.End > len(source) || !containsSubstring(source[result.Diagnostic.Span.Start:result.Diagnostic.Span.End], "yield") {
		t.Errorf("yield conflict did not record the yield source span: %d..%d", result.Diagnostic.Span.Start, result.Diagnostic.Span.End)
	}
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestBuildEffectInExpressionInputIsPhaseInvalid(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "NAME = ./input\n./output : @(out NAME)\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "PHASE_INVALID" { t.Errorf("planning effect diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestServiceIsExplicitlyUnsupported(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "service daemon :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("daemon")
	if result.Diagnostic.Code != "FEATURE_UNSUP" { t.Errorf("service diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMissingBareInputFails(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "run : missing\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "TGT_NO_RULE" { t.Errorf("missing input diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestFileDependencyRunsProducerBeforeDependent(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/src", []byte("source"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./mid : ./src\n\tcp @< @>\n./out : ./mid\n\tcp @< @>\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./out")
	if result.Diagnostic.Code != "" { t.Errorf("dependency materialization failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	seenDependency := false
	for { next := compiled.Program.NextEvent(); if !next.OK { break }; if next.Event.Kind == program.DependencyDiscovered { seenDependency = true }; next.Event.Free(a) }
	if !seenDependency { t.Error("dependency discovery event was not emitted") }
	data, readErr := os.ReadFile(a, dir+"/out")
	if readErr != nil || string(data) != "source" { t.Error("file producer was not scheduled before dependent") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestEmptyFileRecipeMissingOutputFails(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "./missing :\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./missing")
	if result.Diagnostic.Code != "OUTPUT_MISSING" { t.Errorf("empty recipe diagnostic = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestRenderedDuplicateOutputsAreRejected(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "./out/{name} ./out/{name} :\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./out/x")
	if planned.Diagnostic.Code != "PARSE_ERR" { t.Errorf("duplicate output diagnostic = %s", planned.Diagnostic.Code) }
	planned.Diagnostic.Free(a)
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestRecipeFailureRetainsBodySpan(t *testing.T) {
	a := t.Allocator()
	source := "./output :\n\tfalse\n"
	parsed := script.Parse(a, "test.lmk", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "RECIPE_FAIL" || result.Diagnostic.Span.Start <= 0 || result.Diagnostic.Span.End > len(source) { t.Errorf("recipe failure span = %s %d..%d", result.Diagnostic.Code, result.Diagnostic.Span.Start, result.Diagnostic.Span.End) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanFlattensComputedInputExpression(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "SRC = ./src\nOTHER = ./other\n./output : @((list SRC (list OTHER :nil)))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 2 || len(planned.Plan.ResourceInputs) != 2 || planned.Plan.Inputs[0] != "./src" || planned.Plan.Inputs[1] != "./other" { t.Error("plan did not flatten computed input expression") }
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanOnlyResolvesSelectedFallbackInput(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "SRC = ./src\n./output : @((? SRC missing))\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "" || len(planned.Plan.Inputs) != 1 || planned.Plan.Inputs[0] != "./src" { t.Error("plan evaluated an unselected fallback input") }
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestMaterializeReevaluatesComputedInputsWithoutDuplication(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/src", []byte("a"), 0o644) != nil || os.WriteFile(dir+"/other", []byte("b"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "SRC = ./src\nOTHER = ./other\n./output : @((list SRC OTHER))\n\tcat @<* > @>\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "ab" { t.Error("computed inputs were not flattened for execution") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestPlanReportsDefinitionCycleInInput(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "A = \"@(B)\"\nB = \"@(A)\"\n./output : @(A)\n\ttrue\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	planned := compiled.Program.Plan("./output")
	if planned.Diagnostic.Code != "DEP_CYCLE" { t.Errorf("cycle diagnostic = %s", planned.Diagnostic.Code) }
	planned.Diagnostic.Free(a)
	planned.Plan.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestTargetEventsCarrySharedIdentity(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "run :\n\t@(nop \"\")\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	var nodeID int64
	started, value, completed := false, false, false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.NodeID == 0 || next.Event.Key.Name == "" { t.Error("event lacked node identity") }
		if nodeID == 0 { nodeID = next.Event.NodeID }
		if next.Event.NodeID != nodeID { t.Error("target events did not share node identity") }
		if next.Event.Kind == program.TargetStarted { started = true }
		if next.Event.Kind == program.TargetValue { value = true }
		if next.Event.Kind == program.TargetCompleted { completed = true }
		next.Event.Free(a)
	}
	if !started || !value || !completed { t.Error("target lifecycle events were incomplete") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCompileManyQualifiesDiagnostics(t *testing.T) {
	a := t.Allocator()
	registry := eval.NewRegistry(a)
	compiled := program.CompileMany(a, []program.CompileSource{{Name: "first.lmk", Text: "value = \"ok\""}, {Name: "second.lmk", Text: "(broken"}}, registry, program.Options{Directory: "."})
	if len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Source != "second.lmk" { t.Error("multi-source diagnostic lost its source name") }
	if compiled.Program != nil { compiled.Program.Free() }
	compiled.Free(a)
	registry.Free()
}

func TestSharedHandlesCancelOnlyAfterLastRelease(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.lmk", "run :\n\tsleep 1\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Start("run")
	second := compiled.Program.Start("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Fatal("start failed"); return }
	compiled.Program.Tick(0)
	first.Handle.Cancel()
	compiled.Program.Tick(10)
	if second.Handle.Node.State == core.NodeCancelled { t.Error("releasing one shared handle cancelled the process") }
	second.Handle.Cancel()
	for i := 0; i < 30 && compiled.Program.Host.Active() != 0; i++ { compiled.Program.Tick(10) }
	if compiled.Program.Host.Active() != 0 { t.Error("last handle did not cancel the process group") }
	first.Handle.Free(); second.Handle.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestDynamicExternalFileDependencyIsCurrent(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output :\n\t@(depends-file \"./input\")\n\tprintf result > @>\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "depends-file", Call: observeFileDependency, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("dynamic file dependency failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	second := compiled.Program.Materialize("./output")
	if second.Diagnostic.Code != "" || !second.Fresh { t.Errorf("dynamic file dependency did not preserve freshness: %s %t", second.Diagnostic.Code, second.Fresh) }
	second.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestWriteOperationDefersUntilExecution(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.lmk", "./output :\n\t@(write \"./output\" \"written\")\n")
	registry := eval.NewRegistry(a)
	lib.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, Grants: []eval.Grant{{Capability: eval.Write, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("write failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "written" { t.Error("write did not commit") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}


func TestReadOperationResumesDuringRendering(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("abc"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output :\n\t@(yield (str (count (read \"./input\"))))\n")
	registry := eval.NewRegistry(a)
	lib.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("read failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "3" { t.Error("read result did not resume into yield") }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestWildcardRelativePathDoesNotLeak(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("abc"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output :\n\t@(yield (str (wildcard \"./*\")))\n")
	registry := eval.NewRegistry(a)
	lib.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "" { t.Errorf("wildcard failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/output")
	if readErr != nil || string(data) != "[\"./input\"]" { t.Errorf("wildcard result = %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestRenderFreesLineSpansWhenLaterLineWaits(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("abc"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output :\n\tkept\n\t@(yield (str (count (read \"./input\"))))\n")
	registry := eval.NewRegistry(a)
	lib.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "OUTPUT_CONFLICT" { t.Errorf("yield conflict = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func cacheManifestLength(t *testing.T, a mem.Allocator, dir string) int {
	entries, err := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if err != nil || len(entries) != 1 { t.Fatal("cache record missing"); return 0 }
	path := dir + "/.littlemake/cache/tasks/" + entries[0].Name
	os.FreeDirEntry(a, entries)
	data, readErr := os.ReadFile(a, path)
	if readErr != nil || len(data) < 13 { t.Fatal("cache record unreadable"); return 0 }
	identityLength := uint64(0)
	for i := 0; i < 8; i++ { identityLength |= uint64(data[5+i]) << uint(i*8) }
	manifestLengthAt := 5 + 8 + int(identityLength) + 32 + 1 + 32
	if manifestLengthAt+8 > len(data) { mem.FreeSlice(a, data); t.Fatal("manifest length missing"); return 0 }
	n := uint64(0)
	for i := 0; i < 8; i++ { n |= uint64(data[manifestLengthAt+i]) << uint(i*8) }
	mem.FreeSlice(a, data)
	return int(n)
}

func clearTaskCache(a mem.Allocator, dir string) {
	entries, err := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if err != nil { return }
	for i := range entries { os.Remove(dir + "/.littlemake/cache/tasks/" + entries[i].Name) }
	os.FreeDirEntry(a, entries)
}

func cacheWarningCount(a mem.Allocator, compiled *program.Program) int {
	warnings := 0
	for {
		next := compiled.NextEvent()
		if !next.OK { break }
		if next.Event.Diagnostic.Code == "CACHE_UNUSABLE" { warnings++ }
		next.Event.Free(a)
	}
	return warnings
}

func isCacheRecordName(name string) bool {
	if len(name) != 69 || name[64:] != ".lmkr" { return false }
	for i := 0; i < 64; i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') { return false }
	}
	return true
}

func finishMaterialize(t *testing.T, a mem.Allocator, compiled *program.Program, target string, label string) program.Result {
	started := compiled.Start(target)
	if started.Diagnostic.Code != "" { return program.Result{Diagnostic: started.Diagnostic} }
	if started.Handle == nil { t.Fatal("materialize did not start"); return program.Result{} }
	for i := 0; i < 2000; i++ {
		wait := 0
		if i%25 == 24 { wait = 5 }
		compiled.Tick(wait)
		poll := started.Handle.Poll()
		if poll.Done { started.Handle.Free(); return poll.Result }
	}
	state := started.Handle.Node.State
	notes := ""
	for {
		next := compiled.NextEvent()
		if !next.OK { break }
		if next.Event.Diagnostic.Code != "" { notes += next.Event.Diagnostic.Code + " " + next.Event.Diagnostic.Message + "; " }
		next.Event.Free(a)
	}
	active := 0
	if compiled.Host != nil { active = compiled.Host.Active() }
	t.Fatalf("materialize timed out (%s): state %d active %d events %s", label, int(state), active, notes)
	return program.Result{}
}

func TestCachedTaskManifestOverflowFromNestedDefinition(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-overflow-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	// nop observes the definition without interpolating it into the shell script.
	// The nested @(mid)/@(leaf) chain is what must consume the manifest budget.
	parsed := script.Parse(a, "overflow.lmk", "leaf = \"nested-definition-value\"\nmid = \"@(leaf)\"\nouter = \"@(mid)\"\ntask run :\n\t@(nop outer)\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	if !lib.Register(registry) { t.Fatal("library registration failed"); return }
	measured := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if measured.Program == nil { t.Fatal("measure compile failed"); return }
	first := finishMaterialize(t, a, measured.Program, "run", "measure")
	if t.Failed() { first.Free(a); measured.Program.Free(); measured.Free(a); parsed.Free(); registry.Free(); return }
	if first.Diagnostic.Code != "" { t.Fatalf("measure run failed: %s", first.Diagnostic.Code) }
	first.Free(a)
	limit := cacheManifestLength(t, a, dir)
	measured.Program.Free(); measured.Free(a)
	if t.Failed() { parsed.Free(); registry.Free(); return }
	if limit < 2 { t.Fatal("manifest length was not measured"); return }
	clearTaskCache(a, dir)
	os.Remove(dir + "/task-log")
	under := program.Compile(a, parsed, registry, program.Options{Directory: dir, CacheManifestMax: limit})
	if under.Program == nil { t.Fatal("under-limit compile failed"); return }
	underFirst := finishMaterialize(t, a, under.Program, "run", "under-1")
	if t.Failed() { underFirst.Free(a); under.Program.Free(); under.Free(a); parsed.Free(); registry.Free(); return }
	if underFirst.Diagnostic.Code != "" { t.Errorf("under-limit task failed: %s", underFirst.Diagnostic.Code) }
	underFirst.Free(a)
	under.Program.Free(); under.Free(a)
	hit := program.Compile(a, parsed, registry, program.Options{Directory: dir, CacheManifestMax: limit})
	if hit.Program == nil { t.Fatal("hit compile failed"); return }
	underSecond := finishMaterialize(t, a, hit.Program, "run", "under-2")
	if t.Failed() { underSecond.Free(a); hit.Program.Free(); hit.Free(a); parsed.Free(); registry.Free(); return }
	if underSecond.Diagnostic.Code != "" { t.Errorf("under-limit hit failed: %s", underSecond.Diagnostic.Code) }
	underSecond.Free(a)
	hit.Program.Free(); hit.Free(a)
	underLog, underErr := os.ReadFile(a, dir+"/task-log")
	if underErr != nil || string(underLog) != "x" { t.Errorf("manifest at the limit did not cache: %s", string(underLog)) }
	mem.FreeSlice(a, underLog)
	clearTaskCache(a, dir)
	os.Remove(dir + "/task-log")
	over := program.Compile(a, parsed, registry, program.Options{Directory: dir, CacheManifestMax: limit - 1, Verbose: true})
	if over.Program == nil { t.Fatal("over-limit compile failed"); return }
	overRun := finishMaterialize(t, a, over.Program, "run", "over")
	if t.Failed() { overRun.Free(a); over.Program.Free(); over.Free(a); parsed.Free(); registry.Free(); return }
	if overRun.Diagnostic.Code != "" { t.Errorf("overflow should still execute: %s", overRun.Diagnostic.Code) }
	overRun.Free(a)
	if warnings := cacheWarningCount(a, over.Program); warnings != 1 { t.Errorf("overflow warning count = %d, want 1", warnings) }
	overLog, overErr := os.ReadFile(a, dir+"/task-log")
	if overErr != nil || string(overLog) != "x" { t.Errorf("overflow did not execute the recipe: %s", string(overLog)) }
	mem.FreeSlice(a, overLog)
	entries, listErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if listErr == nil {
		if len(entries) != 0 { t.Error("overflow wrote a cache record") }
		os.FreeDirEntry(a, entries)
	}
	over.Program.Free(); over.Free(a)
	parsed.Free(); registry.Free()
}

func TestCachedTaskMissingPathInvalidatesWhenItAppears(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-missing-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "missing.lmk", "task run :\n\t@(observe-file \"./absent\")\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "observe-file", Call: observeFileDependency, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, Verbose: true})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run")
	second := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" || second.Diagnostic.Code != "" { t.Error("missing path dependency failed") }
	first.Free(a); second.Free(a)
	if warnings := cacheWarningCount(a, compiled.Program); warnings != 0 { t.Errorf("missing path was unusable: %d warnings", warnings) }
	if os.WriteFile(dir+"/absent", []byte("now"), 0o644) != nil { t.Fatal("absent path creation failed"); return }
	third := compiled.Program.Materialize("run")
	if third.Diagnostic.Code != "" { t.Errorf("appearance rerun failed: %s", third.Diagnostic.Code) }
	third.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Errorf("missing path appearance did not invalidate the cache: %s", string(data)) }
	mem.FreeSlice(a, data)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachedTaskSymlinkDependencyIsUnusable(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-symlink-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/target", []byte("data"), 0o644) != nil { t.Fatal("symlink target write failed"); return }
	if os.Symlink("target", dir+"/link") != nil { t.Fatal("symlink creation failed"); return }
	parsed := script.Parse(a, "symlink.lmk", "task run : ./link\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, Verbose: true})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run")
	if first.Diagnostic.Code != "" { t.Fatalf("symlink task failed: %s", first.Diagnostic.Code) }
	first.Free(a)
	if warnings := cacheWarningCount(a, compiled.Program); warnings != 1 { t.Errorf("symlink warning count = %d, want 1", warnings) }
	second := compiled.Program.Materialize("run")
	if second.Diagnostic.Code != "" { t.Errorf("second symlink run failed: %s", second.Diagnostic.Code) }
	second.Free(a)
	if warnings := cacheWarningCount(a, compiled.Program); warnings != 1 { t.Errorf("second symlink warning count = %d, want 1", warnings) }
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data) != "xx" { t.Errorf("symlink dependency reused a cache result: %s", string(data)) }
	mem.FreeSlice(a, data)
	entries, listErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if listErr == nil {
		if len(entries) != 0 { t.Error("symlink dependency wrote a cache record") }
		os.FreeDirEntry(a, entries)
	}
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachePathIgnoresRawTargetText(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-cache-path-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "path.lmk", "task {name} :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	targets := []string{"..", "a:b", "....", "back\\slash", "odd-\xff"}
	for i := range targets {
		result := compiled.Program.Materialize(targets[i])
		if result.Diagnostic.Code != "" { t.Fatalf("target %q failed: %s", targets[i], result.Diagnostic.Code) }
		result.Free(a)
	}
	entries, listErr := os.ReadDir(a, dir+"/.littlemake/cache/tasks")
	if listErr != nil || len(entries) != len(targets) { t.Fatalf("cache records = %d, want %d", len(entries), len(targets)) }
	for i := range entries {
		if !isCacheRecordName(entries[i].Name) { t.Errorf("cache record escaped hashed name: %s", entries[i].Name) }
	}
	os.FreeDirEntry(a, entries)
	cacheEntries, cacheErr := os.ReadDir(a, dir+"/.littlemake/cache")
	if cacheErr != nil || len(cacheEntries) != 1 || cacheEntries[0].Name != "tasks" { t.Error("cache directory contains a path derived from raw target text") }
	if cacheErr == nil { os.FreeDirEntry(a, cacheEntries) }
	rootEntries, rootErr := os.ReadDir(a, dir+"/.littlemake")
	if rootErr != nil || len(rootEntries) != 1 || rootEntries[0].Name != "cache" { t.Error("littlemake directory contains a path derived from raw target text") }
	if rootErr == nil { os.FreeDirEntry(a, rootEntries) }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestYieldBufferFreedWhenLaterWriteFails(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "littlemake-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/not-a-dir", []byte("file"), 0o644) != nil { t.Fatal("blocker write failed"); return }
	parsed := script.Parse(a, "test.lmk", "./output :\n\t@(yield \"data\")\n\t@(write \"./not-a-dir/child\" \"x\")\n")
	registry := eval.NewRegistry(a)
	lib.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: dir, Grants: []eval.Grant{{Capability: eval.Write, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "FS_ERR" { t.Errorf("write failure = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}
