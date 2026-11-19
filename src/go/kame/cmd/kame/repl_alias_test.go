package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestNativeInteractiveKashAliases(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	directory := t.TempDir()
	entries := strings.Join([]string{
		`alias ll = ["printf", "<%s>\n", "two words", "$literal", "|"]`,
		`ll "extra space"`,
		`ll | cat > ./alias-output`,
		`cat ./alias-output`,
		`echo $(ll capture)`,
		`alias echo = ["echo", "prefix"]`,
		`echo once`,
		`alias ll = ["printf", "%s\n", "replacement"]`,
		`ll`,
		`alias ll = []`,
		`alias ll = ["echo", (out! "must-not-run")]`,
		`ll`,
		`alias ll`,
		`unalias ll missing`,
		`ll`,
		`unalias ll echo`,
		`alias`,
		`echo plain`,
		"alias multiline = [\n \"printf\",\n \"%s\\n\",\n \"multiline\"]",
		`multiline`,
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
	want := "<two words>\n<$literal>\n<|>\n<extra space>\n" +
		"<two words>\n<$literal>\n<|>\n" +
		"<two words>\n<$literal>\n<|>\n<capture>\n\n" +
		"prefix once\nreplacement\nreplacement\n" +
		"alias ll = [\"printf\", \"%s\\n\", \"replacement\"]\nreplacement\nplain\nmultiline\n"
	if status != 0 || out.String() != want {
		t.Fatalf("aliases: status=%d out=%q want=%q err=%q", status, out.String(), want, errOut.String())
	}
	for _, message := range []string{"nonempty literal list", "expected literal strings", "unknown alias: missing"} {
		if !strings.Contains(errOut.String(), message) {
			t.Errorf("missing %q in %q", message, errOut.String())
		}
	}
}

func TestNativeInteractiveKashAliasDirectory(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	directory := t.TempDir()
	sub := filepath.Join(directory, "two words")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	entries := "alias c = [\"cd\", \"two words\"]\nc\npwd\n"
	status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
	if status != 0 || out.String() != sub+"\n" || errOut.Len() != 0 {
		t.Fatalf("directory alias: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
}

func TestNativeKashLogin(t *testing.T) {
	binary := nativeCLIBinary(t)
	directory := t.TempDir()
	interpreter := filepath.Join(directory, "kash")
	if err := os.Symlink(binary, interpreter); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(directory, "profile.kash")
	init := filepath.Join(directory, "init.kash")
	nativeCLIWrite(t, profile, "echo profile\nexport FROM_PROFILE=yes\nalias p = [\"echo\", \"shortcut\"]\n")
	nativeCLIWrite(t, init, "echo init\n")
	t.Setenv("KASH_PROFILE", profile)
	t.Setenv("KASH_INIT", init)
	for _, test := range []struct {
		name, argv0 string
		args        []string
		stdin, want string
	}{
		{"argv0", "-kash", []string{"-i"}, "printenv FROM_PROFILE\np\n", "profile\ninit\nyes\nshortcut\n"},
		{"flag", "kash", []string{"--login", "-i"}, "p\n", "profile\ninit\nshortcut\n"},
		{"command", "kash", []string{"-lc", "printenv FROM_PROFILE"}, "", "profile\nyes\n"},
		{"remote command", "kash", []string{"-c", "echo remote"}, "", "remote\n"},
		{"ordinary interactive", "kash", []string{"-i"}, "echo ordinary\n", "init\nordinary\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(interpreter, test.args...)
			command.Args[0], command.Dir = test.argv0, directory
			command.Stdin = strings.NewReader(test.stdin)
			output, err := command.CombinedOutput()
			if err != nil || string(output) != test.want {
				t.Fatalf("login: error=%v out=%q want=%q", err, output, test.want)
			}
		})
	}
}

func TestAliasCompletionAndHighlighting(t *testing.T) {
	ctx := completionContext{Aliases: map[string][]string{"ll": {"ls", "-lah"}}}
	_, candidates := completionsAt("cat x | ll", len("cat x | ll"), ctx)
	if len(candidates) != 1 || candidates[0].Text != "ll" || candidates[0].Description != "ls -lah" {
		t.Fatalf("alias completion: %+v", candidates)
	}
	editor := editorReader{snapshot: ctx}
	if got := editor.highlight([]rune("ll")); strings.Contains(got, replUnknownCommandStyle) {
		t.Fatalf("alias marked unknown: %q", got)
	}
}

func TestNativeKashAliasFormatting(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "init.kash")
	text := "# shortcuts\nalias   ll=[\n \"printf\", \"%s\\n\", \"two words\"]\nalias ll\nunalias   ll\nalias ll = [\"printf\", \"%s\\n\", \"ready\"]\n"
	nativeCLIWrite(t, path, text)
	binary := nativeCLIBinary(t)
	command := exec.Command(binary, "do", "fmt", "--lang", "kash", "-i", path)
	output, err := command.CombinedOutput()
	want := "# shortcuts\nalias ll = [\"printf\", \"%s\\n\", \"two words\"]\nalias ll\nunalias ll\nalias ll = [\"printf\", \"%s\\n\", \"ready\"]\n"
	if err != nil || nativeCLIRead(path) != want {
		t.Fatalf("alias formatting: error=%v out=%q file=%q", err, output, nativeCLIRead(path))
	}
	command = exec.Command(binary, "do", "fmt", "--lang", "kash", "-i", path)
	if output, err = command.CombinedOutput(); err != nil || nativeCLIRead(path) != want {
		t.Fatalf("alias formatting is not idempotent: error=%v out=%q file=%q", err, output, nativeCLIRead(path))
	}
	t.Setenv("KASH_INIT", path)
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader("ll\n"), &out, &errOut)
	if status != 0 || out.String() != "alias ll = [\"printf\", \"%s\\n\", \"two words\"]\nready\n" || errOut.Len() != 0 {
		t.Fatalf("formatted aliases: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	command = exec.Command(binary, "do", "run", path)
	if output, err = command.CombinedOutput(); err == nil || !strings.Contains(string(output), "FEATURE_UNSUP") {
		t.Fatalf("batch aliases were not rejected: error=%v out=%q", err, output)
	}
	for _, source := range []string{
		"touch ./unexpected\nalias ll = [\"ls\"]\n",
		"((kash " + kameString("touch ./unexpected\nalias ll = [\"ls\"]\n") + "))",
	} {
		command = exec.Command(binary, "do", "run", "--lang", "kash", "-c", source)
		command.Dir = directory
		if output, err = command.CombinedOutput(); err == nil || !strings.Contains(string(output), "FEATURE_UNSUP") || fileExists(filepath.Join(directory, "unexpected")) {
			t.Fatalf("batch alias preflight: error=%v out=%q", err, output)
		}
	}
	text = "alias ll = [\"ls\"\n"
	nativeCLIWrite(t, path, text)
	command = exec.Command(binary, "do", "fmt", "--lang", "kash", "-i", path)
	if output, err = command.CombinedOutput(); err == nil || nativeCLIRead(path) != text {
		t.Fatalf("malformed alias was overwritten: error=%v out=%q file=%q", err, output, nativeCLIRead(path))
	}
}

func TestNativeInteractiveKashHangup(t *testing.T) {
	for _, mode := range []string{"foreground", "prompt"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			command, in, _, stderr := startInteractiveKash(t, directory)
			defer in.Close()
			entry := "defer touch ./finalized\nsh -c 'echo $$ > child; sleep 30'\n"
			if mode == "prompt" {
				entry = "defer touch ./finalized\nif true\n\tsh -c 'echo $$ > child; sleep 30'\n&\necho ready > ./ready\n"
			}
			if _, err := in.Write([]byte(entry)); err != nil {
				t.Fatal(err)
			}
			nativeCLIAwait(t, stderr, func() bool {
				return fileExists(filepath.Join(directory, "child")) && (mode != "prompt" || fileExists(filepath.Join(directory, "ready")))
			})
			if err := command.Process.Signal(syscall.SIGHUP); err != nil {
				t.Fatal(err)
			}
			if status := nativeCLIWait(t, command, stderr); status != 129 || !fileExists(filepath.Join(directory, "finalized")) {
				t.Fatalf("hangup: status=%d finalized=%v err=%q", status, fileExists(filepath.Join(directory, "finalized")), nativeCLIRead(stderr))
			}
			pid, err := strconv.Atoi(strings.TrimSpace(nativeCLIRead(filepath.Join(directory, "child"))))
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
				t.Fatalf("child %d survived hangup: %v", pid, err)
			}
		})
	}
}
