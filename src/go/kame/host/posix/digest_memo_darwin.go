package posix

import (
	"os"
	"syscall"
)

func statIdentity(info os.FileInfo) (fileIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, false
	}
	return fileIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Size: uint64(stat.Size), ModifiedNs: stat.Mtimespec.Nano(), ChangedNs: stat.Ctimespec.Nano()}, true
}
