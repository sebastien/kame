package posix

import (
	"syscall"
	"time"
)

func waitDescriptor(fd int, write bool, timeout time.Duration) (bool, error) {
	var fds syscall.FdSet
	if fd < 0 || fd >= len(fds.Bits)*64 {
		return false, syscall.EBADF
	}
	fds.Bits[fd/64] |= 1 << uint(fd%64)
	tv := syscall.NsecToTimeval(max(0, timeout.Nanoseconds()))
	var readSet, writeSet *syscall.FdSet
	if write {
		writeSet = &fds
	} else {
		readSet = &fds
	}
	n, e := syscall.Select(fd+1, readSet, writeSet, nil, &tv)
	return n > 0, e
}
