package posix

import "golang.org/x/sys/unix"

func notificationOpen() int {
	fd, e := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if e != nil {
		return -1
	}
	return fd
}
func notificationAdd(fd int, name string) bool {
	_, e := unix.InotifyAddWatch(fd, name, unix.IN_MODIFY|unix.IN_ATTRIB|unix.IN_CREATE|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF)
	return e == nil
}
func notificationWait(fd, milliseconds int) int {
	descriptors := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, e := unix.Poll(descriptors, max(0, milliseconds))
	if e == unix.EINTR {
		return 0
	}
	if e != nil {
		return -1
	}
	if n == 0 {
		return 0
	}
	if descriptors[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
		return -1
	}
	var data [16384]byte
	for {
		n, e := unix.Read(fd, data[:])
		if e == unix.EINTR {
			continue
		}
		if n <= 0 {
			break
		}
	}
	return 1
}
