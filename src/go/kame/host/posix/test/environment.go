package posixcheck

import (
	"kame/host"
	"kame/host/posix"
	"kame/internal/testing"
	"os"
	"path/filepath"
)

func TestEnvironmentNamePlatformPolicy(t *testing.T) {
	if !posix.EnvironmentNameEqualForPlatform("Path", "PATH", true) ||
		posix.EnvironmentNameEqualForPlatform("Path", "PATH", false) ||
		posix.EnvironmentNameEqualForPlatform("PATH", "PATHEXT", true) ||
		!posix.EnvironmentNameEqualForPlatform("Ä", "ä", true) {
		t.Error("environment name platform policy is incorrect")
	}
	if posix.EnvironmentNameEqual("Path", "PATH") {
		t.Error("Linux native environment comparison must be case-sensitive")
	}
}

func TestEnvironmentPipelineOverridesLinux(t *testing.T) {
	h := posix.New(t.Allocator())
	defer h.Free()
	r := posix.Request{ID: 7001, Directory: ".", Environment: []string{"PATH=/usr/bin:/bin", "VALUE=base", "Value=case"}, RetainBytes: 1024,
		Stages: []host.ProcessStage{{Argv: []string{"/bin/sh", "-c", `printf '%s:%s' "$VALUE" "$Value"`}, Environment: []string{"VALUE=stage"}}}}
	if !h.Start(r) {
		t.Fatal("start environment pipeline")
		return
	}
	var terminal posix.Event
	events := drain(t, h, &terminal)
	defer freeEvents(t.Allocator(), events)
	if terminal.Outcome != posix.Exited || terminal.Status != 0 || string(terminal.Stdout) != "stage:case" {
		t.Error("stage override failed or removed distinct POSIX environment name")
	}
}

func TestProcessLookupUsesStageEnvironmentAndDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bin", "kame-child"), []byte("#!/bin/sh\nprintf resolved"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, topology := range []bool{false, true} {
		h := posix.New(t.Allocator())
		r := posix.Request{ID: 8001, Topology: topology, Directory: directory, Environment: []string{"PATH=/nonexistent"}, RetainBytes: 1024,
			Stages: []host.ProcessStage{{Argv: []string{"kame-child"}, Environment: []string{"PATH=bin"}}}}
		if !h.Start(r) {
			h.Free()
			t.Fatal("child lookup did not start")
		}
		var terminal posix.Event
		events := drain(t, h, &terminal)
		if terminal.Status != 0 || string(terminal.Stdout) != "resolved" {
			t.Errorf("child PATH/cwd lookup: topology=%v status=%d output=%q", topology, terminal.Status, terminal.Stdout)
		}
		freeEvents(t.Allocator(), events)
		h.Free()
	}
}
