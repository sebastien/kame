package host

import (
	"solod.dev/so/mem"
)

// ProgramHost is the complete host contract for materializing targets: process
// execution plus the filesystem and clock the build runtime needs. Solod does
// not support embedded interfaces, so every method is declared directly. It
// lives in a file that orders after the process types it refers to.
type ProgramHost interface {
	Start(request ProcessRequest) bool
	Pump(waitMS int) bool
	Next() ProcessEventResult
	Cancel(id int64) bool
	CancelAll()
	Active() int
	Free()
	Stat(name string) StatResult
	Lstat(name string) StatResult
	ReadFile(a mem.Allocator, name string) ([]byte, error)
	ReadDir(a mem.Allocator, name string) ([]DirEntry, error)
	WriteFileAtomic(name string, data []byte, perm uint32, durable bool) error
	Mkdir(name string, perm uint32) error
	Now() int64
	Monotonic() int64
}
