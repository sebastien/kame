package posix

import "time"

// macOS retains the portable fingerprint polling fallback.
func notificationOpen() int                    { return -1 }
func notificationAdd(fd int, name string) bool { return false }
func notificationWait(fd, milliseconds int) int {
	time.Sleep(time.Duration(max(0, milliseconds)) * time.Millisecond)
	return 0
}
