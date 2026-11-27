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

func TestNativeKashSourceRereadsRevisedFile(t *testing.T) {
	dir := t.TempDir()
	nativeCLIWrite(t, filepath.Join(dir, "profile.kash"), `set env/KASH_SOURCE_VALUE = "before"`+"\n")
	t.Setenv("KASH_SOURCE_VALUE", "initial")
	t.Setenv("KASH_INIT", "")
	code := `source ./profile.kash
source ./profile.kash
printenv KASH_SOURCE_VALUE
/bin/echo 'set env/KASH_SOURCE_VALUE = "after"' > ./profile.kash
source ./profile.kash
printenv KASH_SOURCE_VALUE
`
	var out, errOut bytes.Buffer
	runNativeCLIIn(t, dir, []string{"do", "run", "-i"}, strings.NewReader(code), &out, &errOut)
	if out.String() != "before\nafter\n" || errOut.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestNativeKashSourcePublishesDefinitions(t *testing.T) {
	dir := t.TempDir()
	nativeCLIWrite(t, filepath.Join(dir, "lib.kash"), `let greeting = args.0
def suffix = greeting
def hello name = str suffix name
out! (hello "self") "\n"
`)
	nativeCLIWrite(t, filepath.Join(dir, "outer.kash"), `source ./lib.kash -- $...args
out! (hello "nested") "\n"
def app name = hello name
`)
	nativeCLIWrite(t, filepath.Join(dir, "bad.kash"), `def app name = "bad"
def leaked () = "bad"
false
`)
	init := filepath.Join(dir, "init.kash")
	nativeCLIWrite(t, init, "source ./outer.kash -- \"init \"\n")
	t.Setenv("KASH_INIT", init)
	code := `out! (app "prompt") "\n"
def callapp prefix
	source ./outer.kash -- $prefix
	return app

let saved = callapp "local "
out! (saved "escaped") "\n"
source ./bad.kash ?
out! (app "unchanged") "\n"
(leaked ())
source ./outer.kash -- (42)
out! (app "again") "\n"
out! (saved "still") "\n"
`
	var out, errOut bytes.Buffer
	runNativeCLIIn(t, dir, []string{"do", "run", "-i"}, strings.NewReader(code), &out, &errOut)
	want := "init self\ninit nested\ninit prompt\nlocal self\nlocal nested\nlocal escaped\ninit unchanged\n42self\n42nested\n42again\nlocal still\n"
	if out.String() != want || strings.Count(errOut.String(), "error[") != 1 || !strings.Contains(errOut.String(), "unknown operation: leaked") {
		t.Fatalf("stdout=%q, want %q; stderr=%q", out.String(), want, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	status := runNativeCLIIn(t, dir, []string{"do", "run", "--lang", "kash", "-c", `def callapp ()
	source ./outer.kash -- $(printf "direct ")
	out! (app "caller") "\n"
callapp
`}, nil, &out, &errOut)
	if status != 0 || out.String() != "direct self\ndirect nested\ndirect caller\n" || errOut.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}

func TestNativeKashSourceOutsideDefaultReadGrant(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(root, "profile.kash"), "def greet () = \"loaded\"\n")
	t.Setenv("KASH_INIT", "")
	for _, tc := range []struct {
		name  string
		flags []string
		code  string
		want  string
	}{
		{"default", nil, `source ../profile.kash; out! (greet ())`, "loaded"},
		{"restricted", []string{"--allow-read=."}, `source ../profile.kash`, ""},
		{"opt-out", []string{"--no-default-grants"}, `source ../profile.kash`, ""},
		{"ordinary read", nil, `(read ../profile.kash)`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := append([]string{"do", "run", "--lang", "kash"}, tc.flags...)
			status := runNativeCLIIn(t, work, append(args, "-c", tc.code), nil, &out, &errOut)
			if tc.want != "" {
				if status != 0 || out.String() != tc.want || errOut.Len() != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
				}
			} else if status != 1 || !strings.Contains(errOut.String(), "[CAP_DENIED]") {
				t.Fatalf("status=%d stderr=%q, want CAP_DENIED", status, errOut.String())
			}
		})
	}
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, root, []string{"do", "run", "-i"}, strings.NewReader("cd work\nsource ../profile.kash\nout! (greet ())\n"), &out, &errOut)
	if status != 0 || out.String() != "loaded" || errOut.Len() != 0 {
		t.Fatalf("after cd: status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
}
