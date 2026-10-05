package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNodeLaunchPreservesArgumentsAndExitStatus(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is not installed")
	}

	directory := t.TempDir()
	script := filepath.Join(directory, "kame.js")
	if err := os.WriteFile(script, []byte(`process.stdout.write(JSON.stringify(process.argv.slice(2))); process.exit(23);`), 0600); err != nil {
		t.Fatal(err)
	}
	want := []string{"two words", "&|;$(touch nope)", "", "雪"}
	var stdout, stderr bytes.Buffer
	status := runNode(node, script, want, nil, &stdout, &stderr)
	if status != 23 {
		t.Fatalf("exit status = %d, want 23; stderr: %s", status, stderr.String())
	}
	var got []string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid child argument output %q: %v", stdout.String(), err)
	}
	if len(got) != len(want) {
		t.Fatalf("child args = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("child arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMissingNodeFailsClearly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	status := runNode(filepath.Join(t.TempDir(), "missing-node"), "kame.js", nil, nil, &stdout, &stderr)
	if status != 1 {
		t.Fatalf("exit status = %d, want 1", status)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("cannot find Node runtime")) {
		t.Fatalf("stderr = %q, want a missing-runtime diagnostic", stderr.String())
	}
}
