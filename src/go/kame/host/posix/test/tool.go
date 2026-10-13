package posix_test

import (
	"kame/host/posix"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
)

func TestToolWindowsPathPolicy(t *testing.T) {
	names := []string{`C:\tools\runner.exe`, "C:/tools/runner.exe", `\\server\share\runner.exe`, `tools\runner.exe`, "C:runner.exe"}
	want := []string{"C:/tools/runner.exe", "C:/tools/runner.exe", "//server/share/runner.exe", "D:/work/tools/runner.exe", ""}
	for i := range names {
		result := posix.ToolPath(t.Allocator(), `D:\work`, names[i], true)
		if result != want[i] {
			t.Errorf("Windows path policy: got %s, want %s", result, want[i])
		}
		mem.FreeString(t.Allocator(), result)
	}
	unc := posix.ToolPath(t.Allocator(), `\\server\share`, "runner.exe", true)
	if unc != "//server/share/runner.exe" {
		t.Error("joining lost UNC root")
	}
	mem.FreeString(t.Allocator(), unc)
}

func TestToolWindowsCosmoDriveAliasPolicy(t *testing.T) {
	cwds := []string{"/C/work", "/D/work", "/D/work", "/D/work", "/D/work", "/D/work", "/D/work", "/D/work", "/D/work", `/D/work`, `/D/work`}
	names := []string{"./tools/tool.EXE", "/C/work/tools/tool.EXE", "/C", "/c/tools/tool.EXE", "/tools/tool.EXE", `\C\tools\tool.EXE`, `/C\tools\tool.EXE`, "//server/share/tool.EXE", "/Cousin/tool.EXE", "/C/tools", `/Cousin/tools`}
	want := []string{"C:/work/tools/tool.EXE", "C:/work/tools/tool.EXE", "C:/", "c:/tools/tool.EXE", "D:/tools/tool.EXE", "D:/C/tools/tool.EXE", "D:/C/tools/tool.EXE", "//server/share/tool.EXE", "D:/Cousin/tool.EXE", "C:/tools", "D:/Cousin/tools"}
	for i := range names {
		result := posix.ToolPath(t.Allocator(), cwds[i], names[i], true)
		if result != want[i] {
			t.Errorf("Cosmo drive handoff: got %s, want %s", result, want[i])
		}
		mem.FreeString(t.Allocator(), result)
	}
	// The PATH loop resolves the component first, then joins the bare name.
	base := posix.ToolPath(t.Allocator(), "/D/work", "/C/tools", true)
	result := posix.ToolPath(t.Allocator(), base, "tool.EXE", true)
	if result != "C:/tools/tool.EXE" {
		t.Error("cross-drive Cosmo PATH component doubled the drive")
	}
	mem.FreeString(t.Allocator(), base)
	mem.FreeString(t.Allocator(), result)
}

func TestToolWindowsRootRelativeExplicitPathPolicy(t *testing.T) {
	names := []string{`\tools\runner.exe`, "/tools/runner.exe", `C:\tools\runner.exe`, `\\server\share\runner.exe`, `C:runner.exe`}
	want := []string{"D:/tools/runner.exe", "D:/tools/runner.exe", "C:/tools/runner.exe", "//server/share/runner.exe", ""}
	for i := range names {
		result := posix.ToolPath(t.Allocator(), `D:\work`, names[i], true)
		if result != want[i] {
			t.Errorf("explicit Windows path: got %s, want %s", result, want[i])
		}
		mem.FreeString(t.Allocator(), result)
	}
}

func TestToolWindowsUNCWorkingDirectoryPathPolicy(t *testing.T) {
	names := []string{`\tools\runner.exe`, "/tools/runner.exe", `\C\tools\runner.exe`, `C:\tools\runner.exe`, `/C/tools/runner.exe`, `\\server\share\runner.exe`, `tools\runner.exe`}
	want := []string{"", "", "", "C:/tools/runner.exe", "C:/tools/runner.exe", "//server/share/runner.exe", "//server/share/work/tools/runner.exe"}
	for i := range names {
		result := posix.ToolPath(t.Allocator(), `\\server\share\work`, names[i], true)
		if result != want[i] {
			t.Errorf("UNC cwd path: got %s, want %s", result, want[i])
		}
		mem.FreeString(t.Allocator(), result)
	}
}

func TestToolWindowsRootRelativePathComponentPolicy(t *testing.T) {
	directories := []string{`\tools`, "/tools", `C:\tools`, `\\server\share`, "C:tools", "."}
	want := []string{"D:/tools/runner.exe", "D:/tools/runner.exe", "C:/tools/runner.exe", "//server/share/runner.exe", "", "D:/work/runner.exe"}
	for i := range directories {
		// Apply the same two steps as ResolveToolForPlatform's PATH loop.
		base := posix.ToolPath(t.Allocator(), `D:\work`, directories[i], true)
		result := ""
		if base != "" {
			result = posix.ToolPath(t.Allocator(), base, "runner.exe", true)
		}
		if result != want[i] {
			t.Errorf("Windows PATH component: got %s, want %s", result, want[i])
		}
		mem.FreeString(t.Allocator(), base)
		mem.FreeString(t.Allocator(), result)
	}
}

func removeToolTestDirectory(directory string) {
	os.Remove(directory + "/runner")
	os.Remove(directory + "/runner.EXE")
	os.Remove(directory + "/runner.CUSTOM")
	os.Remove(directory + "/suffix.CUSTOM")
	os.Remove(directory)
}

func TestToolResolutionPlatformPolicyAndLinuxFiles(t *testing.T) {
	var buffer [os.MaxPathLen]byte
	directory, err := os.MkdirTemp(buffer[:], "/tmp/opencode", "kame-tools-")
	if err != nil {
		t.Fatal("create tool directory")
		return
	}
	defer removeToolTestDirectory(directory)
	if os.WriteFile(directory+"/runner", []byte("#!/bin/sh\nprintf resolved"), 0o700) != nil ||
		os.WriteFile(directory+"/runner.EXE", []byte("Windows policy fixture, not executable"), 0o600) != nil ||
		os.WriteFile(directory+"/runner.CUSTOM", []byte("custom extension fixture"), 0o600) != nil ||
		os.WriteFile(directory+"/suffix.CUSTOM", []byte("suffix-only fixture"), 0o600) != nil {
		t.Fatal("write tool fixtures")
		return
	}
	a := t.Allocator()
	resolved := posix.ResolveTool(a, "runner", directory, []string{"Path=/missing", "PATH=" + directory})
	if resolved != directory+"/runner" {
		t.Error("POSIX PATH matching or executable lookup failed")
	}
	if resolved != "" {
		h := posix.New(a)
		if h.Start(posix.Request{ID: 7002, Argv: []string{resolved}, Directory: directory, RetainBytes: 1024}) {
			var terminal posix.Event
			events := drain(t, h, &terminal)
			if terminal.Status != 0 || terminal.Outcome != posix.Exited || string(terminal.Stdout) != "resolved" {
				t.Error("resolved Linux tool did not execute")
			}
			freeEvents(a, events)
		} else {
			t.Error("resolved Linux tool did not start")
		}
		h.Free()
	}
	mem.FreeString(a, resolved)
	resolved = posix.ResolveTool(a, "runner.EXE", directory, []string{"PATH=" + directory})
	if resolved != "" {
		t.Error("POSIX accepted file without executable bits")
	}
	mem.FreeString(a, resolved)
	// Linux resolves //tmp/opencode/... to the same fixture; the Windows policy
	// sees a valid UNC path, not a rooted path with a fictitious drive-less cwd.
	windowsDirectory := "/" + directory
	windows := posix.ResolveToolForPlatform(a, "runner", windowsDirectory, []string{"pAtH=//missing/share;" + windowsDirectory, "Pathext=.EXE"}, true)
	if windows != windowsDirectory+"/runner.EXE" {
		t.Error("Windows PATH name, separator, extension or permission policy failed")
	}
	mem.FreeString(a, windows)
	windows = posix.ResolveToolForPlatform(a, windowsDirectory+"/runner", windowsDirectory, []string{"PATHEXT=.CUSTOM"}, true)
	if windows != windowsDirectory+"/runner" {
		t.Error("explicit extensionless path did not try the exact file first")
	}
	mem.FreeString(a, windows)
	windows = posix.ResolveToolForPlatform(a, windowsDirectory+"/suffix", windowsDirectory, []string{"PATHEXT=.CUSTOM"}, true)
	if windows != windowsDirectory+"/suffix.CUSTOM" {
		t.Error("explicit path did not try PATHEXT after the missing exact file")
	}
	mem.FreeString(a, windows)
	windows = posix.ResolveToolForPlatform(a, "runner", windowsDirectory, []string{"PATH=" + windowsDirectory, "PATHEXT=.MISSING"}, true)
	if windows != windowsDirectory+"/runner" {
		t.Error("bare Windows tool did not fall back to the exact extensionless file")
	}
	mem.FreeString(a, windows)
	windows = posix.ResolveToolForPlatform(a, "runner", windowsDirectory, []string{"PATH=" + windowsDirectory}, true)
	if windows != windowsDirectory+"/runner.EXE" {
		t.Error("default Windows executable extensions failed")
	}
	mem.FreeString(a, windows)
	windows = posix.ResolveToolForPlatform(a, windowsDirectory+"/runner.EXE", windowsDirectory, []string{"PATHEXT=.CUSTOM"}, true)
	if windows != windowsDirectory+"/runner.EXE" {
		t.Error("explicit executable extension was not checked directly")
	}
	mem.FreeString(a, windows)
	windows = posix.ResolveToolForPlatform(a, "runner", windowsDirectory, []string{"Path=//missing/share;", "PATHEXT=.EXE"}, true)
	if windows != windowsDirectory+"/runner.EXE" {
		t.Error("empty Windows PATH component did not use cwd")
	}
	mem.FreeString(a, windows)
	windows = posix.ResolveToolForPlatform(a, directory+"/runner.EXE", windowsDirectory, []string{"PATHEXT=.EXE"}, true)
	if windows != "" {
		t.Error("rooted tool without a cwd drive was resolved on the parent drive")
	}
	mem.FreeString(a, windows)
	windows = posix.ResolveToolForPlatform(a, "runner", windowsDirectory, []string{"PATH=" + directory + ";" + windowsDirectory, "PATHEXT=.EXE"}, true)
	if windows != windowsDirectory+"/runner.EXE" {
		t.Error("rooted PATH component without a cwd drive was not skipped")
	}
	mem.FreeString(a, windows)
	resolved = posix.ResolveTool(a, "runner", directory, []string{"PATH=/missing:."})
	if resolved != directory+"/runner" {
		t.Error("relative POSIX PATH component did not use cwd")
	}
	mem.FreeString(a, resolved)
	resolved = posix.ResolveTool(a, "./runner", directory, []string{"PATH=/missing"})
	if resolved != directory+"/runner" {
		t.Error("explicit relative tool was not resolved from cwd")
	}
	mem.FreeString(a, resolved)
}
