package program_test

import (
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/os"
	"solod.dev/so/testing"
	"solod.dev/so/time"
)

func TestNewerInputsUseSuccessfulContentRecord(t *testing.T) {
	var buffer [os.MaxPathLen]byte
	dir, err := os.MkdirTemp(buffer[:], "", "kame-newer-inputs-")
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
	source := "(newer UNUSED) = @<?\n./output : ./a named ./b ./a | ./order\n\tprintf '%s\\n' @(newer :nil) > @>\nnamed :\n\ttrue\n"
	code, _ := materializeSource(t, dir, source, false, false)
	if code != "" || !tempMatches(t, dir+"/output", "./a\n./b\n") {
		t.Fatal("existing output without a record did not select unique normal files")
		return
	}
	if os.WriteFile(dir+"/b", []byte("changed"), 0o644) != nil || os.Chtimes(dir+"/b", stamp, stamp) != nil {
		t.Fatal("input rewrite failed")
		return
	}
	code, _ = materializeSource(t, dir, source, false, false)
	if code != "" || !tempMatches(t, dir+"/output", "./b\n") {
		t.Error("changed bytes with unchanged older mtime were not selected")
	}
	stamp = time.Unix(4000000000, 0)
	if os.Chtimes(dir+"/a", stamp, stamp) != nil || os.Chtimes(dir+"/b", stamp, stamp) != nil {
		t.Fatal("input touch failed")
		return
	}
	code, _ = materializeSource(t, dir, source, false, true)
	if code != "" || !tempMatches(t, dir+"/output", "\n") {
		t.Error("force or newer mtime invented changed content")
	}
}

func materializeNewerKash(t *testing.T, directory string, text string) string {
	a := t.Allocator()
	parsed := script.Parse(a, "newer.kmk", text)
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), ResolveTool: posix.ResolveTool, Directory: directory, Grants: []eval.Grant{{Capability: eval.Read, Names: []string{"."}}, {Capability: eval.Write, Names: []string{"."}}, {Capability: eval.Run}}})
	defer compiled.Free(a)
	if compiled.Program == nil { t.Fatal("Kash fixture did not compile"); return "COMPILE_FAILED" }
	defer compiled.Program.Free()
	result := compiled.Program.Materialize("./output")
	code := copyText(a, result.Diagnostic.Code)
	result.Free(a)
	return code
}

func TestKashNewerInputsSurviveSuspendedFunctionRead(t *testing.T) {
	var buffer [os.MaxPathLen]byte
	dir, err := os.MkdirTemp(buffer[:], "", "kame-kash-newer-inputs-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	if os.WriteFile(dir+"/a", []byte("a"), 0o644) != nil || os.WriteFile(dir+"/b", []byte("b"), 0o644) != nil || os.WriteFile(dir+"/order", []byte("order"), 0o644) != nil {
		t.Fatal("input write failed")
		return
	}
	source := "(newer UNUSED) = (if (read \"./order\") @<? @<?)\nSELECTED = (newer :nil)\n./output : ./a ./b ./a | ./order ; [shell: kash]\n\tprintf \"%s\\n\" $SELECTED > ./output\n"
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
