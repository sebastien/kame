//go:build !linux && !darwin

package posix

import "os"

// Without a known stat layout, digests are not memoized; every signature is
// computed from content.
func statIdentity(os.FileInfo) (fileIdentity, bool) { return fileIdentity{}, false }
