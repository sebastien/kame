// Package ownership implements conservative historical cleanup on native hosts.
package ownership

import (
	"kame/core"
	"kame/host/posix"
	"solod.dev/so/c"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

//so:embed native.h
var native_h string

//so:embed native.c
var native_c string

//so:extern km_own_store
type nativeStore struct{}

//so:extern km_own_file
type nativeFile struct{}

//so:extern
func km_own_supported() bool { return false }

func Supported() bool { return km_own_supported() }

//so:extern nodecay
func km_own_open(root string, create bool, status *c.Int) *nativeStore {
	_, _, _ = root, create, status
	return nil
}

//so:extern
func km_own_lock_fd(s *nativeStore) c.Int { _ = s; return -1 }

//so:extern nodecay
func km_own_resource_fd(s *nativeStore, name string, status *c.Int) c.Int {
	_, _, _ = s, name, status
	return -1
}

//so:extern
func km_own_fd_close(fd c.Int) { _ = fd }

//so:extern
func km_own_close(s *nativeStore) { _ = s }

//so:extern nodecay
func km_own_root(s *nativeStore, data []byte) c.Int { _, _ = s, data; return -1 }

//so:extern nodecay
func km_own_read(s *nativeStore, name string, artifact bool, status *c.Int) *nativeFile {
	_, _, _, _ = s, name, artifact, status
	return nil
}

//so:extern
func km_own_size(f *nativeFile) int64 { _ = f; return 0 }

//so:extern nodecay
func km_own_stamp(f *nativeFile, data []byte) c.Int { _, _ = f, data; return -1 }

//so:extern nodecay
func km_own_bytes(f *nativeFile, data []byte) c.Int { _, _ = f, data; return -1 }

//so:extern
func km_own_stable(f *nativeFile) bool { _ = f; return false }

//so:extern
func km_own_file_close(f *nativeFile) { _ = f }

//so:extern nodecay
func km_own_write(s *nativeStore, name string, data []byte) c.Int { _, _, _ = s, name, data; return -1 }

//so:extern nodecay
func km_own_remove(s *nativeStore, name string) c.Int { _, _ = s, name; return -1 }

//so:extern nodecay
func km_own_list(s *nativeStore, data []byte) c.Int { _, _ = s, data; return -1 }

//so:extern nodecay
func km_own_capture(s *nativeStore, name string, token string, expected []byte) c.Int {
	_, _, _, _ = s, name, token, expected
	return -1
}

//so:extern nodecay
func km_own_restore(s *nativeStore, name string, token string) c.Int {
	_, _, _ = s, name, token
	return -1
}

const (
	leaseNone = iota
	leaseShared
	leaseExclusive
)

// heldLock is one process-wide reference to a resource lock file. Producers of
// disjoint outputs or different targets coexist; a repeated key only raises the
// reference count, matching the cache stripe behavior.
type heldLock struct {
	Key  string
	FD   int
	Refs int
}

type Store struct {
	Alloc       mem.Allocator
	Root        string
	native      *nativeStore
	ID          [16]byte
	Status      int
	PollCancel  func(any) bool
	CancelState any
	Interrupted bool
	lease       int
	locks       []heldLock
}

func (s *Store) cancelled() bool {
	if s.Interrupted {
		return true
	}
	if s.PollCancel != nil && s.PollCancel(s.CancelState) {
		s.Interrupted = true
	}
	return s.Interrupted
}

func New(a mem.Allocator, root string) *Store {
	s := mem.Alloc[Store](a)
	s.Alloc, s.Root = a, path.Clean(a, root)
	return s
}

// Open takes the exclusive project lease without waiting. Protection and
// cleanup stay fail-fast while producers hold a shared lease.
func (s *Store) Open(create bool) bool {
	return s.exclusive(create)
}

// Acquire admits a producer under the shared project lease. Concurrent
// producers coexist; cleanup still requires the exclusive lease.
func (s *Store) Acquire() bool {
	return s.lockLease(true, true, true)
}

func (s *Store) openNative(create bool) bool {
	if s.native != nil {
		return true
	}
	var status c.Int
	s.native = km_own_open(s.Root, create, &status)
	s.Status = int(status)
	if s.native == nil {
		return false
	}
	if km_own_root(s.native, s.ID[:]) != 0 {
		km_own_close(s.native)
		s.native = nil
		s.Status = -1
		return false
	}
	return true
}

func (s *Store) leasePath() string {
	return path.Join(s.Alloc, s.Root, ".kame/outputs/lock")
}

func (s *Store) lockLease(create bool, wait bool, shared bool) bool {
	if !s.openNative(create) {
		return false
	}
	if s.lease == leaseExclusive || (shared && s.lease == leaseShared) {
		return true
	}
	lockPath := s.leasePath()
	defer mem.FreeString(s.Alloc, lockPath)
	if s.lease == leaseShared {
		// flock conversion is not atomic; release before requesting exclusive.
		posix.UnlockFile(int(km_own_lock_fd(s.native)), false)
		s.lease = leaseNone
	}
	status := posix.LockFile(int(km_own_lock_fd(s.native)), lockPath, "project", wait, shared)
	s.Status = status
	if status != 0 {
		return false
	}
	if shared {
		s.lease = leaseShared
	} else {
		s.lease = leaseExclusive
	}
	return true
}

// exclusive upgrades a shared lease and restores it when the exclusive attempt
// is busy or cancelled, so the rest of this run keeps the cleanup boundary.
func (s *Store) exclusive(create bool) bool {
	wasShared := s.lease == leaseShared
	if s.lockLease(create, false, false) {
		return true
	}
	failed := s.Status
	if wasShared && s.native != nil {
		lockPath := s.leasePath()
		restored := posix.LockFile(int(km_own_lock_fd(s.native)), lockPath, "project", true, true)
		mem.FreeString(s.Alloc, lockPath)
		if restored == 0 {
			s.lease = leaseShared
		}
	}
	s.Status = failed
	return false
}

// share downgrades an exclusive lease after cleanup so later producers in this
// run remain concurrent with other invocations.
func (s *Store) share() bool {
	if s.lease == leaseExclusive {
		posix.UnlockFile(int(km_own_lock_fd(s.native)), true)
		s.lease = leaseNone
	}
	return s.lockLease(false, true, true)
}

// finishClean restores the lease that existed before an in-process cleanup.
// A producer keeps its shared boundary; a standalone clean releases exclusivity
// instead of blocking other invocations for the remainder of the run.
func (s *Store) finishClean(shared bool) {
	if s.native == nil {
		return
	}
	if shared {
		s.share()
		return
	}
	if s.lease == leaseExclusive {
		posix.UnlockFile(int(km_own_lock_fd(s.native)), true)
		s.lease = leaseNone
	}
}

// LockResource serializes one declared output path or target name. Waits are
// cancellable and reported through the shared lock observer.
func (s *Store) LockResource(name string, kind string) bool {
	if name == "" || !s.Acquire() {
		return false
	}
	identity := kind + "\x00" + name
	lockKey := key(s.Alloc, identity)
	for i := range s.locks {
		if s.locks[i].Key == lockKey {
			mem.FreeString(s.Alloc, lockKey)
			s.locks[i].Refs++
			s.Status = 0
			return true
		}
	}
	var status c.Int
	fd := int(km_own_resource_fd(s.native, lockKey, &status))
	if fd < 0 {
		mem.FreeString(s.Alloc, lockKey)
		s.Status = int(status)
		return false
	}
	s.Status = posix.LockFile(fd, name, kind, true, false)
	if s.Status != 0 {
		km_own_fd_close(c.Int(fd))
		mem.FreeString(s.Alloc, lockKey)
		return false
	}
	s.locks = slices.Append(s.Alloc, s.locks, heldLock{Key: lockKey, FD: fd, Refs: 1})
	return true
}

func (s *Store) UnlockResource(name string, kind string) {
	identity := kind + "\x00" + name
	lockKey := key(s.Alloc, identity)
	for i := range s.locks {
		if s.locks[i].Key != lockKey {
			continue
		}
		s.locks[i].Refs--
		if s.locks[i].Refs > 0 {
			mem.FreeString(s.Alloc, lockKey)
			return
		}
		posix.UnlockFile(s.locks[i].FD, true)
		km_own_fd_close(c.Int(s.locks[i].FD))
		mem.FreeString(s.Alloc, s.locks[i].Key)
		last := len(s.locks) - 1
		s.locks[i] = s.locks[last]
		s.locks = s.locks[:last]
		mem.FreeString(s.Alloc, lockKey)
		return
	}
	mem.FreeString(s.Alloc, lockKey)
}

func (s *Store) Free() {
	if s == nil {
		return
	}
	for i := range s.locks {
		posix.UnlockFile(s.locks[i].FD, true)
		km_own_fd_close(c.Int(s.locks[i].FD))
		mem.FreeString(s.Alloc, s.locks[i].Key)
	}
	slices.Free(s.Alloc, s.locks)
	if s.native != nil {
		if s.lease != leaseNone {
			posix.UnlockFile(int(km_own_lock_fd(s.native)), s.lease == leaseExclusive)
		}
		km_own_close(s.native)
	}
	mem.FreeString(s.Alloc, s.Root)
	mem.Free(s.Alloc, s)
}

func clone(a mem.Allocator, text string) string {
	b := slices.Make[byte](a, len(text))
	copy(b, text)
	return string(b)
}

func hash(data []byte) core.Signature {
	d := core.NewDigest()
	d.Write(data)
	result := core.Signature{Mode: core.SignatureContent}
	d.Sum(result.Digest[:])
	return result
}

func key(a mem.Allocator, text string) string {
	d := hash([]byte(text))
	b := slices.Make[byte](a, 64)
	const hex = "0123456789abcdef"
	for i := range d.Digest {
		b[i*2], b[i*2+1] = hex[d.Digest[i]>>4], hex[d.Digest[i]&15]
	}
	return string(b)
}

type observation struct {
	Stamp  [48]byte
	Digest [32]byte
	Status int
}

func (s *Store) observe(name string, artifact bool) observation {
	var status c.Int
	f := km_own_read(s.native, name, artifact, &status)
	result := observation{Status: int(status)}
	if f == nil {
		return result
	}
	km_own_stamp(f, result.Stamp[:])
	buffer := slices.Make[byte](s.Alloc, 32768)
	d := core.NewDigest()
	for {
		if s.cancelled() {
			result.Status = -4
			break
		}
		n := int(km_own_bytes(f, buffer))
		if n < 0 {
			result.Status = -1
			break
		}
		if n == 0 {
			break
		}
		d.Write(buffer[:n])
	}
	if result.Status == 0 && !km_own_stable(f) {
		result.Status = 2
	}
	d.Sum(result.Digest[:])
	slices.Free(s.Alloc, buffer)
	km_own_file_close(f)
	return result
}

func (s *Store) read(name string) []byte {
	var status c.Int
	f := km_own_read(s.native, name, false, &status)
	if f == nil {
		s.Status = int(status)
		return nil
	}
	size := km_own_size(f)
	if size < 0 || size > 16*1024*1024 {
		km_own_file_close(f)
		s.Status = -3
		return nil
	}
	data := slices.Make[byte](s.Alloc, int(size))
	used := 0
	for used < len(data) {
		n := int(km_own_bytes(f, data[used:]))
		if n <= 0 {
			break
		}
		used += n
	}
	valid := used == len(data) && km_own_stable(f)
	km_own_file_close(f)
	if !valid {
		slices.Free(s.Alloc, data)
		s.Status = -1
		return nil
	}
	s.Status = 0
	return data
}
