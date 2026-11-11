package posix

import (
	"syscall"
	"time"
)

func waitDescriptor(fd int, write bool, timeout time.Duration) (bool, error) {
	var fds syscall.FdSet
	if fd < 0 || fd >= len(fds.Bits)*32 {
		return false, syscall.EBADF
	}
	bit := int32(1) << uint(fd%32)
	fds.Bits[fd/32] |= bit
	tv := syscall.NsecToTimeval(max(0, timeout.Nanoseconds()))
	var readSet, writeSet *syscall.FdSet
	if write {
		writeSet = &fds
	} else {
		readSet = &fds
	}
	e := syscall.Select(fd+1, readSet, writeSet, nil, &tv)
	return e == nil && fds.Bits[fd/32]&bit != 0, e
}
