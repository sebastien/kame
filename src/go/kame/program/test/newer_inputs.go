package programcheck

import (
	"kame/host/posix"
	"kame/internal/testing"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"os"
	"time"
)

func TestNewerInputsUseSuccessfulContentRecord(t *testing.T) {

	dir, err := os.MkdirTemp("", "kame-newer-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/a", []byte("a"), 0o644) != nil || os.WriteFile(dir+"/b", []byte("b"), 0o644) != nil || os.WriteFile(dir+"/order", []byte("order"), 0o644) != nil || os.WriteFile(dir+"/output", []byte("preexisting"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	stamp := time.Unix(100, 0)
	if os.Chtimes(dir+"/b", stamp, stamp) != nil {
		t.Fatal("input mtime setup failed")
		return
	}
	source := "rule ./output : ./a named ./b ./a | ./order = str (join \"\\n\" (map std/str changed-inputs)) \"\\n\"\ntask named\n"
	code, _ := materializeSource(t, dir, source, true, false)
	if code != "" || !tempMatches(t, dir+"/output", "./a\n./b\n") {
		t.Fatal("existing output without a record did not select unique normal files")
		return
	}
	if os.WriteFile(dir+"/b", []byte("changed"), 0o644) != nil || os.Chtimes(dir+"/b", stamp, stamp) != nil {
		t.Fatal("input rewrite failed")
		return
	}
	code, _ = materializeSource(t, dir, source, true, false)
	if code != "" || !tempMatches(t, dir+"/output", "./b\n") {
		t.Error("changed bytes with unchanged older mtime were not selected")
	}
	stamp = time.Unix(4000000000, 0)
	if os.Chtimes(dir+"/a", stamp, stamp) != nil || os.Chtimes(dir+"/b", stamp, stamp) != nil {
		t.Fatal("input touch failed")
		return
	}
	code, _ = materializeSource(t, dir, source, true, true)
	if code != "" || !tempMatches(t, dir+"/output", "\n") {
		t.Error("force or newer mtime invented changed content")
	}
}

func materializeNewerKash(t *testing.T, directory string, text string) string {
	a := t.Allocator()
	parsed := script.ParseKameFile(a, "newer.km", text)
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), ResolveTool: posix.ResolveTool, Directory: directory, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}, {Capability: eval.Write, Names: []string{"."}}, {Capability: eval.Run}}})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Fatal("Kash fixture did not compile")
		return "COMPILE_FAILED"
	}
	defer compiled.Program.Free()
	result := compiled.Program.Materialize("./output")
	code := copyText(a, result.Diagnostic.Code)
	result.Free(a)
	return code
}

func TestKashNewerInputsSurviveSuspendedFunctionRead(t *testing.T) {

	dir, err := os.MkdirTemp("", "kame-kash-newer-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/a", []byte("a"), 0o644) != nil || os.WriteFile(dir+"/b", []byte("b"), 0o644) != nil || os.WriteFile(dir+"/order", []byte("order"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	source := "def newer changed = if (read ./order) then changed else []\nrule ./output : ./a ./b ./a | ./order\n\tlet selected = newer changed-inputs\n\tprintf '%s\\n' $...selected > $outputs.0\n"
	code := materializeNewerKash(t, dir, source)
	if code != "" || !tempMatches(t, dir+"/output", "./a\n./b\n") {
		t.Fatal("Kash function lost initial newer-input frame: " + code)
		return
	}
	if os.WriteFile(dir+"/b", []byte("changed"), 0o644) != nil {
		t.Fatal("input rewrite failed")
		return
	}
	code = materializeNewerKash(t, dir, source)
	if code != "" || !tempMatches(t, dir+"/output", "./b\n") {
		t.Error("Kash function lost newer-input snapshot across suspended read")
	}
}
