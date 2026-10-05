// Package host defines portable requests issued by evaluators and runtimes.
package host

import (
	"solod.dev/so/mem"
)

// FileInfo is a portable snapshot of one path. ModTime is unix nanoseconds so
// freshness comparisons never depend on a platform time type.
type FileInfo struct {
	Size    int64
	Mode    uint32
	ModTime int64
	IsDir   bool
	Regular bool
}

// StatResult is a single-return stat outcome. Solod cannot wrap a struct and an
// error in one multi-return, so absence and failure are explicit flags:
// Exists is true for a present path, Failed is true for any error other than
// not-found, and both false means the path is simply absent.
type StatResult struct {
	Info   FileInfo
	Exists bool
	Failed bool
}

// DirEntry is one directory member. ReadDir does not follow symlinks.
type DirEntry struct {
	Name  string
	IsDir bool
}

// FileSystem is the portable filesystem contract used by the build runtime. It
// deliberately covers exactly the operations the runtime needs, so a browser or
// embedded host can service them without a POSIX layer.
type FileSystem interface {
	Stat(name string) StatResult
	Lstat(name string) StatResult
	ReadFile(a mem.Allocator, name string) ([]byte, error)
	ReadDir(a mem.Allocator, name string) ([]DirEntry, error)
	// WriteFileAtomic writes a sibling temporary and renames it into place. A
	// durable write syncs the temporary before the rename; effects use the
	// non-durable form and the cache uses the durable form.
	WriteFileAtomic(name string, data []byte, perm uint32, durable bool) error
	Mkdir(name string, perm uint32) error
	Remove(name string) error
	// Now returns wall-clock unix nanoseconds for cache retention accounting.
	Now() int64
}
