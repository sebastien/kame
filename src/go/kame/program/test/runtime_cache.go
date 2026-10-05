package program_test

import (
	"kame/core"
	"kame/host/posix"
	"kame/host/wasm"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/math"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
	"solod.dev/so/time"
)

func cacheVersionOperation(context *eval.Context, state any, values []core.Value) eval.Result {
	_, _, _ = context, state, values
	return eval.Result{Value: core.Value{Kind: core.Nil}}
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

func cleanupFingerprintSubdir(a mem.Allocator, subdir string) {
	entries,err:=os.ReadDir(a,subdir)
	if err==nil { for i:=range entries { _=os.Remove(subdir+"/"+entries[i].Name) };os.FreeDirEntry(a,entries) }
	_ = os.Remove(subdir)
}

func cleanupFingerprintVector(a mem.Allocator) {
	const dir="/tmp/kame-cache-fingerprint-vector"
	cleanupFingerprintSubdir(a,dir+"/.kame/cache/tasks")
	cleanupFingerprintSubdir(a,dir+"/.kame/cache/locks")
	_ = os.Remove(dir+"/.kame/cache")
	_ = os.Remove(dir+"/.kame")
	_ = os.Remove(dir)
}

func TestCompleteTaskFingerprintVector(t *testing.T) {
	a:=t.Allocator()
	const dir="/tmp/kame-cache-fingerprint-vector"
	cleanupFingerprintVector(a)
	if os.Mkdir(dir,0o755)!=nil { t.Fatal("cannot create fingerprint vector directory");return }
	parsed:=script.Parse(a,"vector.kmk","task cache-vector :\n\tprintf vector\n")
	registry:=eval.NewRegistry(a)
	compiled:=program.Compile(a,parsed,registry,program.Options{Host: posix.New(a), Directory:dir,Shell:[]string{"/bin/sh","-c"},Environment:[]string{"PATH=/usr/bin:/bin"}})
	if compiled.Program==nil { t.Fatal("vector task compile failed");return }
	result:=compiled.Program.Materialize("cache-vector")
	if result.Diagnostic.Code!="" { t.Fatalf("vector task failed: %s",result.Diagnostic.Code) }
	result.Free(a)
	entries,err:=os.ReadDir(a,dir+"/.kame/cache/tasks")
	if err!=nil||len(entries)!=1 { t.Fatal("expected exactly one task record");return }
	data,err:=os.ReadFile(a,dir+"/.kame/cache/tasks/"+entries[0].Name)
	os.FreeDirEntry(a,entries)
	if err!=nil||len(data)<46 { t.Fatal("task record could not be read");return }
	identityLength:=uint64(0);for i:=0;i<8;i++{identityLength|=uint64(data[5+i])<<uint(i*8)}
	fingerprintAt:=13+int(identityLength)
	if fingerprintAt+32>len(data) { mem.FreeSlice(a,data);t.Fatal("record fingerprint is truncated");return }
	fingerprint:=program.FingerprintHex(data[fingerprintAt:fingerprintAt+32])
	if fingerprint!="3473ede1d722e4e6472667839964f86b68e4ab6102f41613cf51b76fa3644345" { t.Errorf("complete task fingerprint = %s",fingerprint) }
	mem.FreeString(mem.System,fingerprint);mem.FreeSlice(a,data)
	compiled.Program.Free();compiled.Free(a);parsed.Free();registry.Free()
	entries,err=os.ReadDir(a,dir+"/.kame/cache/tasks")
	if err==nil { for i:=range entries { _=os.Remove(dir+"/.kame/cache/tasks/"+entries[i].Name) };os.FreeDirEntry(a,entries) }
	cleanupFingerprintVector(a)
}

func TestCachedTaskHits(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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

func TestCachedTaskUsesPortableMemoryBackend(t *testing.T) {
	a := t.Allocator()
	host := wasm.NewMemoryHost(a)
	parsed := script.Parse(a, "memory-cache.kmk", "task cached :\n\t@(out \"memory-hit\")\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) {
		t.Fatal("library registration failed")
		return
	}
	compiled := program.Compile(a, parsed, registry, program.Options{Host: host, Directory: "."})
	if compiled.Program == nil {
		t.Fatal("memory-backed program compile failed")
		return
	}
	first := compiled.Program.Materialize("cached")
	first.Free(a)
	for {
		next := compiled.Program.NextEvent()
		if !next.OK {
			break
		}
		next.Event.Free(a)
	}
	second := compiled.Program.Materialize("cached")
	second.Free(a)
	cachedOutput := false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK {
			break
		}
		if next.Event.Cached && next.Event.Kind == program.Stdout && string(next.Event.Data) == "memory-hit" {
			cachedOutput = true
		}
		next.Event.Free(a)
	}
	entries, err := host.ReadDir(a, ".kame/cache/tasks")
	if err != nil || len(entries) != 1 {
		t.Error("portable in-memory backend did not publish one task record")
	}
	for i := range entries {
		mem.FreeString(a, entries[i].Name)
	}
	mem.FreeSlice(a, entries)
	if !cachedOutput {
		t.Error("portable in-memory backend did not return the completed cache record")
	}
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
}

func TestCachedTaskInvalidatesForDeclaredFile(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("one"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "task run : ./input\n\tcat ./input >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-kind-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/input", 0o755) != nil { t.Fatal("input directory creation failed"); return }
	parsed := script.Parse(a, "test.kmk", "task run : ./input\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Verbose: true})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-mtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("same"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", "task run : ./input\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tprintf out; printf err >&2\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\t@(out \"out\")\n\t@(err \"err\")\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); first.Free(a)
	entries, readErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if readErr != nil || len(entries) != 1 { t.Fatal("cache record was not written"); return }
	if os.WriteFile(dir+"/.kame/cache/tasks/"+entries[0].Name, []byte("broken"), 0o644) != nil { t.Fatal("cache corruption failed"); return }
	os.FreeDirEntry(a, entries)
	second := compiled.Program.Materialize("run"); second.Free(a)
	data, dataErr := os.ReadFile(a, dir+"/task-log")
	if dataErr != nil || string(data) != "xx" { t.Error("malformed cache record was not treated as a miss") }
	mem.FreeSlice(a, data)
	// A malformed record is silent unless verbose diagnostics were requested.
	compiled.Program.Free(); compiled.Free(a)
	entries, readErr = os.ReadDir(a, dir+"/.kame/cache/tasks")
	if readErr != nil || len(entries)!=1 { t.Fatal("cache record was not replaced"); return }
	verboseCorruptPath:=dir+"/.kame/cache/tasks/"+entries[0].Name
	os.FreeDirEntry(a,entries)
	if os.WriteFile(verboseCorruptPath,[]byte("broken"),0o644)!=nil { t.Fatal("cache corruption failed"); return }
	verbose := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Verbose: true})
	if verbose.Program == nil { t.Fatal("verbose compile failed"); return }
	verboseResult := verbose.Program.Materialize("run"); verboseResult.Free(a)
	warningFound := false
	for { next:=verbose.Program.NextEvent(); if !next.OK { break }; if next.Event.Kind==program.CacheWarning && next.Event.Diagnostic.Code=="CACHE_RECORD" { warningFound=true }; next.Event.Free(a) }
	if !warningFound { t.Error("verbose mode omitted malformed-record warning") }
	verbose.Program.Free(); verbose.Free(a)
	// Write another malformed record and ensure default mode remains silent.
	entries, readErr = os.ReadDir(a, dir+"/.kame/cache/tasks")
	if readErr != nil || len(entries)!=1 { t.Fatal("cache record was not replaced"); return }
	cacheFile := dir+"/.kame/cache/tasks/"+entries[0].Name
	os.FreeDirEntry(a,entries)
	if os.WriteFile(cacheFile,[]byte("broken"),0o644)!=nil { t.Fatal("cache corruption failed"); return }
	quiet := program.Compile(a,parsed,registry,program.Options{Host: posix.New(a), Directory:dir})
	if quiet.Program==nil { t.Fatal("quiet compile failed"); return }
	quietResult:=quiet.Program.Materialize("run");quietResult.Free(a)
	for { next:=quiet.Program.NextEvent();if !next.OK{break};if next.Event.Kind==program.CacheWarning{t.Error("default mode emitted cache warning")};next.Event.Free(a) }
	quiet.Program.Free();quiet.Free(a)
	compiled = program.Compile(a,parsed,registry,program.Options{Host: posix.New(a), Directory:dir})
	if compiled.Program==nil { t.Fatal("final compile failed");return }
	entries, readErr = os.ReadDir(a, dir+"/.kame/cache/tasks")
	if readErr != nil || len(entries) != 1 { t.Fatal("replacement cache record was not written"); return }
	cachePath := dir+"/.kame/cache/tasks/"+entries[0].Name
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
	dir,err:=os.MkdirTemp(dirBuffer,"","kame-cache-manifest-")
	if err!=nil { t.Fatal("temporary directory failed");return }
	defer os.Remove(dir)
	parsed:=script.Parse(a,"manifest.kmk","task run :\n\tprintf x >> task-log\n")
	registry:=eval.NewRegistry(a)
	compiled:=program.Compile(a,parsed,registry,program.Options{Host: posix.New(a), Directory:dir})
	if compiled.Program==nil { t.Fatal("compile failed");return }
	first:=compiled.Program.Materialize("run");first.Free(a)
	entries,readErr:=os.ReadDir(a,dir+"/.kame/cache/tasks")
	if readErr!=nil||len(entries)!=1 { t.Fatal("cache record was not written");return }
	cachePath:=dir+"/.kame/cache/tasks/"+entries[0].Name
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\t@((versioned))\n\tprintf x >> task-log\n")
	firstRegistry := eval.NewRegistry(a)
	if !firstRegistry.Add(eval.Operation{Name: "versioned", Version: "v1", Call: cacheVersionOperation, MinArity: 0, MaxArity: 0}) { t.Fatal("operation registration failed"); return }
	firstProgram := program.Compile(a, parsed, firstRegistry, program.Options{Host: posix.New(a), Directory: dir})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a); firstRegistry.Free()
	secondRegistry := eval.NewRegistry(a)
	if !secondRegistry.Add(eval.Operation{Name: "versioned", Version: "v2", Call: cacheVersionOperation, MinArity: 0, MaxArity: 0}) { t.Fatal("operation registration failed"); return }
	secondProgram := program.Compile(a, parsed, secondRegistry, program.Options{Host: posix.New(a), Directory: dir})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-env-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tprintf '%s' \\\"$KM_CACHE_VALUE\\\" >> task-log\n")
	registry := eval.NewRegistry(a)
	firstProgram := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "KM_CACHE_VALUE=one"}})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run")
	if first.Diagnostic.Code != "" { t.Error("first cached task failed") }
	first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	secondProgram := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "KM_CACHE_VALUE=two"}})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-envdep-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\t@(out (str (env \"KM_CACHE_VALUE\")))\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	grant := []eval.Grant{{Capability: eval.Env, Names: []string{"KM_CACHE_VALUE"}}}
	firstProgram := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "KM_CACHE_VALUE=one"}, Grants: grant})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	secondProgram := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Environment: []string{"PATH=/usr/bin:/bin", "KM_CACHE_VALUE=two"}, Grants: grant})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-shell-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	firstProgram := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Shell: []string{"/bin/sh", "-c"}})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	secondProgram := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Shell: []string{"/usr/bin/env", "bash", "-c"}})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-timeout-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	firstProgram := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, TimeoutMS: 1000})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	secondProgram := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, TimeoutMS: 2000})
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
	dir,err:=os.MkdirTemp(dirBuffer,"","kame-cache-retry-option-")
	if err!=nil { t.Fatal("temporary directory failed");return }
	defer os.Remove(dir)
	parsed:=script.Parse(a,"retry.kmk","task run :\n\tprintf x >> task-log\n")
	registry:=eval.NewRegistry(a)
	firstProgram:=program.Compile(a,parsed,registry,program.Options{Host: posix.New(a), Directory:dir,RetryCount:0})
	if firstProgram.Program==nil { t.Fatal("first compile failed");return }
	first:=firstProgram.Program.Materialize("run");first.Free(a)
	firstProgram.Program.Free();firstProgram.Free(a)
	secondProgram:=program.Compile(a,parsed,registry,program.Options{Host: posix.New(a), Directory:dir,RetryCount:1})
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
	dir,err:=os.MkdirTemp(dirBuffer,"","kame-cache-retry-run-")
	if err!=nil { t.Fatal("temporary directory failed");return }
	defer os.Remove(dir)
	parsed:=script.Parse(a,"retry.kmk","task run :\n\tif [ ! -e attempted ]; then touch attempted; exit 1; fi\n\tprintf success >> task-log\n")
	registry:=eval.NewRegistry(a)
	compiled:=program.Compile(a,parsed,registry,program.Options{Host: posix.New(a), Directory:dir,RetryCount:1})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-rule-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	registry := eval.NewRegistry(a)
	firstScript := script.Parse(a, "first.kmk", "task run :\n\tprintf a >> task-log\n")
	firstProgram := program.Compile(a, firstScript, registry, program.Options{Host: posix.New(a), Directory: dir})
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run"); first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a); firstScript.Free()
	secondScript := script.Parse(a, "second.kmk", "task run :\n\tprintf b >> task-log\n")
	secondProgram := program.Compile(a, secondScript, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-capture-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run-{name} :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-output-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tprintf abcdef\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, CacheRetainBytes: 3})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-glob-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.Mkdir(dir+"/src", 0o755) != nil { t.Fatal("source directory failed"); return }
	if os.WriteFile(dir+"/src/one", []byte("1"), 0o644) != nil { t.Fatal("source write failed"); return }
	parsed := script.Parse(a, "test.kmk", "task run :\n\t@(out (str (count (wildcard \"./src/*\"))))\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	options := program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}}
	firstProgram := program.Compile(a, parsed, registry, options)
	if firstProgram.Program == nil { t.Fatal("first compile failed"); return }
	first := firstProgram.Program.Materialize("run")
	if first.Diagnostic.Code != "" { t.Errorf("first run failed: %s", first.Diagnostic.Code) }
	first.Free(a)
	firstProgram.Program.Free(); firstProgram.Free(a)
	if os.WriteFile(dir+"/src/two", []byte("2"), 0o644) != nil { t.Fatal("glob member write failed"); return }
	options.Host = posix.New(a)
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-failure-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("good"), 0o644) != nil { t.Fatal("input write failed"); return }
	parsed := script.Parse(a, "test.kmk", `task run : ./input
	if [ "$(cat ./input)" = good ]; then printf x >> task-log; else false; fi
`)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	success := compiled.Program.Materialize("run")
	if success.Diagnostic.Code != "" { t.Errorf("initial run failed: %s", success.Diagnostic.Code) }
	success.Free(a)
	entries, listErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if listErr != nil || len(entries) != 1 { t.Fatal("successful record was not written"); return }
	cachePath := dir+"/.kame/cache/tasks/"+entries[0].Name
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-oversize-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	first := compiled.Program.Materialize("run"); first.Free(a)
	entries, readErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if readErr != nil || len(entries) != 1 { t.Fatal("cache record was not written"); return }
	path := dir+"/.kame/cache/tasks/"+entries[0].Name
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-writers-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "test.kmk", "task run :\n\tsleep 0.1; printf x >> task-log\n")
	registry := eval.NewRegistry(a)
	left := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	right := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	verify := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if verify.Program == nil { t.Fatal("verification compile failed"); return }
	result := verify.Program.Materialize("run"); if result.Diagnostic.Code!="" { t.Errorf("cache record after concurrent writes was unreadable: %s",result.Diagnostic.Code) }; result.Free(a)
	data, readErr := os.ReadFile(a, dir+"/task-log")
	if readErr != nil || string(data)!="xx" { t.Errorf("concurrent cache writer left no reusable complete record: %s",string(data)) }
	mem.FreeSlice(a,data)
	verify.Program.Free(); verify.Free(a); parsed.Free(); registry.Free()
}

func cacheManifestLength(t *testing.T, a mem.Allocator, dir string) int {
	entries, err := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if err != nil || len(entries) != 1 { t.Fatal("cache record missing"); return 0 }
	path := dir + "/.kame/cache/tasks/" + entries[0].Name
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
	entries, err := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if err != nil { return }
	for i := range entries { os.Remove(dir + "/.kame/cache/tasks/" + entries[i].Name) }
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
	if len(name) != 69 || name[64:] != ".kmkr" { return false }
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-overflow-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	// nop observes the definition without interpolating it into the shell script.
	// The nested @(mid)/@(leaf) chain is what must consume the manifest budget.
	parsed := script.Parse(a, "overflow.kmk", "leaf = \"nested-definition-value\"\nmid = \"@(leaf)\"\nouter = \"@(mid)\"\ntask run :\n\t@(nop outer)\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	measured := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
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
	under := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, CacheManifestMax: limit})
	if under.Program == nil { t.Fatal("under-limit compile failed"); return }
	underFirst := finishMaterialize(t, a, under.Program, "run", "under-1")
	if t.Failed() { underFirst.Free(a); under.Program.Free(); under.Free(a); parsed.Free(); registry.Free(); return }
	if underFirst.Diagnostic.Code != "" { t.Errorf("under-limit task failed: %s", underFirst.Diagnostic.Code) }
	underFirst.Free(a)
	under.Program.Free(); under.Free(a)
	hit := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, CacheManifestMax: limit})
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
	over := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, CacheManifestMax: limit - 1, Verbose: true})
	if over.Program == nil { t.Fatal("over-limit compile failed"); return }
	overRun := finishMaterialize(t, a, over.Program, "run", "over")
	if t.Failed() { overRun.Free(a); over.Program.Free(); over.Free(a); parsed.Free(); registry.Free(); return }
	if overRun.Diagnostic.Code != "" { t.Errorf("overflow should still execute: %s", overRun.Diagnostic.Code) }
	overRun.Free(a)
	if warnings := cacheWarningCount(a, over.Program); warnings != 1 { t.Errorf("overflow warning count = %d, want 1", warnings) }
	overLog, overErr := os.ReadFile(a, dir+"/task-log")
	if overErr != nil || string(overLog) != "x" { t.Errorf("overflow did not execute the recipe: %s", string(overLog)) }
	mem.FreeSlice(a, overLog)
	entries, listErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-missing-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "missing.kmk", "task run :\n\t@(observe-file \"./absent\")\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "observe-file", Call: observeFileDependency, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Verbose: true})
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
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-symlink-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/target", []byte("data"), 0o644) != nil { t.Fatal("symlink target write failed"); return }
	if os.Symlink("target", dir+"/link") != nil { t.Fatal("symlink creation failed"); return }
	parsed := script.Parse(a, "symlink.kmk", "task run : ./link\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Verbose: true})
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
	entries, listErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if listErr == nil {
		if len(entries) != 0 { t.Error("symlink dependency wrote a cache record") }
		os.FreeDirEntry(a, entries)
	}
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestCachePathIgnoresRawTargetText(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-cache-path-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	parsed := script.Parse(a, "path.kmk", "task {name} :\n\tprintf x >> task-log\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	targets := []string{"..", "a:b", "....", "back\\slash", "odd-\xff"}
	for i := range targets {
		result := compiled.Program.Materialize(targets[i])
		if result.Diagnostic.Code != "" { t.Fatalf("target %q failed: %s", targets[i], result.Diagnostic.Code) }
		result.Free(a)
	}
	entries, listErr := os.ReadDir(a, dir+"/.kame/cache/tasks")
	if listErr != nil || len(entries) != len(targets) { t.Fatalf("cache records = %d, want %d", len(entries), len(targets)) }
	for i := range entries {
		if !isCacheRecordName(entries[i].Name) { t.Errorf("cache record escaped hashed name: %s", entries[i].Name) }
	}
	os.FreeDirEntry(a, entries)
	cacheEntries, cacheErr := os.ReadDir(a, dir+"/.kame/cache")
	if cacheErr != nil || len(cacheEntries) != 2 || cacheEntries[0].Name != "locks" || cacheEntries[1].Name != "tasks" { t.Error("cache directory contains a path derived from raw target text") }
	if cacheErr == nil { os.FreeDirEntry(a, cacheEntries) }
	rootEntries, rootErr := os.ReadDir(a, dir+"/.kame")
	if rootErr != nil || len(rootEntries) != 1 || rootEntries[0].Name != "cache" { t.Error("kame directory contains a path derived from raw target text") }
	if rootErr == nil { os.FreeDirEntry(a, rootEntries) }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestYieldBufferFreedWhenLaterWriteFails(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-runtime-")
	if err != nil { t.Fatal("temporary directory failed"); return }
	defer os.Remove(dir)
	if os.WriteFile(dir+"/not-a-dir", []byte("file"), 0o644) != nil { t.Fatal("blocker write failed"); return }
	parsed := script.Parse(a, "test.kmk", "./output :\n\t@(yield \"data\")\n\t@(write \"./not-a-dir/child\" \"x\")\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Grants: []eval.Grant{{Capability: eval.Write, Names: []string{"."}}}})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "FS_ERR" { t.Errorf("write failure = %s", result.Diagnostic.Code) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}
