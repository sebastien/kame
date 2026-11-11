package posix

import (
	"golang.org/x/sys/unix"
	"io/fs"
	"kame/core"
	"os"
	"path/filepath"
	"strings"
)

// Watcher replaces its subscriptions before fingerprint reconciliation, so
// notifications arriving during the scan remain queued on the new descriptor.
type Watcher struct {
	fd, next int
	complete bool
}

func NewWatcher() *Watcher { return &Watcher{fd: -1, next: -1} }
func (w *Watcher) Begin() {
	if w.next >= 0 {
		unix.Close(w.next)
	}
	w.next = notificationOpen()
	w.complete = w.next >= 0
}
func (w *Watcher) Commit() bool {
	if w.fd >= 0 {
		unix.Close(w.fd)
	}
	w.fd, w.next = w.next, -1
	return w.complete
}
func (w *Watcher) Wait(milliseconds int) int { return notificationWait(w.fd, milliseconds) }
func (w *Watcher) Free() {
	if w == nil {
		return
	}
	if w.fd >= 0 {
		unix.Close(w.fd)
	}
	if w.next >= 0 {
		unix.Close(w.next)
	}
	w.fd, w.next = -1, -1
}
func (w *Watcher) directory(name string, recursive bool) bool {
	for {
		info, e := os.Stat(name)
		if e == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(name)
		if parent == name {
			return false
		}
		name = parent
		recursive = false
	}
	if resolved, e := filepath.EvalSymlinks(name); e == nil {
		name = resolved
	}
	if !recursive {
		return notificationAdd(w.next, name)
	}
	baseDepth := strings.Count(name, string(filepath.Separator))
	complete := true
	err := filepath.WalkDir(name, func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			complete = false
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if strings.Count(path, string(filepath.Separator))-baseDepth > 128 {
			complete = false
			return filepath.SkipDir
		}
		if !notificationAdd(w.next, path) {
			complete = false
		}
		return nil
	})
	return err == nil && complete
}
func (w *Watcher) add(name string, glob bool) bool {
	if w == nil {
		return false
	}
	if w.next < 0 || name == "" || strings.IndexByte(name, 0) >= 0 {
		w.complete = false
		return false
	}
	ok := true
	if glob {
		if at := strings.IndexAny(name, "*?[\\"); at >= 0 {
			name = name[:at]
		}
		ok = w.directory(filepath.Dir(name), true)
	} else {
		if target, e := filepath.EvalSymlinks(name); e == nil {
			ok = w.directory(filepath.Dir(target), false)
		}
		if target, e := os.Readlink(name); e == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(name), target)
			}
			ok = w.directory(filepath.Dir(target), false) && ok
		}
		ok = w.directory(filepath.Dir(name), false) && ok
	}
	w.complete = w.complete && ok
	return ok
}
func (h *Host) AddWatch(w *Watcher, name string, glob bool) bool {
	if core.IsMemoryResourceURIName(name) {
		return false
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return false
	}
	return w.add(path.Path, glob)
}
