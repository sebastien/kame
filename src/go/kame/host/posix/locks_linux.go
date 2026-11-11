package posix

import (
	"bufio"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func lockKernelOwner(fd int) int64 {
	var info syscall.Stat_t
	if syscall.Fstat(fd, &info) != nil {
		return 0
	}
	file, e := os.Open("/proc/locks")
	if e != nil {
		return 0
	}
	defer file.Close()
	var owner int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var number, major, minor, inode uint64
		var kind, mode, access string
		var pid int64
		n, _ := fmt.Sscanf(scanner.Text(), "%d: %s %s %s %d %x:%x:%d", &number, &kind, &mode, &access, &pid, &major, &minor, &inode)
		if n == 8 && kind == "FLOCK" && (access == "WRITE" || access == "READ") && pid > 0 && major == uint64(unix.Major(uint64(info.Dev))) && minor == uint64(unix.Minor(uint64(info.Dev))) && inode == info.Ino {
			owner = pid
			if access == "WRITE" {
				break
			}
		}
	}
	return owner
}
