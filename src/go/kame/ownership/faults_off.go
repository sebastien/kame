//go:build (linux || darwin) && !kame_faults

package ownership

import "golang.org/x/sys/unix"

func ownCaptureRename(parent int, name string, records int, token string) error {
	return unix.Renameat(parent, name, records, token)
}
func ownLedgerRemove(dir int, name string) error { return unix.Unlinkat(dir, name, 0) }
