package host_test

import (
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/testing"
)

func TestMemoryResourcesReadListPublishAndRemove(t *testing.T) {
	a := t.Allocator()
	resources := host.NewMemoryResources(a)
	if resources.WriteFileAtomic("mem://workspace/docs/one.md", []byte("one"), 0o644, false) != nil {
		t.Fatal("memory resource write failed")
		return
	}
	if resources.WriteFileAtomic("mem://workspace/docs/two.md", []byte("two"), 0o600, false) != nil {
		t.Fatal("second memory resource write failed")
		return
	}
	if !resources.Stat("mem://workspace/").Exists || !resources.Stat("mem://workspace/docs").Info.IsDir {
		t.Error("memory resource directory metadata was missing")
	}
	data, err := resources.ReadFile(a, "mem://workspace/docs/one.md")
	if err != nil || string(data) != "one" {
		t.Error("memory resource read returned incorrect bytes")
	}
	mem.FreeSlice(a, data)
	entries, err := resources.ReadDir(a, "mem://workspace/docs")
	if err != nil || len(entries) != 2 || entries[0].Name != "one.md" || entries[1].Name != "two.md" {
		t.Error("memory resource listing did not preserve stable insertion order")
	}
	for i := range entries {
		mem.FreeString(a, entries[i].Name)
	}
	mem.FreeSlice(a, entries)
	if resources.Remove("mem://workspace/docs/one.md") != nil || resources.Stat("mem://workspace/docs/one.md").Exists {
		t.Error("memory resource removal did not update identity")
	}
	resources.Free()
}

func TestMemoryResourcesFreeAllOwnedStorage(t *testing.T) {
	a := t.Allocator()
	tracker := a.(*mem.Tracker)
	before := tracker.Stats().Alloc
	resources := host.NewMemoryResources(a)
	resources.WriteFileAtomic("mem://workspace/a/b", []byte("payload"), 0o644, false)
	resources.Mkdir("mem://workspace/empty", 0o755)
	resources.Free()
	if got := tracker.Stats().Alloc - before; got != 0 {
		t.Errorf("memory resource store retained %d bytes", got)
	}
}

func TestMemoryResourcesRecordedDirectoryContract(t *testing.T) {
	a := t.Allocator()
	r := host.NewMemoryResources(a)
	defer r.Free()
	r.Mkdir("mem://ns/recorded", 0o755)
	r.Mkdir("mem://ns/recorded/child", 0o755)
	if r.Remove("mem://ns/recorded") == nil {
		t.Error("nonempty recorded directory removal accepted")
	}
	r.Mkdir("mem://ns/parent/empty", 0o755)
	if !r.Stat("mem://ns/parent").Info.IsDir {
		t.Error("missing directory-only ancestor")
	}
	entries, err := r.ReadDir(a, "mem://ns/parent")
	if err != nil || len(entries) != 1 || entries[0].Name != "empty" || !entries[0].IsDir {
		t.Error("explicit directory listed as file")
	}
	for i := range entries {
		mem.FreeString(a, entries[i].Name)
	}
	mem.FreeSlice(a, entries)
	entries, err = r.ReadDir(a, "mem://ns/parent/empty")
	if err != nil || len(entries) != 0 {
		t.Error("empty directory not readable")
	}
	for i := range entries {
		mem.FreeString(a, entries[i].Name)
	}
	mem.FreeSlice(a, entries)
	if r.Remove("mem://ns/parent") == nil {
		t.Error("nonempty ancestor removal accepted")
	}
	if r.WriteFileAtomic("mem://ns/parent/empty", nil, 0o644, false) == nil {
		t.Error("write accepted directory")
	}
	if r.Remove("mem://ns/parent/empty") != nil || r.Stat("mem://ns/parent/empty").Exists {
		t.Error("empty directory removal failed")
	}
	r.WriteFileAtomic("mem://ns/file", []byte("x"), 0o644, false)
	if r.Mkdir("mem://ns/file", 0o755) == nil {
		t.Error("mkdir accepted file")
	}
	entries, err = r.ReadDir(a, "mem://ns/file")
	if err == nil {
		t.Error("listing accepted file")
	}
	for i := range entries {
		mem.FreeString(a, entries[i].Name)
	}
	mem.FreeSlice(a, entries)
}
