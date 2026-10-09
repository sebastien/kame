package posix_test

import (
	"kame/core"
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
	parsed := core.ParseResourceURI(t.Allocator(), "file://"+directory+"/entry%252F%23%3f%20%C3%A9")
	if parsed.Error != "" {
		t.Fatal("parse file URI")
		return
	}
	defer parsed.URI.Free()
	name := parsed.URI.Canonical(t.Allocator())
	defer mem.FreeString(t.Allocator(), name)
	if h.WriteFileAtomic(name, []byte("uri"), 0o600, false) != nil {
		t.Fatal("file URI write failed")
		return
	}
	data, readErr := h.ReadFile(t.Allocator(), name)
	if readErr != nil || string(data) != "uri" {
		t.Error("file URI did not map to its absolute path")
	}
	mem.FreeSlice(t.Allocator(), data)
	data, readErr = os.ReadFile(t.Allocator(), directory+"/entry%2F#? é")
	if readErr != nil || string(data) != "uri" {
		t.Error("file URI write did not preserve the decoded filename")
	}
	mem.FreeSlice(t.Allocator(), data)
	stat := h.Stat(name)
	link := h.Lstat(name)
	if !stat.Exists || !link.Exists || stat.Info.Size != 3 {
		t.Error("file URI stat did not preserve the decoded filename")
	}
	if h.Remove(name) != nil || h.Stat(name).Exists {
		t.Error("file URI remove failed")
	}
}

func TestPosixHostRejectsInvalidFileResourceURIs(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	defer h.Free()
	invalid := []string{"file://host/path", "file:///../../escape", "file:///bad%Q0", "file:///path?query", "file:///path#fragment"}
	for _, name := range invalid {
		parsed := core.ParseResourceURI(a, name)
		if parsed.Error == "" { t.Error("invalid URI fixture was accepted: " + name) }
		stat := h.Stat(name)
		link := h.Lstat(name)
		if stat.Exists || !stat.Failed || link.Exists || !link.Failed {
			t.Error("invalid file URI was treated as a filesystem path by stat: " + name)
		}
		data, readErr := h.ReadFile(a, name)
		mem.FreeSlice(a, data)
		if readErr == nil || readErr.Error() != "invalid file resource URI" {
			t.Error("invalid file URI did not return a rejection from read: " + name)
		}
		entries, dirErr := h.ReadDir(a, name)
		for i := range entries {
			mem.FreeString(a, entries[i].Name)
		}
		mem.FreeSlice(a, entries)
		if dirErr == nil || dirErr.Error() != "invalid file resource URI" {
			t.Error("invalid file URI did not return a rejection from readdir: " + name)
		}
		writeErr := h.WriteFileAtomic(name, []byte("invalid"), 0o600, false)
		mkdirErr := h.Mkdir(name, 0o700)
		removeErr := h.Remove(name)
		if writeErr == nil || writeErr.Error() != "invalid file resource URI" || mkdirErr == nil || mkdirErr.Error() != "invalid file resource URI" || removeErr == nil || removeErr.Error() != "invalid file resource URI" {
			t.Error("invalid file URI did not return a rejection from mutation: " + name)
		}
		parsed.URI.Free()
	}
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
