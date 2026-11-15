package posix

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// waitid child codes (siginfo si_code).
const (
	cldTrapped   = 4
	cldStopped   = 5
	cldContinued = 6
)

// observeProcess blocks until pid exits without reaping it, reporting each
// stop (with its signal) and continue on the way. Stop and continue
// notifications are consumed by a separate call without WEXITED, so the exit
// status always stays waitable for exec.Cmd.Wait.
func observeProcess(pid int, changed func(stopped bool, signal int)) error {
	var info unix.Siginfo
	for {
		e := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WSTOPPED|unix.WCONTINUED|unix.WNOWAIT, nil)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return e
		}
		if info.Code != cldStopped && info.Code != cldTrapped && info.Code != cldContinued {
			return nil
		}
		var consumed unix.Siginfo
		if e := unix.Waitid(unix.P_PID, pid, &consumed, unix.WSTOPPED|unix.WCONTINUED|unix.WNOHANG, nil); e != nil && e != unix.EINTR {
			return e
		}
		if siginfoPID(&consumed) == 0 {
			continue // Raced with another state change; observe it next.
		}
		switch consumed.Code {
		case cldStopped, cldTrapped:
			changed(true, siginfoStatus(&consumed))
		case cldContinued:
			changed(false, 0)
		}
	}
}

// For SIGCHLD, siginfo_t's union follows three ints, aligned to a pointer,
// and holds si_pid, si_uid, si_status.
const siginfoUnion = (12 + unsafe.Sizeof(uintptr(0)) - 1) &^ (unsafe.Sizeof(uintptr(0)) - 1)

func siginfoPID(info *unix.Siginfo) int32 {
	return *(*int32)(unsafe.Add(unsafe.Pointer(info), siginfoUnion))
}

func siginfoStatus(info *unix.Siginfo) int {
	return int(*(*int32)(unsafe.Add(unsafe.Pointer(info), siginfoUnion+8)))
}
