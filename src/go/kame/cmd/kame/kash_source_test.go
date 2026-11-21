package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeKashSource(t *testing.T) {
	dir := t.TempDir()
	profiles := filepath.Join(dir, "profiles")
	if err := os.Mkdir(profiles, 0700); err != nil {
		t.Fatal(err)
	}
	writeReplTool(t, filepath.Join(dir, "bin"), "kash-native-source-probe", "echo from-path")
	nativeCLIWrite(t, filepath.Join(profiles, "helper.km"), "export mode\nmode = \"loaded\"\n")
	nativeCLIWrite(t, filepath.Join(profiles, ".appenv"), `import ./helper.km as config
let name = "KASH_SOURCE_MODE"
set env/KASH_SOURCE_JOURNAL = env name
set env/${name} = config/mode
set env/${if (eq? label "=") then "KASH_SOURCE_NEW" else "WRONG"} = ""
set env/PATH = str (joinpath module/dir "../bin") ":" (env "PATH")
`)
	nativeCLIWrite(t, filepath.Join(dir, "bad-reload.kash"), `source ./undo.kash
source ./profiles/.appenv
set env/KASH_SOURCE_MODE = "broken"
cd profiles
false
`)
	nativeCLIWrite(t, filepath.Join(dir, "undo.kash"), `set env/${"KASH_SOURCE_MODE"} = env "KASH_SOURCE_JOURNAL"
set env/${"KASH_SOURCE_NEW"} = nil
set env/KASH_SOURCE_JOURNAL = nil
`)
	nativeCLIWrite(t, filepath.Join(dir, "cycle.kash"), "source ./cycle.kash\n")
	init := filepath.Join(dir, "init.kash")
	nativeCLIWrite(t, init, "let label = \"=\"\ndef run-profile file\n  source $file\n")
	t.Setenv("KASH_INIT", init)
	t.Setenv("KASH_SOURCE_MODE", "original")
	code := `let profile = ./profiles/.appenv
run-profile $profile
kash-native-source-probe
source ./bad-reload.kash ?
printenv KASH_SOURCE_MODE KASH_SOURCE_NEW
run-profile ./undo.kash
printenv KASH_SOURCE_MODE KASH_SOURCE_NEW ?
source ./cycle.kash
set env/${"KASH_SOURCE_MODE"} = nil
set env/${"INVALID=NAME"} = "bad"
printenv KASH_SOURCE_MODE
`
	var out, errOut bytes.Buffer
	runNativeCLIIn(t, dir, []string{"do", "run", "-i", "--allow-run", "--allow-read", "--allow-env=PATH", "--allow-env=KASH_SOURCE_MODE", "--allow-env=KASH_SOURCE_JOURNAL"}, strings.NewReader(code), &out, &errOut)
	if want := "from-path\nloaded\n\noriginal\noriginal\n"; out.String() != want {
		t.Fatalf("stdout=%q, want %q; stderr=%q", out.String(), want, errOut.String())
	}
	for _, code := range []string{"DEP_CYCLE", "CAP_DENIED", "EXPR_INVALID"} {
		if !strings.Contains(errOut.String(), "["+code+"]") {
			t.Errorf("missing %s in stderr=%q", code, errOut.String())
		}
	}
}
