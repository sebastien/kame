package posix

import "golang.org/x/sys/unix"

// observeProcess blocks until pid exits without reaping it. Darwin's kqueue
// reports no stops, so changed is never called.
// ponytail: stop observation is Linux-only; Darwin job control needs a
// waitpid(WUNTRACED|WNOHANG) poller if it is ever required.
func observeProcess(pid int, changed func(stopped bool, signal int)) error {
	queue, e := unix.Kqueue()
	if e != nil {
		return e
	}
	defer unix.Close(queue)
	change := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	var events [1]unix.Kevent_t
	for {
		n, e := unix.Kevent(queue, change, events[:], nil)
		if e == unix.EINTR {
			continue
		}
		if e == unix.ESRCH {
			return nil
		}
		if e != nil {
			return e
		}
		if n > 0 {
			if events[0].Flags&unix.EV_ERROR != 0 && events[0].Data != 0 && events[0].Data != int64(unix.ESRCH) {
				return unix.Errno(events[0].Data)
			}
			return nil
		}
	}
}
