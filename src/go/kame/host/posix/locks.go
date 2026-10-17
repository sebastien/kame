package posix

import "solod.dev/so/c"

// LockEvent strings are borrowed for the duration of the observer call.
// OwnerVerified distinguishes an OS-confirmed owner from recorded metadata.
//
//so:extern km_lock_event
type LockEvent struct {
	State         string
	Kind          string
	Path          string
	OwnerPID      int64
	OwnerCommand  string
	OwnerVerified bool
	ElapsedMS     int64
	HeldMS        int64
}

//so:extern nodecay
func km_cli_observe_locks(observer func(*LockEvent) bool, command string) { _, _ = observer, command }

// ObserveLocks installs a synchronous CLI observer; returning false cancels a
// wait. A nil observer leaves embedding callers silent. Command excludes values.
func ObserveLocks(observer func(*LockEvent) bool, command string) {
	km_cli_observe_locks(observer, command)
}

//so:extern nodecay
func km_file_lock(fd c.Int, name string, kind string, wait bool) c.Int {
	_, _, _, _ = fd, name, kind, wait
	return -1
}

// LockFile preserves flock semantics: 0 acquired, -2 busy, -4 interrupted,
// -1 host failure. Nonwaiting callers receive a busy observation, not a wait.
func LockFile(fd int, name string, kind string, wait bool) int {
	return int(km_file_lock(c.Int(fd), name, kind, wait))
}

//so:extern
func km_file_unlock(fd c.Int) { _ = fd }

func UnlockFile(fd int) { km_file_unlock(c.Int(fd)) }

//so:extern
func km_cli_signal_pending() bool { return false }

// SignalPending does not consume the command loop's signal notification.
func SignalPending() bool { return km_cli_signal_pending() }
