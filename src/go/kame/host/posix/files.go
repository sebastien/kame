package posix

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/c"
	"solod.dev/so/errors"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/time"
	"solod.dev/so/unicode/utf8"
)

var errInvalidFileResourceURI = errors.New("invalid file resource URI")
var errStorageSync = errors.New("storage sync failed (rename may already be published)")

type hostPathResult struct {
	Path  string
	Owned bool
	Error error
}

func (h *Host) hostPath(name string) hostPathResult {
	if len(name) < 7 || name[:7] != "file://" {
		return hostPathResult{Path: name}
	}
	parsed := core.ParseResourceURI(h.Alloc, name)
	if parsed.Error != "" {
		parsed.URI.Free()
		return hostPathResult{Error: errInvalidFileResourceURI}
	}
	path := cloneText(h.Alloc, parsed.URI.Path)
	parsed.URI.Free()
	return hostPathResult{Path: path, Owned: true}
}

func (h *Host) freeHostPath(path hostPathResult) {
	if path.Owned {
		mem.FreeString(h.Alloc, path.Path)
	}
}

func cloneText(a mem.Allocator, text string) string {
	if text == "" {
		return ""
	}
	buffer := slices.Make[byte](a, len(text))
	copy(buffer, text)
	return string(buffer)
}

func portableFileInfo(info os.FileInfo) host.FileInfo {
	return host.FileInfo{
		Size:    info.Size(),
		Mode:    uint32(info.Mode()),
		ModTime: info.ModTime().UnixNano(),
		IsDir:   info.IsDir(),
		Regular: info.Mode().IsRegular(),
		Symlink: info.Mode()&os.ModeSymlink != 0,
	}
}

func (h *Host) Stat(name string) host.StatResult {
	if core.IsMemoryResourceURIName(name) {
		return h.Memory.Stat(name)
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return host.StatResult{Failed: true}
	}
	info, err := os.Stat(path.Path)
	if err != nil {
		return host.StatResult{Failed: err != os.ErrNotExist}
	}
	result := portableFileInfo(info)
	// Solod 0.4.0 drops stat nanoseconds. Preserve them for same-second builds.
	if stamp := km_file_modtime(path.Path, false); stamp != 0 {
		result.ModTime = stamp
	}
	return host.StatResult{Info: result, Exists: true}
}

func (h *Host) Lstat(name string) host.StatResult {
	if core.IsMemoryResourceURIName(name) {
		return h.Memory.Stat(name)
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return host.StatResult{Failed: true}
	}
	info, err := os.Lstat(path.Path)
	if err != nil {
		return host.StatResult{Failed: err != os.ErrNotExist}
	}
	result := portableFileInfo(info)
	if stamp := km_file_modtime(path.Path, true); stamp != 0 {
		result.ModTime = stamp
	}
	return host.StatResult{Info: result, Exists: true}
}

// WatchStamp owns a cheap polling hint; empty means full content checks are
// required. Memory resources have no OS change timestamp.
func (h *Host) WatchStamp(name string) string {
	if core.IsMemoryResourceURIName(name) {
		return ""
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return ""
	}
	data := slices.Make[byte](h.Alloc, 98)
	if !km_file_watch_stamp(path.Path, data) {
		slices.Free(h.Alloc, data)
		return ""
	}
	return string(data)
}

func (h *Host) ReadFile(a mem.Allocator, name string) ([]byte, error) {
	if core.IsMemoryResourceURIName(name) {
		return h.Memory.ReadFile(a, name)
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return nil, path.Error
	}
	return os.ReadFile(a, path.Path)
}

func (h *Host) Readlink(a mem.Allocator, name string) host.LinkResult {
	if core.IsMemoryResourceURIName(name) { return host.LinkResult{Failed: true} }
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil { return host.LinkResult{Failed: true} }
	// Solod Readlink silently truncates a full buffer. Grow until completeness is
	// proven, never return a truncated target as an ownership check.
	for size := 256; size <= 65536; size *= 2 {
		buffer := slices.Make[byte](a, size)
		text, err := os.Readlink(buffer, path.Path)
		if err != nil { slices.Free(a, buffer); return host.LinkResult{Failed: true} }
		if len(text) < size {
			if !utf8.ValidString(text) { slices.Free(a, buffer); return host.LinkResult{Failed: true} }
			result := host.LinkResult{Target: cloneText(a, text)}
			slices.Free(a, buffer)
			return result
		}
		slices.Free(a, buffer)
	}
	return host.LinkResult{Failed: true}
}

func (h *Host) ReadDir(a mem.Allocator, name string) ([]host.DirEntry, error) {
	if core.IsMemoryResourceURIName(name) {
		return h.Memory.ReadDir(a, name)
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return nil, path.Error
	}
	entries, err := os.ReadDir(a, path.Path)
	if err != nil {
		return nil, err
	}
	defer os.FreeDirEntry(a, entries)
	out := slices.Make[host.DirEntry](a, len(entries))
	for i := range entries {
		// os.ReadDir names are views into the entry allocation, so clone them
		// for a caller that outlives the freed entry list.
		out[i] = host.DirEntry{Name: cloneText(a, entries[i].Name), IsDir: entries[i].IsDir}
	}
	return out, nil
}

// WriteFileAtomic uses an exclusively created sibling temp so concurrent writes
// cannot share a staging file or follow a pre-created staging symlink. Durable
// cache writes sync storage before rename and the parent directory afterwards.
// A parent-sync failure is reported even though rename is already published.
// Filesystems/platforms without directory fsync cannot claim durable success.
func (h *Host) WriteFileAtomic(name string, data []byte, perm uint32, durable bool) error {
	if core.IsMemoryResourceURIName(name) {
		return h.Memory.WriteFileAtomic(name, data, perm, durable)
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return path.Error
	}
	name = path.Path
	separator := strings.LastIndexByte(name, '/')
	directory := "."
	if separator >= 0 {
		directory = name[:separator]
	}
	if separator == 0 {
		directory = "/"
	}
	buffer := make([]byte, len(directory)+len("/.kame-tmp-")+7)
	f, err := os.CreateTemp(buffer, directory, ".kame-tmp-")
	if err != nil {
		return err
	}
	temporary := f.Name()
	written, writeErr := f.Write(data)
	err = writeErr
	if err == nil && written != len(data) {
		err = os.ErrInvalid
	}
	if err == nil {
		err = os.Chmod(temporary, os.FileMode(perm))
	}
	if err == nil && durable {
		if syncStorage(&f) != 0 {
			err = errStorageSync
		}
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporary, name)
	}
	if err != nil {
		os.Remove(temporary)
		return err
	}
	if durable && km_file_sync_parent(directory) != 0 {
		return errStorageSync
	}
	return nil
}

func (h *Host) Mkdir(name string, perm uint32) error {
	if core.IsMemoryResourceURIName(name) {
		return h.Memory.Mkdir(name, perm)
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return path.Error
	}
	return os.Mkdir(path.Path, os.FileMode(perm))
}

func (h *Host) Remove(name string) error {
	if core.IsMemoryResourceURIName(name) {
		return h.Memory.Remove(name)
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return path.Error
	}
	return os.Remove(path.Path)
}

func (h *Host) LockCache(name string, stripe int) bool {
	return h != nil && h.native != nil && km_host_cache_lock(h.native, name, c.Int(stripe)) == 0
}

func (h *Host) UnlockCache(stripe int) {
	if h != nil && h.native != nil {
		km_host_cache_unlock(h.native, c.Int(stripe))
	}
}

func (h *Host) Now() int64 {
	_ = h
	return time.Now().UnixNano()
}

// Monotonic reports nanoseconds since the host was created. It never jumps with
// wall-clock adjustments, so it is suitable for measuring durations.
func (h *Host) Monotonic() int64 {
	return int64(time.Since(h.start))
}
