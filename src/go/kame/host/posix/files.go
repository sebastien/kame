package posix

import (
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/time"
)

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
	}
}

func (h *Host) Stat(name string) host.StatResult {
	_ = h
	info, err := os.Stat(name)
	if err != nil {
		return host.StatResult{Failed: err != os.ErrNotExist}
	}
	result := portableFileInfo(info)
	// Solod 0.4.0 drops stat nanoseconds. Preserve them for same-second builds.
	if stamp := km_file_modtime(name, false); stamp != 0 {
		result.ModTime = stamp
	}
	return host.StatResult{Info: result, Exists: true}
}

func (h *Host) Lstat(name string) host.StatResult {
	_ = h
	info, err := os.Lstat(name)
	if err != nil {
		return host.StatResult{Failed: err != os.ErrNotExist}
	}
	result := portableFileInfo(info)
	if stamp := km_file_modtime(name, true); stamp != 0 {
		result.ModTime = stamp
	}
	return host.StatResult{Info: result, Exists: true}
}

func (h *Host) ReadFile(a mem.Allocator, name string) ([]byte, error) {
	_ = h
	return os.ReadFile(a, name)
}

func (h *Host) ReadDir(a mem.Allocator, name string) ([]host.DirEntry, error) {
	_ = h
	entries, err := os.ReadDir(a, name)
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
// cache writes also sync before rename. Failures remove the temporary.
func (h *Host) WriteFileAtomic(name string, data []byte, perm uint32, durable bool) error {
	_ = h
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
	_, err = f.Write(data)
	if err == nil {
		err = os.Chmod(temporary, os.FileMode(perm))
	}
	if err == nil && durable {
		err = f.Sync()
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
	return nil
}

func (h *Host) Mkdir(name string, perm uint32) error {
	_ = h
	return os.Mkdir(name, os.FileMode(perm))
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
