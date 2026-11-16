package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeReplTool(t *testing.T, directory, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	nativeCLIWrite(t, path, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeInteractiveKashSessionPath(t *testing.T) {
	t.Setenv("KASH_INIT", "")
	directory := t.TempDir()
	one, two := filepath.Join(directory, "one"), filepath.Join(directory, "two words")
	first := writeReplTool(t, one, "probe", "echo one")
	second := writeReplTool(t, two, "probe", "echo two")
	writeReplTool(t, filepath.Join(directory, "bin"), "probe", "echo relative")
	writeReplTool(t, filepath.Join(directory, "sub", "bin"), "probe", "echo sub-relative")
	writeReplTool(t, filepath.Join(directory, "sub"), "probe", "echo current-directory")
	t.Setenv("PATH", one+":/usr/bin:/bin")
	entries := strings.Join([]string{
		"probe",
		`alias p = ["probe"]`,
		"export PATH=" + kameString(two+":/usr/bin:/bin"),
		"probe",
		"p",
		`echo (tool "probe")`,
		"export PATH=bin:/usr/bin:/bin",
		"probe",
		":cwd ./sub probe",
		"cd sub",
		"probe",
		"export PATH=",
		"probe",
		"unset PATH",
		"probe",
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, directory, []string{"do", "run", "-i"}, strings.NewReader(entries), &out, &errOut)
	want := "one\ntwo\ntwo\n" + second + "\nrelative\nsub-relative\nsub-relative\ncurrent-directory\ncurrent-directory\n"
	if status != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("session PATH: status=%d out=%q want=%q err=%q", status, out.String(), want, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	status = runNativeCLIIn(t, directory, []string{"do", "run", "-i", "--tool", "probe=" + first}, strings.NewReader("export PATH="+kameString(two)+"\nprobe\n"), &out, &errOut)
	if status != 0 || out.String() != "one\n" || errOut.Len() != 0 {
		t.Fatalf("tool override: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	status = runNativeCLIIn(t, directory, []string{"do", "run", "--lang", "kash", "--env", "PATH=" + two, "-c", "probe"}, strings.NewReader(""), &out, &errOut)
	if status != 0 || out.String() != "one\n" || errOut.Len() != 0 {
		t.Fatalf("batch lookup changed: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	status = runNativeCLIIn(t, directory, []string{"do", "run", "-i", "--allow-run=" + first}, strings.NewReader("export PATH="+kameString(two)+"\nprobe\n"), &out, &errOut)
	if status == 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "CAP_DENIED") {
		t.Fatalf("PATH escaped run grants: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
	profile := filepath.Join(directory, "profile.kash")
	nativeCLIWrite(t, profile, "export PATH="+kameString(two)+"\n")
	t.Setenv("KASH_PROFILE", profile)
	out.Reset()
	errOut.Reset()
	status = runNativeCLIIn(t, directory, []string{"do", "run", "--lang", "kash", "--login", "-c", "probe"}, strings.NewReader(""), &out, &errOut)
	if status != 0 || out.String() != "two\n" || errOut.Len() != 0 {
		t.Fatalf("login PATH: status=%d out=%q err=%q", status, out.String(), errOut.String())
	}
}

func TestNativeInteractiveKashPathKeepsBackgroundJob(t *testing.T) {
	directory := t.TempDir()
	two := filepath.Join(directory, "two")
	writeReplTool(t, two, "probe", "echo two")
	command, in, stdout, stderr := startInteractiveKash(t, directory)
	defer in.Close()
	io.WriteString(in, "if true\n\t/bin/sh -c 'echo launched >> ./launches; while [ ! -f release ]; do /bin/sleep 0.01; done; echo finished'\n&\necho ready\n")
	nativeCLIAwait(t, stderr, func() bool {
		return strings.Contains(nativeCLIRead(stdout), "ready\n") && fileExists(filepath.Join(directory, "launches"))
	})
	io.WriteString(in, "export PATH="+kameString(two)+"\nprobe\n")
	nativeCLIAwait(t, stderr, func() bool { return strings.Contains(nativeCLIRead(stdout), "two\n") })
	nativeCLIWrite(t, filepath.Join(directory, "release"), "")
	in.Close()
	if status := nativeCLIWait(t, command, stderr); status != 0 || nativeCLIRead(stdout) != "ready\ntwo\nfinished\n" || nativeCLIRead(filepath.Join(directory, "launches")) != "launched\n" {
		t.Fatalf("background PATH change: status=%d out=%q launches=%q err=%q", status, nativeCLIRead(stdout), nativeCLIRead(filepath.Join(directory, "launches")), nativeCLIRead(stderr))
	}
}

func TestSessionPathCompletionAndHighlighting(t *testing.T) {
	root := t.TempDir()
	writeReplTool(t, filepath.Join(root, "bin"), "relative-tool", "exit 0")
	writeReplTool(t, root, "local-tool", "exit 0")
	for _, path := range []string{"bin", "", ":"} {
		ctx := completionContext{Directory: root, Path: path}
		name := "local-tool"
		if path == "bin" {
			name = "relative-tool"
		}
		_, candidates := completionsAt(name, len(name), ctx)
		if len(candidates) != 1 || candidates[0].Text != name || candidates[0].Display != name+"*" {
			t.Errorf("PATH=%q completion: %+v", path, candidates)
		}
		editor := editorReader{context: func() completionContext { return ctx }}
		if got := editor.highlight([]rune(name)); strings.Contains(got, replUnknownCommandStyle) {
			t.Errorf("PATH=%q marked executable unknown: %q", path, got)
		}
	}
}
