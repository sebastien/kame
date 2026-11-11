package posix

// macOS has no /proc lock ownership table; validated metadata is advisory.
func lockKernelOwner(fd int) int64 { return 0 }
