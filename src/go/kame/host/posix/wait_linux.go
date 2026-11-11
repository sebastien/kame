package posix

import "golang.org/x/sys/unix"

func observeProcessExit(pid int) error {
	var info unix.Siginfo
	for {
		e := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if e == unix.EINTR {
			continue
		}
		return e
	}
}
