package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestNativeForeignShell(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("requires Bash")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	writeReplTool(t, filepath.Join(dir, "bin"), "kash-foreign-probe", "echo new-path")
	nativeCLIWrite(t, filepath.Join(dir, "setup.sh"), `printf 'source once\n'
declare -a counts=(0)
export BRIDGE_NEW=$'first\nsecond'
export BRIDGE_EMPTY=
export PATH="$PWD/bin:$PATH"
step() {
  counts[0]=$((counts[0] + 1))
  printf '%s:[%s]:[%s]:%s:%s\n' "${counts[0]}" "$1" "$2" "$BRIDGE_EDIT" "${PWD##*/}"
  export BRIDGE_NEW=changed
  return 7
}
drop() { unset BRIDGE_NEW BRIDGE_EMPTY BRIDGE_OLD; }
`)
	t.Setenv("BRIDGE_EDIT", "original")
	t.Setenv("BRIDGE_OLD", "old")
	t.Setenv("KASH_INIT", "")
	t.Setenv("HOME", dir)
	code := `source bash ~/setup.sh as tools unset BRIDGE_OLD
printenv BRIDGE_NEW BRIDGE_EMPTY
kash-foreign-probe
set env/BRIDGE_EDIT = "from Kash"
cd sub
tools/step "two words" "" ?
tools/step '$(not code)' "x" ?
printenv BRIDGE_NEW
tools/drop
printenv BRIDGE_NEW BRIDGE_EMPTY BRIDGE_OLD ?
`
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, dir, []string{"do", "run", "--lang", "kash", "--allow-run", "--allow-read", "-c", code}, nil, &out, &errOut)
	want := "source once\nfirst\nsecond\n\nnew-path\n1:[two words]:[]:from Kash:sub\n2:[$(not code)]:[x]:from Kash:sub\nchanged\n"
	if status != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q, want %q", status, out.String(), errOut.String(), want)
	}

	for _, test := range []struct {
		name string
		code string
		out  string
		err  string
	}{
		{"denied removal is atomic and invalidates the namespace", `source bash ./setup.sh as tools
tools/drop ?
printenv BRIDGE_NEW BRIDGE_OLD
tools/step a b
`, "source once\nfirst\nsecond\nold\n", "[CAP_DENIED]"},
		{"pipeline is rejected before starting a stage", `source bash ./setup.sh as tools
tools/step a b | cat
`, "source once\n", "[FEATURE_UNSUP]"},
		{"failed source does not publish exports", `source bash ./fail.sh as tools ?
printenv BRIDGE_OLD
`, "old\n", ""},
		{"worker exit does not publish exports and permits replacement", `source bash ./crash.sh as tools
tools/crash
printenv BRIDGE_OLD
source bash ./setup.sh as tools
`, "before exit\nold\nsource once\n", "[HOST_FAIL]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			out.Reset()
			errOut.Reset()
			nativeCLIWrite(t, filepath.Join(dir, "fail.sh"), "export BRIDGE_OLD=bad\nreturn 4\n")
			nativeCLIWrite(t, filepath.Join(dir, "crash.sh"), "crash() { echo before exit; export BRIDGE_OLD=bad; exit 7; }\n")
			runNativeCLIIn(t, dir, []string{"do", "run", "-i", "--allow-run", "--allow-read"}, strings.NewReader(test.code), &out, &errOut)
			if out.String() != test.out || test.err != "" && !strings.Contains(errOut.String(), test.err) || test.err == "" && errOut.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q; want stdout=%q stderr contains %q", out.String(), errOut.String(), test.out, test.err)
			}
		})
	}
}

func TestNativeForeignShellInterrupt(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("requires Bash")
	}
	dir := t.TempDir()
	t.Setenv("BRIDGE_EDIT", "original")
	nativeCLIWrite(t, filepath.Join(dir, "setup.sh"), `block() {
  export BRIDGE_EDIT=unpublished
  printf '%s\n' "$$" > child
  echo running
  sleep 30
}
`)
	command, in, stdout, stderr := startInteractiveKash(t, dir)
	io.WriteString(in, "source bash ./setup.sh as tools\ntools/block\n")
	child := filepath.Join(dir, "child")
	nativeCLIAwait(t, stderr, func() bool {
		_, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(child)))
		return err == nil && nativeCLIRead(stdout) == "running\n"
	})
	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	io.WriteString(in, "printenv BRIDGE_EDIT\nsource bash ./setup.sh as tools\n")
	in.Close()
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "running\noriginal\n" || nativeCLIRead(stderr) != "" {
		t.Fatalf("interrupt: status=%d out=%q err=%q", status, nativeCLIRead(stdout), nativeCLIRead(stderr))
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(nativeCLIRead(child)))
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("interrupted worker %d was not reaped", pid)
	}
}
