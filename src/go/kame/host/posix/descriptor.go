package posix

import (
	"math"
	"os"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

func descriptorType(fd int) uint32 {
	var info unix.Stat_t
	if unix.Fstat(fd, &info) != nil {
		return 0
	}
	return uint32(info.Mode) & unix.S_IFMT
}

// reopenPipe gives a Linux pipe a separate set of status flags, without
// changing its byte queue. A dup (including /dev/fd on Darwin) shares flags.
func reopenPipe(fd, flags int) (int, error) {
	if runtime.GOOS != "linux" {
		return -1, unix.ENOTSUP
	}
	// Do not wait for a peer while reopening the pipe.
	return unix.Open("/proc/self/fd/"+strconv.Itoa(fd), flags&unix.O_ACCMODE|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
}

func blockingPipe(fd, flags int) (int, error) {
	copyFD, err := reopenPipe(fd, flags)
	if err != nil {
		return -1, err
	}
	if err := unix.SetNonblock(copyFD, false); err != nil {
		unix.Close(copyFD)
		return -1, err
	}
	return copyFD, nil
}

func invocationInput(fd int) (*os.File, error) {
	copyFD, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	flags, err := unix.FcntlInt(uintptr(copyFD), unix.F_GETFL, 0)
	if err == nil && flags&unix.O_NONBLOCK != 0 && descriptorType(copyFD) == unix.S_IFIFO {
		if blockingFD, err := blockingPipe(copyFD, flags); err == nil {
			unix.Close(copyFD)
			copyFD = blockingFD
		}
	}
	// Files retain the caller's offset; borrowing never reads ahead.
	return os.NewFile(uintptr(copyFD), "invocation input"), nil
}

func waitDescriptor(fd int, write bool, timeout time.Duration) (bool, error) {
	if fd < 0 || fd > math.MaxInt32 {
		return false, unix.EBADF
	}
	events := int16(unix.POLLIN)
	if write {
		events = unix.POLLOUT
	}
	poll := [1]unix.PollFd{{Fd: int32(fd), Events: events}}
	ready, err := unix.Poll(poll[:], int((timeout+time.Millisecond-1)/time.Millisecond))
	if poll[0].Revents&unix.POLLNVAL != 0 {
		return false, unix.EBADF
	}
	return ready > 0, err
}
