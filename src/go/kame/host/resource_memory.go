package host

import (
	"solod.dev/so/errors"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type memoryResourceFile struct {
	URI     string
	Data    []byte
	Mode    uint32
	ModTime int64
}

// MemoryResources is a host-owned store for canonical mem:// resources. It is
// deliberately explicit: each host owns an independent namespace set.
type MemoryResources struct {
	Alloc mem.Allocator
	Files []memoryResourceFile
	Dirs  []string
	Time  int64
}

func NewMemoryResources(a mem.Allocator) *MemoryResources {
	resources := mem.Alloc[MemoryResources](a)
	resources.Alloc = a
	return resources
}

func (r *MemoryResources) Free() {
	if r == nil {
		return
	}
	for i := range r.Files {
		mem.FreeString(r.Alloc, r.Files[i].URI)
		slices.Free(r.Alloc, r.Files[i].Data)
	}
	slices.Free(r.Alloc, r.Files)
	for i := range r.Dirs {
		mem.FreeString(r.Alloc, r.Dirs[i])
	}
	slices.Free(r.Alloc, r.Dirs)
	mem.Free(r.Alloc, r)
}

func (r *MemoryResources) fileIndex(uri string) int {
	for i := range r.Files {
		if r.Files[i].URI == uri {
			return i
		}
	}
	return -1
}

func (r *MemoryResources) isDir(uri string) bool {
	if uri == "" {
		return len(r.Files) != 0 || len(r.Dirs) != 0
	}
	for i := range r.Dirs {
		if r.Dirs[i] == uri {
			return true
		}
	}
	prefix := uri
	if prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}
	for i := range r.Files {
		if strings.HasPrefix(r.Files[i].URI, prefix) {
			return true
		}
	}
	for i := range r.Dirs {
		if strings.HasPrefix(r.Dirs[i], prefix) {
			return true
		}
	}
	return false
}

func (r *MemoryResources) Stat(uri string) StatResult {
	if r == nil {
		return StatResult{Failed: true}
	}
	if i := r.fileIndex(uri); i >= 0 {
		file := r.Files[i]
		return StatResult{Info: FileInfo{Size: int64(len(file.Data)), Mode: file.Mode, ModTime: file.ModTime, Regular: true}, Exists: true}
	}
	if r.isDir(uri) {
		return StatResult{Info: FileInfo{Mode: 0o755, ModTime: r.Time, IsDir: true}, Exists: true}
	}
	return StatResult{}
}

func (r *MemoryResources) ReadFile(a mem.Allocator, uri string) ([]byte, error) {
	if r == nil {
		return nil, errors.New("memory resource host unavailable")
	}
	if i := r.fileIndex(uri); i >= 0 {
		return slices.Clone(a, r.Files[i].Data), nil
	}
	return nil, errors.New("memory resource does not exist")
}

func (r *MemoryResources) ReadDir(a mem.Allocator, uri string) ([]DirEntry, error) {
	if r == nil || !r.isDir(uri) {
		return nil, errors.New("memory resource directory does not exist")
	}
	prefix := uri
	if prefix != "" && prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}
	var entries []DirEntry
	for i := range r.Files {
		rest, ok := resourceChild(r.Files[i].URI, prefix)
		if ok {
			addMemoryResourceEntry(a, &entries, rest, strings.IndexByte(rest, '/') >= 0)
		}
	}
	for i := range r.Dirs {
		rest, ok := resourceChild(r.Dirs[i], prefix)
		if ok {
			addMemoryResourceEntry(a, &entries, rest, strings.IndexByte(rest, '/') >= 0)
		}
	}
	return entries, nil
}

func resourceChild(uri string, prefix string) (string, bool) {
	if len(uri) <= len(prefix) || uri[:len(prefix)] != prefix {
		return "", false
	}
	return uri[len(prefix):], true
}

func addMemoryResourceEntry(a mem.Allocator, entries *[]DirEntry, rest string, nested bool) {
	name := rest
	isDir := nested
	if nested {
		name = rest[:strings.IndexByte(rest, '/')]
	}
	for i := range *entries {
		if (*entries)[i].Name == name {
			if isDir {
				(*entries)[i].IsDir = true
			}
			return
		}
	}
	copyName := mem.AllocSlice[byte](a, len(name), len(name))
	copy(copyName, name)
	*entries = slices.Append(a, *entries, DirEntry{Name: string(copyName), IsDir: isDir})
}

func (r *MemoryResources) WriteFileAtomic(uri string, data []byte, perm uint32, durable bool) error {
	_, _ = durable, perm
	if r == nil {
		return errors.New("memory resource host unavailable")
	}
	r.Time++
	if i := r.fileIndex(uri); i >= 0 {
		slices.Free(r.Alloc, r.Files[i].Data)
		r.Files[i].Data = slices.Clone(r.Alloc, data)
		r.Files[i].Mode, r.Files[i].ModTime = perm, r.Time
		return nil
	}
	uriCopy := mem.AllocSlice[byte](r.Alloc, len(uri), len(uri))
	copy(uriCopy, uri)
	r.Files = slices.Append(r.Alloc, r.Files, memoryResourceFile{URI: string(uriCopy), Data: slices.Clone(r.Alloc, data), Mode: perm, ModTime: r.Time})
	return nil
}

func (r *MemoryResources) Mkdir(uri string, perm uint32) error {
	_ = perm
	if r == nil {
		return errors.New("memory resource host unavailable")
	}
	if r.isDir(uri) {
		return nil
	}
	copyURI := mem.AllocSlice[byte](r.Alloc, len(uri), len(uri))
	copy(copyURI, uri)
	r.Dirs = slices.Append(r.Alloc, r.Dirs, string(copyURI))
	r.Time++
	return nil
}

func (r *MemoryResources) Remove(uri string) error {
	if r == nil {
		return errors.New("memory resource host unavailable")
	}
	if i := r.fileIndex(uri); i >= 0 {
		mem.FreeString(r.Alloc, r.Files[i].URI)
		slices.Free(r.Alloc, r.Files[i].Data)
		for j := i; j+1 < len(r.Files); j++ {
			r.Files[j] = r.Files[j+1]
		}
		r.Files = r.Files[:len(r.Files)-1]
		if len(r.Files) == 0 {
			slices.Free(r.Alloc, r.Files)
			r.Files = nil
		}
		r.Time++
		return nil
	}
	for i := range r.Dirs {
		if r.Dirs[i] == uri {
			mem.FreeString(r.Alloc, r.Dirs[i])
			for j := i; j+1 < len(r.Dirs); j++ {
				r.Dirs[j] = r.Dirs[j+1]
			}
			r.Dirs = r.Dirs[:len(r.Dirs)-1]
			if len(r.Dirs) == 0 {
				slices.Free(r.Alloc, r.Dirs)
				r.Dirs = nil
			}
			r.Time++
			return nil
		}
	}
	return errors.New("memory resource does not exist")
}
