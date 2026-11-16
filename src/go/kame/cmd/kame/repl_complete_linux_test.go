//go:build linux

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPathCompletionKinds(t *testing.T) {
	directory := t.TempDir()
	for name, mode := range map[string]os.FileMode{"file": 0o644, "exec": 0o755} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"link": "file", "linkdir": "dir", "linkexec": "exec", "broken": "missing"} {
		if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mkfifo(filepath.Join(directory, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(directory, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, candidates := completionsAt("cat ", 4, completionContext{Directory: directory})
	want := map[string]struct{ display, style, description string }{
		"file":     {"file", "", ""},
		"exec":     {"exec*", "32", ""},
		"dir/":     {"dir/", "34", ""},
		"link":     {"link@", "36", ""},
		"linkdir/": {"linkdir@/", "36", ""},
		"linkexec": {"linkexec@", "36", ""},
		"broken":   {"broken@", "36", ""},
		"fifo":     {"fifo|", "33", "fifo"},
		"socket":   {"socket=", "33", "socket"},
	}
	if len(candidates) != len(want) {
		t.Fatalf("candidates: %+v", candidates)
	}
	for _, c := range candidates {
		w, ok := want[c.Text]
		if !ok || c.Display != w.display || c.Style != w.style || c.Description != w.description {
			t.Errorf("candidate %+v, want %+v", c, w)
		}
	}
	_, candidates = completionsAt("cd ", 3, completionContext{Directory: directory})
	if len(candidates) != 2 || candidates[0].Text != "dir/" || candidates[1].Text != "linkdir/" {
		t.Fatalf("directory-only completions: %+v", candidates)
	}
	_, candidates = completionsAt("linke", 5, completionContext{Directory: directory, Path: directory})
	if len(candidates) != 1 || candidates[0].Text != "linkexec" || candidates[0].Display != "linkexec@" || candidates[0].Style != "36" {
		t.Fatalf("executable symlink completion: %+v", candidates)
	}
}
