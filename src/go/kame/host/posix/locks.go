package posix

import (
	"fmt"
	"kame/internal/c"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type LockEvent struct {
	State, Kind, Path string
	OwnerPID          int64
	OwnerCommand      string
	OwnerVerified     bool
	ElapsedMS, HeldMS int64
}

var lockObserver func(*LockEvent) bool
var lockCommand string

func ObserveLocks(observer func(*LockEvent) bool, command string) {
	lockObserver, lockCommand = observer, lockSummary(command)
}
func lockSummary(command string) string {
	command = command[:min(len(command), 127)]
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return '?'
		}
		return r
	}, command)
}
func lockMetadataSafe(fd int) bool {
	var info syscall.Stat_t
	return syscall.Fstat(fd, &info) == nil && info.Mode&syscall.S_IFMT == syscall.S_IFREG && info.Nlink == 1
}
func lockProcessStart(pid int64) uint64 {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return 0
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return 0
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) <= 19 {
		return 0
	}
	start, _ := strconv.ParseUint(fields[19], 10, 64)
	return start
}
func lockNotice(fd int, name, kind, state string, elapsed int64) bool {
	if lockObserver == nil {
		return !SignalPending()
	}
	event := LockEvent{State: state, Path: name, Kind: kind, ElapsedMS: elapsed, HeldMS: -1, OwnerPID: lockKernelOwner(fd)}
	event.OwnerVerified = event.OwnerPID > 0
	var data [320]byte
	if n, _ := syscall.Pread(fd, data[:], 0); n > 0 {
		header, summary, found := strings.Cut(string(data[:n]), "\n")
		var pid, since int64
		var birth uint64
		if count, _ := fmt.Sscanf(header, "kame-lock-v1 %d %d %d", &pid, &birth, &since); count == 3 && found && pid > 0 && pid <= 2147483647 && (birth == 0 || birth == lockProcessStart(pid)) {
			alive := syscall.Kill(int(pid), 0)
			if pid == event.OwnerPID || (event.OwnerPID == 0 && (alive == nil || alive == syscall.EPERM)) {
				event.OwnerPID = pid
				command, _, _ := strings.Cut(summary, "\n")
				event.OwnerCommand = lockSummary(command)
				if now := time.Now().UnixMilli(); since > 0 && since <= now {
					event.HeldMS = now - since
				}
			}
		}
	}
	if event.OwnerPID > 0 && event.OwnerCommand == "" {
		if b, e := os.ReadFile(fmt.Sprintf("/proc/%d/comm", event.OwnerPID)); e == nil {
			event.OwnerCommand = lockSummary(strings.TrimSpace(string(b)))
		}
	}
	return lockObserver(&event)
}
func LockFile(fd int, name, kind string, wait, shared bool) int {
	if !lockMetadataSafe(fd) {
		return -1
	}
	op := syscall.LOCK_EX
	if shared {
		op = syscall.LOCK_SH
	}
	start := time.Now()
	waited := false
	for {
		if SignalPending() {
			return -4
		}
		err := syscall.Flock(fd, op|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return -1
		}
		state := "waiting"
		if !wait {
			state = "busy"
		}
		if !lockNotice(fd, name, kind, state, time.Since(start).Milliseconds()) {
			return -4
		}
		if !wait {
			return -2
		}
		waited = true
		if SignalPending() {
			return -4
		}
		time.Sleep(20 * time.Millisecond)
	}
	if SignalPending() {
		syscall.Flock(fd, syscall.LOCK_UN)
		return -4
	}
	data := []byte(fmt.Sprintf("kame-lock-v1 %d %d %d\n%s\n", os.Getpid(), lockProcessStart(int64(os.Getpid())), time.Now().UnixMilli(), lockCommand))
	if lockMetadataSafe(fd) && syscall.Ftruncate(fd, 0) == nil {
		syscall.Pwrite(fd, data, 0)
	}
	if waited {
		lockNotice(fd, name, kind, "acquired", time.Since(start).Milliseconds())
	}
	return 0
}
func UnlockFile(fd int, clear bool) {
	if !clear && syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		clear = true
	}
	if clear && lockMetadataSafe(fd) {
		syscall.Ftruncate(fd, 0)
	}
	syscall.Flock(fd, syscall.LOCK_UN)
}
func SignalPending() bool { return km_cli_signal_pending() }
func km_file_lock(fd c.Int, name, kind string, wait, shared bool) c.Int {
	return c.Int(LockFile(int(fd), name, kind, wait, shared))
}
func km_file_unlock(fd c.Int, clear bool) { UnlockFile(int(fd), clear) }
