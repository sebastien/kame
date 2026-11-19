package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// In-process utilities must be indistinguishable from the executables they
// replace: compare stdout and exit status with the real /usr/bin utility for
// accepted forms and for forms that must fall back to it.
func TestNativeKashBuiltinsMatchSystemUtilities(t *testing.T) {
	binary := nativeCLIBinary(t)
	directory := t.TempDir()
	interpreter := filepath.Join(directory, "kash")
	if err := os.Symlink(binary, interpreter); err != nil {
		t.Fatal(err)
	}
	nativeCLIWrite(t, filepath.Join(directory, "script"), "def tool = (first args)\ndef rest = (slice 1 (count args) args)\n$tool $rest\n")
	nativeCLIWrite(t, filepath.Join(directory, "file"), "data")
	if err := os.Mkdir(filepath.Join(directory, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := [][]string{
		{"true"}, {"false"}, {"true", "ignored"}, {"true", "--help"},
		{"echo"}, {"echo", "a", "b  c"}, {"echo", "-n", "x"}, {"echo", "-n", "-n", "x"}, {"echo", "-e", "a\\tb"}, {"echo", "a\\c"}, {"echo", "--version"},
		{"printf", "%s|%d|%5s|%-4d|%04d|%.2s\\n", "a", "7", "b", "3", "-5", "xyz"}, {"printf", "%s-%s\\n", "a", "b", "c"},
		{"printf", "%d\\n", "010"}, {"printf", "%d\\n", ""}, {"printf", "plain\\n", "extra"}, {"printf", "%x\\n", "255"}, {"printf", "\\101\\n"}, {"printf", "100%%\\n"},
		{"test"}, {"test", ""}, {"test", "x"}, {"test", "-n", ""}, {"test", "-z", ""}, {"test", "-f", "file"}, {"test", "-d", "file"}, {"test", "-d", "dir"},
		{"test", "-e", "missing"}, {"test", "-s", "file"}, {"test", "-r", "file"}, {"test", "-x", "file"}, {"test", "!", "-f", "file"}, {"test", "a", "=", "a"},
		{"test", "a", "!=", "a"}, {"test", "3", "-lt", "10"}, {"test", "010", "-eq", "10"}, {"test", "x", "-eq", "1"}, {"test", "(", "x", ")"}, {"test", "-f"},
		{"[", "1", "-ge", "2", "]"}, {"[", "-d", "dir", "]"}, {"[", "x"},
	}
	for _, args := range cases {
		system := "/usr/bin/" + args[0]
		if _, err := os.Stat(system); err != nil {
			continue
		}
		expected := exec.Command(system, args[1:]...)
		expected.Dir = directory
		want, wantErr := expected.Output()
		actual := exec.Command(interpreter, append([]string{"script", system}, args[1:]...)...)
		actual.Dir = directory
		got, gotErr := actual.Output()
		if string(got) != string(want) || exitCode(gotErr) != exitCode(wantErr) {
			t.Errorf("%q: kash stdout=%q status=%d, utility stdout=%q status=%d", args, got, exitCode(gotErr), want, exitCode(wantErr))
		}
	}
}

func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}
