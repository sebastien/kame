package posix_test

import (
	"kame/host/posix"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
	"solod.dev/so/time"
)

func TestAtomicWriteIgnoresPredictableStagingSymlink(t *testing.T) {
	var buffer [os.MaxPathLen]byte
	directory, err := os.MkdirTemp(buffer[:], "", "kame-atomic-")
	if err != nil {
		t.Fatal("create test directory")
		return
	}
	defer removeAtomicTestDirectory(directory)
	victim := directory + "/victim"
	output := directory + "/output"
	if os.WriteFile(victim, []byte("untouched"), 0o600) != nil || os.Symlink(victim, output+".kame-write.tmp") != nil {
		t.Fatal("prepare staging symlink")
		return
	}
	h := posix.New(mem.System)
	defer h.Free()
	if h.WriteFileAtomic(output, []byte("new output"), 0o640, false) != nil {
		t.Fatal("atomic write failed")
		return
	}
	data, readErr := os.ReadFile(mem.System, victim)
	if readErr != nil || string(data) != "untouched" {
		t.Error("staging symlink victim was modified")
	}
	mem.FreeSlice(mem.System, data)
	data, readErr = os.ReadFile(mem.System, output)
	if readErr != nil || string(data) != "new output" {
		t.Error("wrong atomic output")
	}
	mem.FreeSlice(mem.System, data)
	info, statErr := os.Stat(output)
	if statErr != nil || info.Mode().Perm() != 0o640 {
		t.Error("requested permissions were not applied")
	}
}

func TestPosixHostServesMemoryResourceURIs(t *testing.T) {
	h := posix.New(t.Allocator())
	defer h.Free()
	name := "mem://workspace/assets/site.css"
	if h.WriteFileAtomic(name, []byte("body{}"), 0o644, false) != nil {
		t.Fatal("memory URI write failed")
		return
	}
	stat := h.Stat(name)
	if !stat.Exists || stat.Info.Size != 6 || !stat.Info.Regular {
		t.Error("memory URI stat returned incorrect metadata")
	}
	data, err := h.ReadFile(t.Allocator(), name)
	if err != nil || string(data) != "body{}" {
		t.Error("memory URI read returned incorrect content")
	}
	mem.FreeSlice(t.Allocator(), data)
	entries, readErr := h.ReadDir(t.Allocator(), "mem://workspace/assets")
	if readErr != nil || len(entries) != 1 || entries[0].Name != "site.css" {
		t.Error("memory URI directory listing failed")
	}
	for i := range entries {
		mem.FreeString(t.Allocator(), entries[i].Name)
	}
	mem.FreeSlice(t.Allocator(), entries)
	if h.Remove(name) != nil || h.Stat(name).Exists {
		t.Error("memory URI remove did not update stat")
	}
}

func TestPosixHostMapsFileResourceURIs(t *testing.T) {
	var buffer [os.MaxPathLen]byte
	directory, err := os.MkdirTemp(buffer[:], "", "kame-file-uri-")
	if err != nil {
		t.Fatal("create URI test directory")
		return
	}
	defer removeAtomicTestDirectory(directory)
	h := posix.New(t.Allocator())
	defer h.Free()
	name := "file://" + directory + "/entry"
	if h.WriteFileAtomic(name, []byte("uri"), 0o600, false) != nil {
		t.Fatal("file URI write failed")
		return
	}
	data, readErr := h.ReadFile(t.Allocator(), name)
	if readErr != nil || string(data) != "uri" {
		t.Error("file URI did not map to its absolute path")
	}
	mem.FreeSlice(t.Allocator(), data)
}

func TestAtomicWriteCleansFailedRenameAndDurableWrite(t *testing.T) {
	var buffer [os.MaxPathLen]byte
	directory, err := os.MkdirTemp(buffer[:], "", "kame-atomic-")
	if err != nil {
		t.Fatal("create test directory")
		return
	}
	defer removeAtomicTestDirectory(directory)
	h := posix.New(mem.System)
	defer h.Free()
	if h.WriteFileAtomic(directory, []byte("cannot replace directory"), 0o600, false) == nil {
		t.Error("rename onto directory succeeded")
	}
	entries, readErr := os.ReadDir(mem.System, directory)
	if readErr != nil || len(entries) != 0 {
		t.Error("failed write left a temporary")
	}
	os.FreeDirEntry(mem.System, entries)
	output := directory + "/cache"
	if h.WriteFileAtomic(output, []byte("record"), 0o600, true) != nil {
		t.Fatal("durable write failed")
		return
	}
	data, readErr := os.ReadFile(mem.System, output)
	if readErr != nil || string(data) != "record" {
		t.Error("wrong durable output")
	}
	mem.FreeSlice(mem.System, data)
	entries, readErr = os.ReadDir(mem.System, directory)
	if readErr != nil || len(entries) != 1 {
		t.Error("successful write left a temporary")
	}
	os.FreeDirEntry(mem.System, entries)
}

func removeAtomicTestDirectory(directory string) {
	entries, err := os.ReadDir(mem.System, directory)
	if err == nil {
		for i := range entries {
			os.Remove(directory + "/" + entries[i].Name)
		}
		os.FreeDirEntry(mem.System, entries)
	}
	os.Remove(directory)
}

func TestStatPreservesSubsecondModificationTimes(t *testing.T) {
	var buffer [os.MaxPathLen]byte
	directory, err := os.MkdirTemp(buffer[:], "", "kame-stat-")
	if err != nil {
		t.Fatal("create test directory")
		return
	}
	defer removeAtomicTestDirectory(directory)
	name := directory + "/input"
	if os.WriteFile(name, []byte("input"), 0o600) != nil {
		t.Fatal("create input")
		return
	}
	h := posix.New(mem.System)
	defer h.Free()
	first := time.Unix(1700000000, 100000000)
	second := time.Unix(1700000000, 900000000)
	if os.Chtimes(name, first, first) != nil {
		t.Fatal("set first timestamp")
		return
	}
	before := h.Stat(name)
	if os.Chtimes(name, second, second) != nil {
		t.Fatal("set second timestamp")
		return
	}
	after := h.Stat(name)
	link := h.Lstat(name)
	if !before.Exists || !after.Exists || !link.Exists || before.Info.ModTime != first.UnixNano() || after.Info.ModTime != second.UnixNano() || link.Info.ModTime != second.UnixNano() {
		t.Error("stat discarded subsecond precision")
	}
}
