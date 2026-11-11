//go:build linux && kame_faults

// These hooks are linked only into the acceptance test binary. Production
// builds neither read the fault environment nor test marker files.
package ownership

import (
	"golang.org/x/sys/unix"
	"kame/host/posix"
	"os"
	"strings"
	"time"
)

func takeFault(dir int) string {
	mode := os.Getenv("KAME_CLEAN_TEST_FAULT")
	if mode == "" {
		return ""
	}
	var info unix.Stat_t
	if unix.Fstatat(dir, "test-fault", &info, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return ""
	}
	if unix.Unlinkat(dir, "test-fault", 0) != nil || unix.Fsync(dir) != nil {
		return ""
	}
	return mode
}
func putFaultFile(dir int, name, text string) {
	fd, e := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW, 0600)
	if e != nil {
		panic(e)
	}
	n, e := unix.Write(fd, []byte(text))
	if e != nil || n != len(text) {
		unix.Close(fd)
		panic("cannot write cleanup test replacement")
	}
	e = unix.Fsync(fd)
	unix.Close(fd)
	if e != nil {
		panic(e)
	}
}
func ownCaptureRename(parent int, name string, records int, token string) error {
	mode := ""
	if strings.HasSuffix(token, ".artifact") && os.Getenv("KAME_CLEAN_TEST_FAULT") != "retire-crash" {
		mode = takeFault(records)
	}
	switch mode {
	case "replace-capture", "replace-occupied", "replace-symlink":
		if e := unix.Renameat(parent, name, parent, "test-original"); e != nil {
			panic(e)
		}
		if mode == "replace-symlink" {
			if e := unix.Symlinkat("../input", parent, name); e != nil {
				panic(e)
			}
		} else {
			putFaultFile(parent, name, "replacement\n")
		}
	}
	e := unix.Renameat(parent, name, records, token)
	if e == nil {
		switch mode {
		case "cancel-capture":
			if e := unix.Kill(os.Getpid(), unix.SIGTERM); e != nil {
				panic(e)
			}
			deadline := time.Now().Add(time.Second)
			for !posix.SignalPending() {
				if time.Now().After(deadline) {
					panic("cleanup test signal not delivered")
				}
				time.Sleep(time.Millisecond)
			}
		case "replace-occupied":
			putFaultFile(parent, name, "new-writer\n")
		}
	}
	return e
}
func ownLedgerRemove(dir int, name string) error {
	if strings.HasSuffix(name, ".record") && os.Getenv("KAME_CLEAN_TEST_FAULT") == "retire-crash" && takeFault(dir) == "retire-crash" {
		unix.Kill(os.Getpid(), unix.SIGKILL)
		panic("cleanup test process survived SIGKILL")
	}
	return unix.Unlinkat(dir, name, 0)
}
