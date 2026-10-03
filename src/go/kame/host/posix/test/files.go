package posix_test

import (
	"kame/host/posix"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
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
