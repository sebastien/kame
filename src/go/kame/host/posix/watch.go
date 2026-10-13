package posix

import (
	"kame/core"
	"solod.dev/so/c"
)

//so:extern km_watch
type Watcher struct{}

//so:extern
func km_watch_new() *Watcher { return nil }

//so:extern
func km_watch_begin(w *Watcher) { _ = w }

//so:extern nodecay
func km_watch_add(w *Watcher, name string, glob bool) bool { _, _, _ = w, name, glob; return false }

//so:extern
func km_watch_commit(w *Watcher) bool { _ = w; return false }

//so:extern
func km_watch_wait(w *Watcher, wait c.Int) c.Int { _, _ = w, wait; return 0 }

//so:extern
func km_watch_free(w *Watcher) { _ = w }

func NewWatcher() *Watcher                   { return km_watch_new() }
func (w *Watcher) Begin()                    { km_watch_begin(w) }
func (w *Watcher) Commit() bool              { return km_watch_commit(w) }
func (w *Watcher) Wait(milliseconds int) int { return int(km_watch_wait(w, c.Int(milliseconds))) }
func (w *Watcher) Free()                     { km_watch_free(w) }

func (h *Host) AddWatch(w *Watcher, name string, glob bool) bool {
	if core.IsMemoryResourceURIName(name) {
		return false
	}
	path := h.hostPath(name)
	defer h.freeHostPath(path)
	if path.Error != nil {
		return false
	}
	return km_watch_add(w, path.Path, glob)
}
