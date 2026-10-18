package host

import (
	"solod.dev/so/mem"
)

// CacheLockStatus keeps cancellation distinct from an unavailable cache, which
// may otherwise degrade to execution without cache coordination.
type CacheLockStatus int

const (
	CacheLockUnavailable CacheLockStatus = iota
	CacheLockAcquired
	CacheLockCancelled
)

// ProgramHost is the complete host contract for materializing targets: process
// execution plus the filesystem and clock the build runtime needs. Solod does
// not support embedded interfaces, so every method is declared directly. It
// lives in a file that orders after the process types it refers to.
type ProgramHost interface {
	SupportsStreamOperands() bool
	SupportsExecutor(name string, version string) bool
	Start(request ProcessRequest) bool
	Pump(waitMS int) bool
	Next() ProcessEventResult
	Cancel(id int64) bool
	Stop(id int64, graceMS int64) bool
	CancelAll()
	Active() int
	Free()
	Stat(name string) StatResult
	Lstat(name string) StatResult
	Readlink(a mem.Allocator, name string) LinkResult
	ReadFile(a mem.Allocator, name string) ([]byte, error)
	ReadDir(a mem.Allocator, name string) ([]DirEntry, error)
	WriteFileAtomic(name string, data []byte, perm uint32, durable bool) error
	Mkdir(name string, perm uint32) error
	Remove(name string) error
	LockCache(path string, stripe int) CacheLockStatus
	UnlockCache(stripe int)
	Now() int64
	Monotonic() int64
}
