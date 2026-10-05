package wasm

import (
	"kame/host"
	"solod.dev/so/errors"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// MemoryHost is a synchronous in-memory ProgramHost. It lets the freestanding
// runtime materialize targets whose files and clock the embedding host has
// supplied. docs/spec/010-wasm.md explicitly allows a browser host to map host
// requests to an in-memory filesystem like this one.
type MemoryHost struct {
	Alloc mem.Allocator
	Files []memoryFile
	// Dirs records directories created with Mkdir. Directories are otherwise
	// derived from file paths; an explicit empty directory needs this list.
	Dirs []string
	Time int64
}

type memoryFile struct {
	Path string
	Data []byte
}

func NewMemoryHost(a mem.Allocator) *MemoryHost {
	h := mem.Alloc[MemoryHost](a)
	h.Alloc = a
	return h
}

// SetFile stores or replaces one file. Paths are the canonical relative names
// the runtime produces with a "." working directory.
func (h *MemoryHost) SetFile(path string, data []byte) {
	if h == nil {
		return
	}
	if i := h.findFile(path); i >= 0 {
		slices.Free(h.Alloc, h.Files[i].Data)
		h.Files[i].Data = slices.Clone(h.Alloc, data)
		return
	}
	h.Files = slices.Append(h.Alloc, h.Files, memoryFile{Path: pureText(h.Alloc, path), Data: slices.Clone(h.Alloc, data)})
}

func (h *MemoryHost) SetTime(nanos int64) {
	if h == nil {
		return
	}
	h.Time = nanos
}

func (h *MemoryHost) findFile(name string) int {
	for i := range h.Files {
		if h.Files[i].Path == name {
			return i
		}
	}
	return -1
}

func (h *MemoryHost) isDir(name string) bool {
	if name == "." || name == "" {
		return len(h.Files) != 0 || len(h.Dirs) != 0
	}
	for i := range h.Dirs {
		if h.Dirs[i] == name {
			return true
		}
	}
	prefix := name + "/"
	for i := range h.Files {
		if strings.HasPrefix(h.Files[i].Path, prefix) {
			return true
		}
	}
	return false
}

func (h *MemoryHost) Stat(name string) host.StatResult {
	if h == nil {
		return host.StatResult{}
	}
	if i := h.findFile(name); i >= 0 {
		return host.StatResult{Info: host.FileInfo{Size: int64(len(h.Files[i].Data)), ModTime: h.Time, Regular: true}, Exists: true}
	}
	if h.isDir(name) {
		return host.StatResult{Info: host.FileInfo{ModTime: h.Time, IsDir: true}, Exists: true}
	}
	return host.StatResult{}
}

func (h *MemoryHost) Lstat(name string) host.StatResult {
	return h.Stat(name)
}

func (h *MemoryHost) ReadFile(a mem.Allocator, name string) ([]byte, error) {
	if h == nil {
		return nil, errors.New("host unavailable")
	}
	if i := h.findFile(name); i >= 0 {
		return slices.Clone(a, h.Files[i].Data), nil
	}
	return nil, errors.New("file does not exist")
}

func (h *MemoryHost) ReadDir(a mem.Allocator, name string) ([]host.DirEntry, error) {
	if h == nil {
		return nil, errors.New("host unavailable")
	}
	prefix := name
	if prefix == "." || prefix == "" {
		prefix = ""
	} else {
		prefix = prefix + "/"
	}
	var entries []host.DirEntry
	for i := range h.Files {
		path := h.Files[i].Path
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := path[len(prefix):]
		if rest == "" {
			continue
		}
		cut := strings.IndexByte(rest, '/')
		if cut < 0 {
			entries = addEntry(a, entries, rest, false)
		} else {
			entries = addEntry(a, entries, rest[:cut], true)
		}
	}
	if len(entries) == 0 {
		return nil, errors.New("directory does not exist")
	}
	return entries, nil
}

func addEntry(a mem.Allocator, entries []host.DirEntry, name string, isDir bool) []host.DirEntry {
	for i := range entries {
		if entries[i].Name == name {
			if isDir {
				entries[i].IsDir = true
			}
			return entries
		}
	}
	entry := host.DirEntry{Name: pureText(a, name), IsDir: isDir}
	at := len(entries)
	for at > 0 && entries[at-1].Name > name {
		at--
	}
	entries = slices.Append(a, entries, host.DirEntry{})
	copy(entries[at+1:], entries[at:])
	entries[at] = entry
	return entries
}

func (h *MemoryHost) WriteFileAtomic(name string, data []byte, perm uint32, durable bool) error {
	_ = perm
	_ = durable
	if h == nil {
		return errors.New("host unavailable")
	}
	h.SetFile(name, data)
	return nil
}

func (h *MemoryHost) Mkdir(name string, perm uint32) error {
	_ = perm
	if h == nil {
		return errors.New("host unavailable")
	}
	for i := range h.Dirs {
		if h.Dirs[i] == name {
			return nil
		}
	}
	h.Dirs = slices.Append(h.Alloc, h.Dirs, pureText(h.Alloc, name))
	return nil
}

func (h *MemoryHost) Remove(name string) error {
	if h == nil {
		return errors.New("host unavailable")
	}
	i := h.findFile(name)
	if i < 0 {
		return errors.New("file does not exist")
	}
	mem.FreeString(h.Alloc, h.Files[i].Path)
	slices.Free(h.Alloc, h.Files[i].Data)
	copy(h.Files[i:], h.Files[i+1:])
	h.Files = h.Files[:len(h.Files)-1]
	return nil
}

func (h *MemoryHost) LockCache(path string, stripe int) bool { _, _, _ = h, path, stripe; return true }

func (h *MemoryHost) UnlockCache(stripe int) { _, _ = h, stripe }

func (h *MemoryHost) Now() int64 {
	if h == nil {
		return 0
	}
	return h.Time
}

func (h *MemoryHost) Monotonic() int64 {
	if h == nil {
		return 0
	}
	return h.Time
}

func (h *MemoryHost) Start(request host.ProcessRequest) bool {
	_, _ = h, request
	return false
}

func (h *MemoryHost) Pump(waitMS int) bool {
	_, _ = h, waitMS
	return false
}

func (h *MemoryHost) Next() host.ProcessEventResult {
	_ = h
	return host.ProcessEventResult{}
}

func (h *MemoryHost) Cancel(id int64) bool {
	_, _ = h, id
	return false
}

func (h *MemoryHost) Stop(id int64, graceMS int64) bool {
	_, _, _ = h, id, graceMS
	return false
}

func (h *MemoryHost) CancelAll() { _ = h }

func (h *MemoryHost) Active() int {
	_ = h
	return 0
}

func (h *MemoryHost) Free() {
	if h == nil {
		return
	}
	for i := range h.Files {
		mem.FreeString(h.Alloc, h.Files[i].Path)
		slices.Free(h.Alloc, h.Files[i].Data)
	}
	for i := range h.Dirs {
		mem.FreeString(h.Alloc, h.Dirs[i])
	}
	slices.Free(h.Alloc, h.Dirs)
	slices.Free(h.Alloc, h.Files)
	a := h.Alloc
	*h = MemoryHost{}
	mem.Free(a, h)
}
