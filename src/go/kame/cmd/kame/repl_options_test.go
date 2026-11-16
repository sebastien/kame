package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func optionsFixture(t *testing.T) *replOptions {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("KASH_HISTORY_SCOPE", "local")
	t.Setenv("KASH_HISTORY_MAX_AGE_DAYS", "15")
	t.Setenv("KASH_HISTORY_MAX_BYTES", "1048576")
	return loadReplOptions(&bytes.Buffer{})
}

func optionValue(o *replOptions, name string) (string, string) {
	option, _ := findReplOption(name)
	return o.value(option)
}

func TestOptionsScopesAndPersistence(t *testing.T) {
	o := optionsFixture(t)
	var out, errOut bytes.Buffer
	for _, command := range []string{
		"option -g history-scope global",
		"option --local history-scope local",
		"option --global history-max-age-days 030",
		"option -l history-max-bytes 2048 # literal setting",
	} {
		if handled, status := o.command(command, &out, &errOut); !handled || status != 0 {
			t.Fatalf("%s: handled=%v status=%d err=%s", command, handled, status, &errOut)
		}
	}
	if value, scope := optionValue(o, "history-scope"); value != "local" || scope != "local" {
		t.Fatalf("local override: %s %s", value, scope)
	}
	want := "history-max-age-days: 30\nhistory-scope: global\n"
	if got := nativeCLIRead(o.path); got != want {
		t.Fatalf("global file: %q, want %q", got, want)
	}
	fresh := loadReplOptions(&errOut)
	if value, scope := optionValue(fresh, "history-scope"); value != "global" || scope != "global" {
		t.Fatalf("global default: %s %s", value, scope)
	}
	if value, scope := optionValue(fresh, "history-max-bytes"); value != "1048576" || scope != "environment" {
		t.Fatalf("local setting escaped: %s %s", value, scope)
	}
	o.command("option history-scope", &out, &errOut)
	if !strings.Contains(out.String(), "history-scope: local (local; local|global)") {
		t.Fatalf("inspection: %s", &out)
	}
	out.Reset()
	o.command("option -g", &out, &errOut)
	if strings.Contains(out.String(), "history-max-bytes") || !strings.Contains(out.String(), "history-scope: global") {
		t.Fatalf("global inspection: %s", &out)
	}
	for _, path := range []string{o.path, o.path + ".lock"} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("option files must be private")
		}
	}
}

func TestOptionsValidationIsAtomic(t *testing.T) {
	o := optionsFixture(t)
	var out, errOut bytes.Buffer
	o.command("option -g history-scope local", &out, &errOut)
	before := nativeCLIRead(o.path)
	for _, command := range []string{
		"option missing on", "option history-scope everywhere",
		"option history-max-age-days 0", "option history-max-age-days 106752",
		"option history-max-bytes -1", "option history-max-bytes 9223372036854775808",
		"option -l -g history-scope global", "option --global --local",
		"option history-scope global extra", "option --unknown",
		"option history-scope global; echo unexpected",
		"option history-scope global\necho unexpected",
		"option history-scope global | cat", "option history-scope global &",
		"option history-scope $(touch ./unexpected)",
	} {
		if handled, status := o.command(command, &out, &errOut); !handled || status != 2 {
			t.Errorf("%q: handled=%v status=%d", command, handled, status)
		}
		if len(o.local) != 0 || nativeCLIRead(o.path) != before {
			t.Fatalf("%q changed options", command)
		}
	}
	boolean := replOption{name: "test", choices: []string{"on", "off"}}
	for _, value := range []string{"on", "off"} {
		if got, err := boolean.validate(value); err != nil || got != value {
			t.Fatal("boolean choices rejected")
		}
	}
	if _, err := boolean.validate("yes"); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}

func TestOptionsMalformedFilesPreserved(t *testing.T) {
	o := optionsFixture(t)
	if err := os.MkdirAll(filepath.Dir(o.path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		"unknown: on\n", "history-scope global\n", "history-scope: local\nhistory-scope: global\n",
		"history-max-bytes: 0\n", "history-scope: global\nhistory-max-age-days: nope\n",
		"# invalid UTF-8 \xff\n",
		strings.Repeat("#", (64<<10)+1),
	} {
		nativeCLIWrite(t, o.path, data)
		var errOut bytes.Buffer
		fresh := loadReplOptions(&errOut)
		if errOut.Len() == 0 || len(fresh.global) != 0 {
			t.Fatal("malformed file was partially accepted")
		}
		if handled, status := fresh.command("option -g history-scope local", &bytes.Buffer{}, &errOut); !handled || status != 2 {
			t.Fatal("malformed file update succeeded")
		}
		if nativeCLIRead(o.path) != data {
			t.Fatal("malformed file was overwritten")
		}
	}
}

func TestOptionsConcurrentWriters(t *testing.T) {
	o := optionsFixture(t)
	var wg sync.WaitGroup
	for name, value := range map[string]string{"history-scope": "global", "history-max-age-days": "30", "history-max-bytes": "2048"} {
		wg.Go(func() {
			other := &replOptions{path: o.path}
			if err := other.save(name, value); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	values, err := readReplOptions(o.path)
	if err != nil || len(values) != 3 {
		t.Fatalf("lost concurrent update: %v, %v", values, err)
	}
}

func TestOptionsUnsafeFilesAndLockFailure(t *testing.T) {
	o := optionsFixture(t)
	if err := os.MkdirAll(filepath.Dir(o.path), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	nativeCLIWrite(t, target, "history-scope: global\n")
	if err := os.Symlink(target, o.path); err != nil {
		t.Fatal(err)
	}
	if err := o.save("history-scope", "local"); err == nil {
		t.Fatal("symlink accepted")
	}
	if nativeCLIRead(target) != "history-scope: global\n" {
		t.Fatal("symlink target changed")
	}
	if err := os.Remove(o.path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(o.path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReplOptions(o.path); err == nil {
		t.Fatal("FIFO accepted")
	}
	if err := os.Remove(o.path); err != nil {
		t.Fatal(err)
	}
	lock, err := historyOpen(o.path+".lock", true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	start := time.Now()
	if err := o.save("history-scope", "global"); err == nil || time.Since(start) > time.Second {
		t.Fatal("lock failure was not bounded")
	}
	if len(o.global) != 0 {
		t.Fatal("failed write changed the global snapshot")
	}
}

func TestOptionsHistoryUsesSharedState(t *testing.T) {
	h, _ := historyFixture(t)
	o := optionsFixture(t)
	h.record("echo expired")
	now := h.now()
	h.now = func() time.Time { return now.Add(48 * time.Hour) }
	h.options = o
	o.command("option history-max-age-days 1", &bytes.Buffer{}, &bytes.Buffer{})
	o.command("option history-max-bytes 512", &bytes.Buffer{}, &bytes.Buffer{})
	o.command("option history-scope global", &bytes.Buffer{}, &bytes.Buffer{})
	h.applyOptions()
	if h.maxAge != 24*time.Hour || h.maxBytes != 512 || !h.global {
		t.Fatal("history did not apply options")
	}
	if h.Len() != 0 {
		t.Fatal("lowered retention did not remove expired recall")
	}
	h.refresh(h.cwd)
	if nativeCLIRead(h.path) != historyHeader {
		t.Fatal("lowered retention did not prune persisted history")
	}
	h.command("history scope local", &bytes.Buffer{}, &bytes.Buffer{})
	if value, scope := optionValue(o, "history-scope"); value != "local" || scope != "local" {
		t.Fatal("history scope did not update local option")
	}
	o.command("option -g history-scope global", &bytes.Buffer{}, &bytes.Buffer{})
	h.applyOptions()
	if h.global {
		t.Fatal("global update overrode local history scope")
	}
}

func TestOptionCompletions(t *testing.T) {
	for line, want := range map[string]string{
		"option history-sc": "history-scope", "option --global history-max-a": "history-max-age-days",
		"option -": "--global", "option history-scope g": "global",
	} {
		_, candidates := completionsAt(line, len(line), completionContext{})
		found := false
		for _, candidate := range candidates {
			found = found || candidate.Text == want
		}
		if !found {
			t.Errorf("%q did not complete %q: %+v", line, want, candidates)
		}
	}
}

func TestNativeInteractiveOptions(t *testing.T) {
	o := optionsFixture(t)
	t.Setenv("KASH_INIT", "")
	directory := t.TempDir()
	var out, errOut bytes.Buffer
	entries := "option -g history-scope global\noption history-scope local\noption history-scope\noption -g history-scope\n"
	status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
	if status != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "history-scope: local (local;") ||
		!strings.Contains(out.String(), "history-scope: global (global;") {
		t.Fatalf("interactive options: status=%d out=%q err=%q", status, &out, &errOut)
	}
	if nativeCLIRead(o.path) != "history-scope: global\n" {
		t.Fatal("native CLI did not persist global options")
	}
	init := filepath.Join(directory, "init.kash")
	nativeCLIWrite(t, init, "option --local history-scope local\n")
	t.Setenv("KASH_INIT", init)
	out.Reset()
	errOut.Reset()
	status = runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader("option history-scope\n"), &out, &errOut)
	if status != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "history-scope: local (local;") {
		t.Fatalf("startup override: status=%d out=%q err=%q", status, &out, &errOut)
	}
	for _, source := range []string{
		"touch ./unexpected\noption history-scope global\n",
		"((kash " + kameString("touch ./unexpected\noption history-scope global\n") + "))",
	} {
		command := exec.Command(nativeCLIBinary(t), "do", "run", "--lang", "kash", "-c", source)
		command.Dir = directory
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "FEATURE_UNSUP") || fileExists(filepath.Join(directory, "unexpected")) {
			t.Fatalf("batch options were not preflight rejected: %v %q", err, output)
		}
	}
	command := exec.Command(nativeCLIBinary(t), "do", "fmt", "--lang", "kash", "-i", init)
	if output, err := command.CombinedOutput(); err != nil || nativeCLIRead(init) != "option --local history-scope local\n" {
		t.Fatalf("option formatting: %v %q", err, output)
	}
}
