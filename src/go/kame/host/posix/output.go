package posix

import (
	"errors"
	"golang.org/x/sys/unix"
	"kame/internal/c"
	"syscall"
	"time"
)

var ErrOutputTimeout = errors.New("cleanup output deadline exhausted")
var ErrOutputWrite = errors.New("cannot write public output")

func km_cli_write_output(fd c.Int, data []byte, timeoutNS int64, written *int) c.Int {
	*written = 0
	if timeoutNS < 0 {
		return 1
	}
	var deadline time.Time
	if timeoutNS > 0 {
		deadline = time.Now().Add(time.Duration(timeoutNS))
	}
	flags, e := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if e != nil {
		return -1
	}
	if flags&unix.O_NONBLOCK == 0 {
		if unix.SetNonblock(int(fd), true) != nil {
			return -1
		}
		defer unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
	}
	for len(data) > 0 {
		wait := 100 * time.Millisecond
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return 1
			}
			wait = min(wait, remaining)
		}
		ready, e := waitDescriptor(int(fd), true, wait)
		if e == syscall.EINTR {
			continue
		}
		if e != nil {
			return -1
		}
		if !ready {
			if deadline.IsZero() && SignalPending() {
				return -1
			}
			continue
		}
		n, e := syscall.Write(int(fd), data[:min(len(data), 4096)])
		if n > 0 {
			*written += n
			data = data[n:]
		}
		if e == syscall.EINTR || e == syscall.EAGAIN || e == syscall.EWOULDBLOCK {
			continue
		}
		if e != nil || n == 0 {
			return -1
		}
	}
	return 0
}

// WriteOutput writes directly to the public descriptor, without a stdio buffer
// whose later flush could escape the deadline. Zero timeout means ordinary I/O;
// a negative timeout is already expired. Descriptor flags are restored on exit.
func WriteOutput(fd int, data []byte, timeoutNS int64) (int, error) {
	written := 0
	status := km_cli_write_output(c.Int(fd), data, timeoutNS, &written)
	if status == 1 {
		return written, ErrOutputTimeout
	}
	if status != 0 {
		return written, ErrOutputWrite
	}
	return written, nil
}
