package posix

import (
	"kame/internal/c"
	"os"
)

func syncStorage(f *os.File) c.Int {
	if f.Sync() != nil {
		return -1
	}
	return 0
}
func km_file_sync_parent(directory string) c.Int {
	f, e := os.Open(directory)
	if e != nil {
		return -1
	}
	defer f.Close()
	return syncStorage(f)
}
