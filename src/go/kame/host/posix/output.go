package posix

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

var ErrOutputTimeout = errors.New("cleanup output deadline exhausted")
var ErrOutputWrite = errors.New("cannot write public output")
var ErrOutputInterrupted = errors.New("public output interrupted")

// WriteOutput bypasses stdio so no later flush can escape the deadline.
// Zero timeout means ordinary I/O; a negative timeout is already expired.
// Finalizers use a deadline and shield ordinary interrupts.
func WriteOutput(fd int, data []byte, timeoutNS int64, interruptible bool) (int, error) {
	if timeoutNS < 0 {
		return 0, ErrOutputTimeout
	}
	var deadline time.Time
	if timeoutNS > 0 {
		deadline = time.Now().Add(time.Duration(timeoutNS))
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return 0, ErrOutputWrite
	}
	kind := descriptorType(fd)
	if kind == unix.S_IFIFO && flags&unix.O_NONBLOCK == 0 {
		copyFD, err := reopenPipe(fd, flags)
		if err == unix.ENXIO {
			return 0, unix.EPIPE
		}
		if err == nil {
			defer unix.Close(copyFD)
			fd, flags = copyFD, flags|unix.O_NONBLOCK
		}
	}
	if flags&unix.O_NONBLOCK == 0 && kind != unix.S_IFREG && kind != unix.S_IFSOCK {
		if unix.SetNonblock(fd, true) != nil {
			return 0, ErrOutputWrite
		}
		defer unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
	}
	written := 0
	for len(data) > 0 {
		if interruptible && SignalPending() {
			return written, ErrOutputInterrupted
		}
		wait := 100 * time.Millisecond
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return written, ErrOutputTimeout
			}
			wait = min(wait, remaining)
		}
		ready, err := waitDescriptor(fd, true, wait)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return written, ErrOutputWrite
		}
		if !ready {
			continue
		}
		chunk := data[:min(len(data), 4096)]
		var n int
		if kind == unix.S_IFSOCK {
			n, err = unix.SendmsgN(fd, chunk, nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		} else {
			n, err = unix.Write(fd, chunk)
		}
		if n > 0 {
			written += n
			data = data[n:]
		}
		if err == unix.EINTR || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			continue
		}
		if err == unix.EPIPE || (kind == unix.S_IFSOCK && err == unix.ECONNRESET) {
			return written, unix.EPIPE
		}
		if err != nil || n == 0 {
			return written, ErrOutputWrite
		}
	}
	return written, nil
}
