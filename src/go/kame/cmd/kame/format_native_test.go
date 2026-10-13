package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These checks must run against the compiled So CLI, not the Go filesystem
// extern doubles. Set KAME_NATIVE_FORMAT_TEST_BINARY to an absolute binary path.
func nativeFormatBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("KAME_NATIVE_FORMAT_TEST_BINARY")
	if binary == "" {
		t.Skip("set KAME_NATIVE_FORMAT_TEST_BINARY to run native formatter regressions")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("KAME_NATIVE_FORMAT_TEST_BINARY must be absolute")
	}
	return binary
}

func nativeFormat(t *testing.T, binary, name string) {
	t.Helper()
	output, err := exec.Command(binary, "do", "fmt", "--lang", "km", "-i", name).CombinedOutput()
	if err != nil {
		t.Fatalf("native formatter: %v\n%s", err, output)
	}
}

func TestNativeFormatPreservesPermissionsAndIgnoresStagingSymlink(t *testing.T) {
	binary := nativeFormatBinary(t)
	for _, mode := range []os.FileMode{0o600, 0o640, 0o755} {
		t.Run(fmt.Sprintf("%o", mode), func(t *testing.T) {
			directory := t.TempDir()
			name := filepath.Join(directory, "source.km")
			victim := filepath.Join(directory, "victim")
			staging := name + ".kame-fmt.tmp"
			if err := os.WriteFile(name, []byte("[1   2]\n"), mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(name, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(victim, []byte("untouched"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, staging); err != nil {
				t.Fatal(err)
			}
			nativeFormat(t, binary, name)
			data, err := os.ReadFile(victim)
			if err != nil || string(data) != "untouched" {
				t.Fatalf("victim changed: %q, %v", data, err)
			}
			info, err := os.Lstat(name)
			if err != nil {
				t.Fatal(err)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
				t.Fatalf("source mode: %v, want %o", info.Mode(), mode)
			}
			data, err = os.ReadFile(name)
			if err != nil || string(data) != "[1 2]\n" {
				t.Fatalf("formatted source: %q, %v", data, err)
			}
			if target, err := os.Readlink(staging); err != nil || target != victim {
				t.Fatalf("staging symlink changed: %q, %v", target, err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 3 {
				t.Fatalf("staging leak: %v, %v", entries, err)
			}
		})
	}
}

func TestNativeFormatSourceSymlinkAndUnchangedFile(t *testing.T) {
	binary := nativeFormatBinary(t)
	directory := t.TempDir()
	target := filepath.Join(directory, "target.km")
	name := filepath.Join(directory, "source.km")
	if err := os.WriteFile(target, []byte("[1   2]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
	nativeFormat(t, binary, name)
	info, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("source symlink replacement mode: %v", info.Mode())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "[1   2]\n" {
		t.Fatalf("source target changed: %q, %v", data, err)
	}
	nativeFormat(t, binary, name)
	after, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, after) {
		t.Fatal("unchanged source was replaced")
	}
}

func TestNativeFormatConcurrentPublication(t *testing.T) {
	binary := nativeFormatBinary(t)
	directory := t.TempDir()
	name := filepath.Join(directory, "source.km")
	if err := os.WriteFile(name, []byte("[1   2]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 16)
	for i := range commands {
		commands[i] = exec.Command(binary, "do", "fmt", "--lang", "km", "-i", name)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Errorf("concurrent formatter: %v", err)
		}
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "[1 2]\n" {
		t.Fatalf("concurrent output: %q, %v", data, err)
	}
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("concurrent source mode: %v", info.Mode())
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging leak: %v, %v", entries, err)
	}
}

func TestNativeFormatReadFailureDiagnostic(t *testing.T) {
	binary := nativeFormatBinary(t)
	name := filepath.Join(t.TempDir(), "missing.km")
	output, err := exec.Command(binary, "do", "fmt", "--lang", "km", "-i", name).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "FS_ERR") || !strings.Contains(string(output), "cannot read source: "+name) {
		t.Fatalf("missing source diagnostic: %v\n%s", err, output)
	}
}

func TestNativeFormatReplacementFailurePreservesSource(t *testing.T) {
	binary := nativeFormatBinary(t)
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	directory := t.TempDir()
	name := filepath.Join(directory, "source.km")
	if err := os.WriteFile(name, []byte("[1   2]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(directory, 0o700) })
	output, err := exec.Command(binary, "do", "fmt", "--lang", "km", "-i", name).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "FS_ERR") || !strings.Contains(string(output), "cannot replace source: "+name) {
		t.Fatalf("replacement diagnostic: %v\n%s", err, output)
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "[1   2]\n" {
		t.Fatalf("failed replacement changed source: %q, %v", data, err)
	}
	info, err := os.Stat(name)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("failed replacement changed source permissions: %v, %v", info, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed replacement staging leak: %v, %v", entries, err)
	}
}
