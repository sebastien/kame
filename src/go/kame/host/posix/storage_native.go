package posix

import (
	"solod.dev/so/c"
	"solod.dev/so/os"
)

// Solod 0.4's os.File stores its native FILE* in fd; keep that ABI seam here.
func syncStorage(f *os.File) c.Int {
	c.Raw("return km_file_sync(f->fd);")
	return -1
}

//so:extern nodecay
func km_file_sync_parent(directory string) c.Int { _ = directory; return -1 }

// ForceEventAllocationFailureForTest fails native event/capture allocations
// after count successes. A negative count disables the process-global probe.
func ForceEventAllocationFailureForTest(count int) {
	km_event_force_allocation_failure(c.Int(count))
}

//so:extern
func km_event_force_allocation_failure(count c.Int) { _ = count }
