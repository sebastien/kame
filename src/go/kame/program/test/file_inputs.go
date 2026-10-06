package program_test

import (
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
	"solod.dev/so/time"
)

func materializeSource(t *testing.T, dir string, source string, read bool, force bool) (string, bool) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", source)
	registry := eval.NewRegistry(a)
	if read && !operations.Register(registry) {
		t.Fatal("operation registration failed")
		return "", false
	}
	var compiled program.CompileResult
	if read {
		compiled = program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Force: force, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}}})
	} else {
		compiled = program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir, Force: force})
	}
	if compiled.Program == nil {
		t.Fatal("compile failed")
		return "", false
	}
	result := compiled.Program.Materialize("./output")
	code := copyText(a, result.Diagnostic.Code)
	fresh := result.Fresh
	result.Free(a)
	compiled.Program.Free()
	compiled.Free(a)
	parsed.Free()
	registry.Free()
	return code, fresh
}

func copyText(a mem.Allocator, value string) string {
	if value == "" {
		return ""
	}
	buf := mem.AllocSlice[byte](a, len(value), len(value))
	for i := range buf {
		buf[i] = value[i]
	}
	return string(buf)
}

func readTemp(t *testing.T, name string) string {
	a := t.Allocator()
	data, err := os.ReadFile(a, name)
	if err != nil {
		t.Fatal("read failed: " + name)
		return ""
	}
	text := copyText(mem.System, string(data))
	mem.FreeSlice(a, data)
	return text
}

func tempMatches(t *testing.T, name string, want string) bool {
	text := readTemp(t, name)
	equal := text == want
	mem.FreeString(mem.System, text)
	return equal
}

func TestFileInputsSkipShellAcrossProcesses(t *testing.T) {
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-file-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	source := "./output : ./input\n\tcp @< @>\n\tprintf x >> recipe-log\n"
	if code, _ := materializeSource(t, dir, source, false, false); code != "" {
		t.Fatal("first build failed: " + code)
		return
	}
	code, fresh := materializeSource(t, dir, source, false, false)
	if code != "" || !fresh {
		t.Errorf("unchanged invocation was not fresh: %s %t", code, fresh)
	}
	if !tempMatches(t, dir+"/recipe-log", "x") {
		t.Error("unchanged invocation reran the recipe")
	}
}

func TestFileInputsRebuildYieldDespiteOldDiscoveredMtime(t *testing.T) {
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-file-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("I"), 0o644) != nil || os.WriteFile(dir+"/extra", []byte("A"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	source := "./output : ./input\n\t@(yield (text (read \"./extra\")))\n"
	if code, _ := materializeSource(t, dir, source, true, false); code != "" {
		t.Fatal("first yield failed: " + code)
		return
	}
	if !tempMatches(t, dir+"/output", "A") {
		t.Fatal("yield output mismatch")
		return
	}
	if os.WriteFile(dir+"/extra", []byte("B"), 0o644) != nil {
		t.Fatal("extra rewrite failed")
		return
	}
	stamp := time.Unix(100, 0)
	if os.Chtimes(dir+"/extra", stamp, stamp) != nil {
		t.Fatal("mtime restore failed")
		return
	}
	code, fresh := materializeSource(t, dir, source, true, false)
	if code != "" || fresh || !tempMatches(t, dir+"/output", "B") {
		t.Errorf("changed bytes with old mtime did not rebuild yield: %s %t", code, fresh)
	}
}

func TestFileInputsRebuildWhenDiscoveredInputChanges(t *testing.T) {
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-file-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("I"), 0o644) != nil || os.WriteFile(dir+"/extra", []byte("A"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	source := "./output : ./input\n\t@(yield (text (read \"./extra\")))\n"
	if code, _ := materializeSource(t, dir, source, true, false); code != "" {
		t.Fatal("first yield failed: " + code)
		return
	}
	if os.WriteFile(dir+"/extra", []byte("B"), 0o644) != nil {
		t.Fatal("extra rewrite failed")
		return
	}
	stamp := time.Unix(1893456000, 0)
	if os.Chtimes(dir+"/extra", stamp, stamp) != nil {
		t.Fatal("mtime update failed")
		return
	}
	code, fresh := materializeSource(t, dir, source, true, false)
	if code != "" || fresh || !tempMatches(t, dir+"/output", "B") {
		t.Errorf("newer discovered input did not rebuild: %s %t", code, fresh)
	}
}

func TestFileInputsRebuildWhenMembershipChanges(t *testing.T) {
	a := t.Allocator()
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-file-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	host := posix.New(a)
	defer host.Free()
	if host.Mkdir(dir+"/src", 0o755) != nil || os.WriteFile(dir+"/src/a.txt", []byte("a"), 0o644) != nil {
		t.Fatal("source setup failed")
		return
	}
	source := "./output : @((wildcard ./src/*))\n\t@(yield (str (count @<*)))\n"
	if code, _ := materializeSource(t, dir, source, true, false); code != "" {
		t.Fatal("first membership build failed: " + code)
		return
	}
	if !tempMatches(t, dir+"/output", "1") {
		t.Fatal("first membership count mismatch")
		return
	}
	if os.WriteFile(dir+"/src/b.txt", []byte("b"), 0o644) != nil {
		t.Fatal("member add failed")
		return
	}
	code, fresh := materializeSource(t, dir, source, true, false)
	if code != "" || fresh || !tempMatches(t, dir+"/output", "2") {
		t.Fatalf("added member did not rebuild: %s %t", code, fresh)
	}
	if os.Remove(dir+"/src/b.txt") != nil {
		t.Fatal("member remove failed")
		return
	}
	code, fresh = materializeSource(t, dir, source, true, false)
	if code != "" || fresh || !tempMatches(t, dir+"/output", "1") {
		t.Errorf("removed member did not rebuild: %s %t", code, fresh)
	}
}

func TestFileInputsRebuildWhenSourceChanges(t *testing.T) {
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-file-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	first := "./output : ./input\n\tprintf x >> recipe-log\n\tprintf a > @>\n"
	if code, _ := materializeSource(t, dir, first, false, false); code != "" {
		t.Fatal("first source build failed: " + code)
		return
	}
	second := "./output : ./input\n\tprintf x >> recipe-log\n\tprintf b > @>\n"
	code, fresh := materializeSource(t, dir, second, false, false)
	if code != "" || fresh || !tempMatches(t, dir+"/recipe-log", "xx") || !tempMatches(t, dir+"/output", "b") {
		t.Errorf("changed recipe did not rebuild: %s %t", code, fresh)
	}
}

func TestFileInputsSkipKashYield(t *testing.T) {
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-file-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("I"), 0o644) != nil || os.WriteFile(dir+"/extra", []byte("A"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	source := "SHELL = kash\n./output : ./input\n\t@(yield (text (read \"./extra\")))\n"
	if code, _ := materializeSource(t, dir, source, true, false); code != "" {
		t.Fatal("kash yield failed: " + code)
		return
	}
	if os.WriteFile(dir+"/extra", []byte("B"), 0o644) != nil {
		t.Fatal("extra rewrite failed")
		return
	}
	stamp := time.Unix(100, 0)
	if os.Chtimes(dir+"/extra", stamp, stamp) != nil {
		t.Fatal("mtime restore failed")
		return
	}
	code, fresh := materializeSource(t, dir, source, true, false)
	if code != "" || fresh || !tempMatches(t, dir+"/output", "B") {
		t.Errorf("scoped yield reused changed bytes with old mtime: %s %t", code, fresh)
	}
}

func TestFileInputsAlwaysAndForceRerun(t *testing.T) {
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-file-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/input", []byte("source"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	always := "always ./output : ./input\n\tprintf x >> recipe-log\n\tprintf a > @>\n"
	if code, fresh := materializeSource(t, dir, always, false, false); code != "" || fresh {
		t.Fatal("always rule did not run: " + code)
		return
	}
	if code, fresh := materializeSource(t, dir, always, false, false); code != "" || fresh || !tempMatches(t, dir+"/recipe-log", "xx") {
		t.Errorf("always rule reused an input record: %s %t", code, fresh)
	}
	plain := "./output : ./input\n\tprintf x >> recipe-log\n\tprintf a > @>\n"
	if code, _ := materializeSource(t, dir, plain, false, false); code != "" {
		t.Fatal("plain rule failed: " + code)
		return
	}
	before := readTemp(t, dir+"/recipe-log")
	code, _ := materializeSource(t, dir, plain, false, true)
	after := readTemp(t, dir+"/recipe-log")
	if code != "" || after == before {
		t.Errorf("force did not rerun: %s before=%s after=%s", code, before, after)
	}
	mem.FreeString(mem.System, before)
	mem.FreeString(mem.System, after)
}

func TestFileInputsDemandDiscoveredGeneratedInput(t *testing.T) {
	dirBuffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(dirBuffer, "", "kame-file-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/seed", []byte("1"), 0o644) != nil || os.WriteFile(dir+"/input", []byte("I"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	source := "./extra : ./seed\n\tcp @< @>\n./output : ./input\n\t@(yield (text (read \"./extra\")))\n"
	if code, _ := materializeSource(t, dir, source, true, false); code != "" {
		t.Fatal("first generated dependency build failed: " + code)
		return
	}
	if os.WriteFile(dir+"/seed", []byte("2"), 0o644) != nil {
		t.Fatal("seed rewrite failed")
		return
	}
	stamp := time.Unix(1893456000, 0)
	if os.Chtimes(dir+"/seed", stamp, stamp) != nil {
		t.Fatal("mtime update failed")
		return
	}
	code, fresh := materializeSource(t, dir, source, true, false)
	if code != "" || fresh || !tempMatches(t, dir+"/extra", "2") || !tempMatches(t, dir+"/output", "2") {
		t.Errorf("discovered generated input was not rebuilt: %s %t", code, fresh)
	}
}
