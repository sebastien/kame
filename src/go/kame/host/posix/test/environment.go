package posix_test

import (
	"kame/host"
	"kame/host/posix"
	"solod.dev/so/testing"
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
