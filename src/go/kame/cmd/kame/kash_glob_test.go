package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeKashCommandGlobs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plan-055 one", "plan-055-two", ".plan-055", "prefix*one", "prefix-other"} {
		nativeCLIWrite(t, filepath.Join(dir, name), "")
	}
	nativeCLIWrite(t, filepath.Join(sub, "after-cd"), "")
	t.Setenv("HOME", dir)
	code := `let star = "*"
printf '<%s>\n' plan-055* ./plan-055-[t]* 'plan-055*' plan-055\* ${star} prefix${star}*
printf '<%s>\n' ~ ~/example '~/example'
def show values...
  printf '<%s>\n' $...values
show plan-055*
printf '<%s>\n' *055* .plan-*
printf '<%s>\n' su*/
cwd: ./sub printf '<%s>\n' after-*
cd sub
let matches = files ./after-*
printf '<%s>\n' $...matches after-*
`
	var out, errOut bytes.Buffer
	flags := []string{"do", "run", "--lang", "kash", "--allow-read", "--allow-run"}
	status := runNativeCLIIn(t, dir, append(flags, "-c", code), nil, &out, &errOut)
	path := "<" + filepath.Join(sub, "after-cd") + ">\n"
	want := "<./plan-055 one>\n<./plan-055-two>\n<./plan-055-two>\n<plan-055*>\n<plan-055*>\n<*>\n<./prefix*one>\n" +
		"<" + dir + ">\n<" + filepath.Join(dir, "example") + ">\n<~/example>\n" +
		"<./plan-055 one>\n<./plan-055-two>\n<./plan-055 one>\n<./plan-055-two>\n<./.plan-055>\n<./sub/>\n" + strings.Repeat(path, 3)
	if status != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q, want stdout=%q", status, out.String(), errOut.String(), want)
	}
	for _, test := range []struct {
		pattern, read, code string
	}{
		{"not-here-*", "--allow-read", "GLOB_NO_MATCH"},
		{"[.]plan-*", "--allow-read", "GLOB_NO_MATCH"},
		{"plan-055*/", "--allow-read", "GLOB_NO_MATCH"},
		{"sub*/../plan-055*", "--allow-read", "PAT_INVALID"},
		{"plan-055*", "--allow-read=./sub", "CAP_DENIED"},
	} {
		out.Reset()
		errOut.Reset()
		status = runNativeCLIIn(t, dir, []string{"do", "run", "--lang", "kash", test.read, "--allow-run", "-c", "printf '<%s>\\n' " + test.pattern + " ?"}, nil, &out, &errOut)
		if status == 0 || out.Len() != 0 || !strings.Contains(errOut.String(), "["+test.code+"]") {
			t.Errorf("%s: status=%d stdout=%q stderr=%q, want %s", test.pattern, status, out.String(), errOut.String(), test.code)
		}
	}
}

func TestNativeKashCommandGlobsSkipHiddenTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"visible/poison/hit.txt",
		"visible/.nested/secret.txt",
		".private/shown-one.txt",
		".private/.nested/shown-two.txt",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		nativeCLIWrite(t, path, "")
	}
	// Reaching this literal intermediate component fails even as root.
	if err := os.Symlink("poison", filepath.Join(dir, ".private", "poison")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	code := `printf '<%s>\n' **/poison/*.txt */poison/*.txt
printf '<%s>\n' **/.private*/**/shown-*.txt .private/shown-*.txt
printf '<%s>\n' visible/**
let all = wildcard ./visible/**
printf '<%s>\n' $...all
`
	var out, errOut bytes.Buffer
	flags := []string{"do", "run", "--lang", "kash", "--allow-read", "--allow-run", "-c"}
	status := runNativeCLIIn(t, dir, append(flags, code), nil, &out, &errOut)
	want := strings.Repeat("<./visible/poison/hit.txt>\n", 2) +
		strings.Repeat("<./.private/shown-one.txt>\n", 2) +
		"<./visible/poison>\n<./visible/poison/hit.txt>\n" +
		"<./visible/.nested>\n<./visible/.nested/secret.txt>\n<./visible/poison>\n<./visible/poison/hit.txt>\n"
	if status != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q, want stdout=%q", status, out.String(), errOut.String(), want)
	}
}

func TestNativeKashCommandGlobsSortFinalSpellings(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "z-root")
	outside := filepath.Join(parent, "a-outside", "hit-one")
	inside := filepath.Join(dir, "hit-two")
	if err := os.MkdirAll(filepath.Dir(outside), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub-more"), 0700); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, outside, "")
	nativeCLIWrite(t, inside, "")
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, dir, []string{"do", "run", "--lang", "kash", "--allow-read", "--allow-run", "-c",
		"cwd: ./sub printf '<%s>\\n' ../../*/hit-*\nprintf '<%s>\\n' sub*/"}, nil, &out, &errOut)
	want := "<" + filepath.ToSlash(outside) + ">\n<" + filepath.ToSlash(inside) + ">\n<./sub-more/>\n<./sub/>\n"
	if status != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q, want stdout=%q", status, out.String(), errOut.String(), want)
	}
}

func TestNativeKashDefaultEnvironmentReads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KASH_INIT", "")
	t.Setenv("CWD", "inherited")
	var out, errOut bytes.Buffer
	for _, args := range [][]string{
		{"do", "run", "-i"},
		{"do", "run", "--lang", "kash", "-c", "echo $CWD"},
	} {
		out.Reset()
		errOut.Reset()
		status := runNativeCLIIn(t, dir, args, strings.NewReader("echo $CWD\n"), &out, &errOut)
		if status != 0 || out.String() != "inherited\n" || errOut.Len() != 0 {
			t.Errorf("%v: status=%d stdout=%q stderr=%q", args, status, out.String(), errOut.String())
		}
	}
	for _, args := range [][]string{
		{"do", "run", "--lang", "kash", "--no-default-grants", "--allow-run", "-c", "echo $CWD"},
		{"do", "run", "--lang", "kash", "--allow-env=HOME", "-c", "echo $CWD"},
		{"do", "run", "--lang", "expr", "-c", `env "CWD"`},
	} {
		out.Reset()
		errOut.Reset()
		status := runNativeCLIIn(t, dir, args, nil, &out, &errOut)
		if status == 0 || !strings.Contains(errOut.String(), "[CAP_DENIED]") {
			t.Errorf("%v: status=%d stdout=%q stderr=%q; want CAP_DENIED", args, status, out.String(), errOut.String())
		}
	}
}

func TestNativeBuildEnvironmentGrantKeepsRunDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CWD", "inherited")
	nativeCLIWrite(t, filepath.Join(dir, "Kamefile"), "task all\n\techo ${env/CWD}\n")
	var out, errOut bytes.Buffer
	status := runNativeCLIIn(t, dir, []string{"--allow-env=CWD", "all"}, nil, &out, &errOut)
	if status != 0 || !strings.Contains(out.String(), "inherited\n") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	status = runNativeCLIIn(t, dir, []string{"--no-default-grants", "--allow-env=CWD", "all"}, nil, &out, &errOut)
	if status == 0 || !strings.Contains(errOut.String()+out.String(), "[CAP_DENIED]") {
		t.Fatalf("status=%d stdout=%q stderr=%q; want CAP_DENIED", status, out.String(), errOut.String())
	}
}
